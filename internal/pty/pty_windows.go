//go:build windows

package pty

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ptmx is the platform pseudo terminal handle.
type ptmx interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procInitializeProcThreadAttr  = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttr      = kernel32.NewProc("DeleteProcThreadAttributeList")
)

// procThreadAttributePseudoConsole is PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE.
const procThreadAttributePseudoConsole = 0x00020016
const glyphWidthGraphemes = 0x08 // PSEUDOCONSOLE_GLYPH_WIDTH_GRAPHEMES

type consoleAPI struct {
	create, resize, close *windows.Proc
	modern                bool
}

func consoleProcs(dll *windows.DLL, prefix string) *consoleAPI {
	create, a := dll.FindProc(prefix + "CreatePseudoConsole")
	resize, b := dll.FindProc(prefix + "ResizePseudoConsole")
	close, c := dll.FindProc(prefix + "ClosePseudoConsole")
	if a != nil || b != nil || c != nil {
		return nil
	}
	return &consoleAPI{create: create, resize: resize, close: close, modern: prefix != ""}
}

var systemConsole = sync.OnceValue(func() *consoleAPI {
	if kernel32.Load() != nil {
		return nil
	}
	return consoleProcs(&windows.DLL{Name: "kernel32.dll", Handle: windows.Handle(kernel32.Handle())}, "")
})

var preferredConsole = sync.OnceValue(func() *consoleAPI {
	// The updated Windows Terminal host forwards VT input modes that older
	// versions of the Windows 10 console host swallow.
	if path, err := bundledConsolePath(); err == nil && path != "" {
		if handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32); err == nil {
			if api := consoleProcs(&windows.DLL{Name: path, Handle: handle}, "Conpty"); api != nil {
				return api
			}
			_ = windows.FreeLibrary(handle)
		}
	}
	return systemConsole()
})

// Available reports whether ConPTY is usable on this system.
func Available() bool {
	if preferredConsole() == nil {
		return false
	}
	for _, p := range []*windows.LazyProc{
		procInitializeProcThreadAttr, procUpdateProcThreadAttribute,
		procDeleteProcThreadAttr,
	} {
		if err := p.Find(); err != nil {
			return false
		}
	}
	return true
}

type conPTY struct {
	api  *consoleAPI
	hpc  windows.Handle
	in   windows.Handle
	out  windows.Handle
	proc windows.Handle

	mu        sync.RWMutex
	closeOnce sync.Once
}

// ModernConsole reports whether this session uses the bundled console host.
func (s *Session) ModernConsole() bool {
	console, ok := s.pty.(*conPTY)
	return ok && console.api.modern
}

func startPTY(command string, args []string, dir string, env []string, cols, rows int, s *Session) (ptmx, func(), error) {
	if !Available() {
		return nil, nil, errors.New("ConPTY is not available (Windows 10 1809 or later required)")
	}
	exe, err := resolveCommand(command, dir, environmentMap(env))
	if err != nil {
		return nil, nil, err
	}
	appName, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return nil, nil, err
	}
	if ext := strings.ToLower(filepath.Ext(exe)); ext == ".cmd" || ext == ".bat" {
		// Windows dispatches batch entry points through its command interpreter.
		appName = nil
	}
	cmdLine, err := syscall.UTF16PtrFromString(buildCommandLine(exe, args))
	if err != nil {
		return nil, nil, err
	}
	var dirPtr *uint16
	if dir != "" {
		dirPtr, err = syscall.UTF16PtrFromString(dir)
		if err != nil {
			return nil, nil, err
		}
	}
	environment, err := environmentBlock(env)
	if err != nil {
		return nil, nil, err
	}
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}

	var ptyIn, cmdIn, cmdOut, ptyOut windows.Handle
	if err := windows.CreatePipe(&ptyIn, &cmdIn, nil, 0); err != nil {
		return nil, nil, fmt.Errorf("CreatePipe: %w", err)
	}
	if err := windows.CreatePipe(&cmdOut, &ptyOut, nil, 0); err != nil {
		windows.CloseHandle(ptyIn)
		windows.CloseHandle(cmdIn)
		return nil, nil, fmt.Errorf("CreatePipe: %w", err)
	}

	size := uintptr(uint32(uint16(rows))<<16 | uint32(uint16(cols)))
	var hpc windows.Handle
	api := preferredConsole()
	var consoleFlags uintptr
	if api.modern {
		consoleFlags = glyphWidthGraphemes
	}
	r1, _, _ := api.create.Call(size, uintptr(ptyIn), uintptr(ptyOut), consoleFlags, uintptr(unsafe.Pointer(&hpc)))
	if r1 != 0 && api.modern {
		if fallback := systemConsole(); fallback != nil {
			api = fallback
			r1, _, _ = api.create.Call(size, uintptr(ptyIn), uintptr(ptyOut), 0, uintptr(unsafe.Pointer(&hpc)))
		}
	}
	// ConPTY owns its references to these ends after creation.
	windows.CloseHandle(ptyIn)
	windows.CloseHandle(ptyOut)
	if r1 != 0 {
		windows.CloseHandle(cmdIn)
		windows.CloseHandle(cmdOut)
		return nil, nil, fmt.Errorf("CreatePseudoConsole: HRESULT %#08x", uint32(r1))
	}

	attrList, err := newProcThreadAttributeList(hpc)
	if err != nil {
		windows.CloseHandle(cmdIn)
		windows.CloseHandle(cmdOut)
		api.close.Call(uintptr(hpc))
		return nil, nil, err
	}
	defer attrList.free()

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	// STARTF_USESTDHANDLES is required: without it the child attaches to the
	// parent console instead of the pseudo console.
	si.Flags |= windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrList.ptr()

	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	err = windows.CreateProcess(appName, cmdLine, nil, nil, false, flags,
		&environment[0], dirPtr, &si.StartupInfo, &pi)
	if err != nil {
		windows.CloseHandle(cmdIn)
		windows.CloseHandle(cmdOut)
		api.close.Call(uintptr(hpc))
		return nil, nil, fmt.Errorf("CreateProcess: %w", err)
	}
	windows.CloseHandle(pi.Thread)

	p := &conPTY{api: api, hpc: hpc, in: cmdIn, out: cmdOut, proc: pi.Process}

	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		_ = windows.GetExitCodeProcess(pi.Process, &code)
		p.mu.Lock()
		_ = windows.CloseHandle(p.proc)
		p.proc = 0
		p.mu.Unlock()
		s.finishExit(int(code))
	}()

	// closer only terminates the child; the pseudo console and its handles are
	// released by conPTY.Close, which is idempotent. Closing an HPCON twice
	// corrupts the process heap.
	closer := func() {
		p.mu.RLock()
		defer p.mu.RUnlock()
		if p.proc != 0 {
			_ = windows.TerminateProcess(p.proc, 0)
		}
	}
	return p, closer, nil
}

func environmentBlock(env []string) ([]uint16, error) {
	entries := map[string]string{
		"TERM":         "TERM=xterm-256color",
		"COLORTERM":    "COLORTERM=truecolor",
		"TERM_PROGRAM": "TERM_PROGRAM=gon",
	}
	for _, entry := range env {
		name, _, ok := splitEnv(entry)
		if !ok {
			return nil, errors.New("invalid environment entry")
		}
		entries[strings.ToUpper(name)] = entry
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	var block []uint16
	for _, name := range names {
		value, err := syscall.UTF16FromString(entries[name])
		if err != nil {
			return nil, errors.New("invalid environment entry")
		}
		block = append(block, value...)
	}
	return append(block, 0), nil
}

// procThreadAttributeList wraps the opaque attribute list buffer.
type procThreadAttributeList struct {
	buf []byte
}

func newProcThreadAttributeList(hpc windows.Handle) (*procThreadAttributeList, error) {
	var size uintptr
	if r, _, _ := procInitializeProcThreadAttr.Call(0, 1, 0, uintptr(unsafe.Pointer(&size))); r != 0 {
		return nil, fmt.Errorf("InitializeProcThreadAttributeList(size) returned %d", r)
	}
	if size == 0 {
		return nil, errors.New("InitializeProcThreadAttributeList: zero size")
	}
	buf := make([]byte, size)
	if r, _, e := procInitializeProcThreadAttr.Call(uintptr(unsafe.Pointer(&buf[0])), 1, 0, uintptr(unsafe.Pointer(&size))); r == 0 {
		return nil, fmt.Errorf("InitializeProcThreadAttributeList: %v", e)
	}
	if r, _, e := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(&buf[0])), 0,
		procThreadAttributePseudoConsole, uintptr(hpc), unsafe.Sizeof(hpc), 0, 0); r == 0 {
		procDeleteProcThreadAttr.Call(uintptr(unsafe.Pointer(&buf[0])))
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %v", e)
	}
	return &procThreadAttributeList{buf: buf}, nil
}

func (l *procThreadAttributeList) free() {
	if len(l.buf) != 0 {
		procDeleteProcThreadAttr.Call(uintptr(unsafe.Pointer(&l.buf[0])))
		l.buf = nil
	}
}

func (l *procThreadAttributeList) ptr() *windows.ProcThreadAttributeList {
	if len(l.buf) == 0 {
		return nil
	}
	return (*windows.ProcThreadAttributeList)(unsafe.Pointer(&l.buf[0]))
}

func (p *conPTY) Read(b []byte) (int, error) {
	p.mu.RLock()
	h := p.out
	p.mu.RUnlock()
	if h == 0 {
		return 0, io.EOF
	}
	var n uint32
	err := windows.ReadFile(h, b, &n, nil)
	if err != nil {
		return int(n), err
	}
	return int(n), nil
}

func (p *conPTY) Write(b []byte) (int, error) {
	p.mu.RLock()
	h := p.in
	p.mu.RUnlock()
	if h == 0 {
		return 0, io.ErrClosedPipe
	}
	var n uint32
	err := windows.WriteFile(h, b, &n, nil)
	if err != nil {
		return int(n), err
	}
	return int(n), nil
}

func (p *conPTY) Resize(cols, rows int) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.hpc == 0 {
		return io.ErrClosedPipe
	}
	size := uintptr(uint32(uint16(rows))<<16 | uint32(uint16(cols)))
	r1, _, _ := p.api.resize.Call(uintptr(p.hpc), size)
	if r1 != 0 {
		return fmt.Errorf("ResizePseudoConsole: HRESULT %#08x", uint32(r1))
	}
	return nil
}

func (p *conPTY) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		hpc := p.hpc
		p.hpc = 0
		p.mu.Unlock()
		if hpc != 0 {
			// Windows 10 can emit final output during close. Leave the
			// output pipe readable until ClosePseudoConsole has returned.
			p.api.close.Call(uintptr(hpc))
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		// A pending synchronous ReadFile makes CloseHandle block, so cancel any
		// outstanding I/O on the pipe ends first.
		if p.out != 0 {
			_ = windows.CancelIoEx(p.out, nil)
		}
		if p.in != 0 {
			_ = windows.CancelIoEx(p.in, nil)
		}
		for _, h := range []windows.Handle{p.in, p.out} {
			if h != 0 && h != windows.InvalidHandle {
				windows.CloseHandle(h)
			}
		}
		p.in, p.out = 0, 0
	})
	return nil
}
