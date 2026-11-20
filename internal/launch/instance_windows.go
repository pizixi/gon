package launch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	launchTimeout = 15 * time.Second
	maxRequest    = 16 << 20
)

var allowForeground = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

type loginScope struct {
	user      string
	integrity string
	session   uint32
}

// Instance owns or can take over the launch pipe for an open window.
type Instance struct {
	path     string
	scope    loginScope
	listener net.Listener
}

// Coordinate claims the pipe or forwards a launch to its current owner.
func Coordinate(configPath string, singleWindow bool, request Request) (instance *Instance, forwarded bool, err error) {
	scope, err := scopeOf(windows.GetCurrentProcessId())
	if err != nil {
		return nil, false, err
	}
	if resolved, err := filepath.EvalSymlinks(configPath); err == nil {
		configPath = resolved
	}
	key := fmt.Sprintf("%s/%s/%d/%s", scope.user, scope.integrity, scope.session, strings.ToLower(configPath))
	i := &Instance{scope: scope, path: fmt.Sprintf(`\\.\pipe\gon-v1-%x`, sha256.Sum256([]byte(key)))}
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()
	for {
		i.listener, err = i.listen()
		if err == nil {
			return i, false, nil
		}
		if !pipeInUse(err) {
			return nil, false, err
		}
		if !singleWindow {
			return i, false, nil
		}
		conn, err := winio.DialPipeContext(ctx, i.path)
		if err == nil {
			defer conn.Close()
			return nil, true, i.forward(ctx, conn, request)
		}
		// An owner can close between the claim and the connection attempt.
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || !wait(ctx, 25*time.Millisecond) {
			return nil, false, err
		}
	}
}

func (i *Instance) listen() (net.Listener, error) {
	return winio.ListenPipe(i.path, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + i.scope.user + ")"})
}

func pipeInUse(err error) bool {
	return errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// Serve opens incoming tabs and claims the pipe if an earlier window closes.
func (i *Instance) Serve(ctx context.Context, open func(context.Context, Request) error) {
	listener := i.listener
	for ctx.Err() == nil {
		if listener == nil {
			listener, _ = i.listen()
			if listener == nil {
				wait(ctx, time.Second)
				continue
			}
		}
		i.serve(ctx, listener, open)
		listener = nil
	}
	if listener != nil {
		_ = listener.Close()
	}
}

func (i *Instance) serve(ctx context.Context, listener net.Listener, open func(context.Context, Request) error) {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go i.receive(ctx, conn, open)
	}
}

type reply struct{ Error string }

func (i *Instance) receive(ctx context.Context, conn net.Conn, open func(context.Context, Request) error) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := i.peer(conn, windows.GetNamedPipeClientProcessId); err != nil {
		return
	}
	deadline := time.Now().Add(launchTimeout)
	_ = conn.SetDeadline(deadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var request Request
	err := json.NewDecoder(io.LimitReader(conn, maxRequest)).Decode(&request)
	if err == nil {
		err = request.validate()
	}
	if err == nil {
		err = open(ctx, request)
	}
	var response reply
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func (i *Instance) forward(ctx context.Context, conn net.Conn, request Request) error {
	pid, err := i.peer(conn, windows.GetNamedPipeServerProcessId)
	if err != nil {
		return err
	}
	allowForeground.Call(uintptr(pid))
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	var response reply
	if err := json.NewDecoder(io.LimitReader(conn, 64<<10)).Decode(&response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func (i *Instance) peer(conn net.Conn, getPID func(windows.Handle, *uint32) error) (uint32, error) {
	var pid uint32
	if err := getPID(windows.Handle(conn.(interface{ Fd() uintptr }).Fd()), &pid); err != nil {
		return 0, err
	}
	scope, err := scopeOf(pid)
	if err != nil {
		return 0, err
	}
	if scope != i.scope {
		return 0, errors.New("launch pipe belongs to a different Windows login or integrity level")
	}
	return pid, nil
}

func scopeOf(pid uint32) (loginScope, error) {
	var scope loginScope
	if err := windows.ProcessIdToSessionId(pid, &scope.session); err != nil {
		return scope, err
	}
	token := windows.GetCurrentProcessToken()
	if pid != windows.GetCurrentProcessId() {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return scope, err
		}
		defer windows.CloseHandle(process)
		if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
			return scope, err
		}
		defer token.Close()
	}
	user, err := token.GetTokenUser()
	if err != nil {
		return scope, err
	}
	scope.user = user.User.Sid.String()
	// A TOKEN_MANDATORY_LABEL and its SID fit in this aligned buffer.
	var data [32]uintptr
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&data[0])), uint32(unsafe.Sizeof(data)), &size); err != nil {
		return scope, err
	}
	scope.integrity = (*windows.Tokenmandatorylabel)(unsafe.Pointer(&data[0])).Label.Sid.String()
	return scope, nil
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
