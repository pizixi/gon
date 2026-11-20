package ui

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"slices"
	"testing"
	"unsafe"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"golang.org/x/sys/windows"

	"gon/internal/config"
	"gon/internal/pty"
)

const (
	wmPointerDown   = 0x0246
	wmPointerUp     = 0x0247
	wmPointerWheel  = 0x024e
	wmPointerHWheel = 0x024f
)

func TestSidebarNativeMouseCoordinates(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread := uintptr(windows.GetCurrentThreadId())
	original, _, _ := user32.NewProc("GetThreadDesktop").Call(thread)
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("gon-sidebar-test-%d-%d", os.Getpid(), thread))
	desktop, _, err := user32.NewProc("CreateDesktopW").Call(uintptr(unsafe.Pointer(name)), 0, 0, 0, 0x10000000, 0)
	if desktop == 0 {
		t.Skipf("private desktop unavailable: %v", err)
	}
	defer user32.NewProc("CloseDesktop").Call(desktop)
	setDesktop := user32.NewProc("SetThreadDesktop")
	if ok, _, err := setDesktop.Call(desktop); ok == 0 {
		t.Skipf("cannot attach to test desktop: %v", err)
	}
	defer setDesktop.Call(original)
	previous, _, _ := setThreadDPIContext.Call(^uintptr(3)) // Per-monitor aware v2.
	if previous == 0 {
		t.Skip("per-monitor DPI context unavailable")
	}
	defer setThreadDPIContext.Call(previous)

	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 300, 200, 1800, 680, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Skipf("Win32 window unavailable: %v", err)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)

	var h *layoutTest
	var info nativePointerInfo
	read := readNativePointerInfo
	cursor := readNativeMouseCursor
	readNativePointerInfo = func(id uint32) (nativePointerInfo, bool) { return info, id == info.ID }
	defer func() { readNativePointerInfo, readNativeMouseCursor = read, cursor }()

	clientToScreen := user32.NewProc("ClientToScreen")
	pressed := false
	mouse := func(msg uint32, at image.Point, scaledMessage bool) {
		t.Helper()
		point := nativePoint{X: int32(at.X), Y: int32(at.Y)}
		if ok, _, _ := clientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&point))); ok == 0 {
			t.Fatal("convert test point to screen coordinates")
		}
		info = nativePointerInfo{Type: ptMouse, ID: 7, Target: hwnd, Pixel: point}
		physical := point
		readNativeMouseCursor = func(uintptr) (nativePoint, bool) { return physical, true }
		switch msg {
		case wmPointerDown:
			pressed = true
		case wmPointerUp:
			pressed = false
		}
		if pressed {
			info.Flags |= 0x10
		}
		if scaledMessage {
			point.X /= 2
			point.Y /= 2
			info.Pixel = point
		}
		params := uintptr(uint32(uint16(point.X)) | uint32(uint16(point.Y))<<16)
		params = pointerMessagePosition(hwnd, msg, uintptr(info.ID), params)
		// Match Gio's Windows input path after the native filter: decode
		// lParam and convert screen coordinates to client pixels. Injecting
		// WM_POINTER messages with fabricated IDs is rejected by Windows.
		point = nativePoint{X: int32(int16(params)), Y: int32(int16(params >> 16))}
		if ok, _, _ := screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&point))); ok == 0 {
			t.Fatal("convert test point to client coordinates")
		}
		kind, buttons := pointer.Move, pointer.Buttons(0)
		if pressed {
			buttons = pointer.ButtonPrimary
		}
		switch msg {
		case wmPointerDown:
			kind = pointer.Press
		case wmPointerUp:
			kind, buttons = pointer.Release, 0
		}
		h.router.Queue(pointer.Event{
			Kind: kind, Source: pointer.Mouse, PointerID: pointer.ID(info.ID),
			Position: f32.Pt(float32(point.X), float32(point.Y)), Buttons: buttons,
		})
		h.frame()
	}

	for _, count := range []int{6, 18} {
		t.Run(fmt.Sprintf("stationary/tabs=%d", count), func(t *testing.T) {
			h = sidebarInputTest(t, 2, count)
			tab := h.activeTab()
			row := h.controlBounds(t, fmt.Sprintf("Tab %d", count), 1)
			at := row.Min.Add(row.Size().Div(2))
			mouse(wmPointerDown, at, false)
			if h.tabDrag.tab != tab || h.tabDrag.start != at {
				t.Fatalf("native press = %+v, want %v", h.tabDrag, at)
			}
			for range 5 {
				mouse(wmPointerUpdate, at, true)
				if h.tabDrag.tab != tab || h.tabDrag.moving || h.tabDrag.at != at {
					t.Fatalf("captured stationary mouse jumped: %+v, want %v", h.tabDrag, at)
				}
			}
			mouse(wmPointerUp, at, false)
			if h.activeTab() != tab || h.tabDrag.tab != nil {
				t.Fatal("stationary release changed the tab or left a drag active")
			}
		})
		t.Run(fmt.Sprintf("single-click/tabs=%d", count), func(t *testing.T) {
			h = sidebarInputTest(t, 2, count)
			tab := h.tabs[count-2]
			row := h.controlBounds(t, fmt.Sprintf("Tab %d", count-1), 0)
			at := row.Min.Add(row.Size().Div(2))
			mouse(wmPointerDown, at, false)
			mouse(wmPointerUp, at, false)
			if h.activeTab() != tab {
				t.Fatal("one native click did not activate the tab")
			}
			close := tabCloseBounds(layout.Context{Metric: h.metric}, row)
			at = close.Min.Add(close.Size().Div(2))
			mouse(wmPointerUpdate, at, false)
			mouse(wmPointerDown, at, false)
			if h.tabDrag.tab != nil {
				t.Fatal("native close button press started a tab drag")
			}
			mouse(wmPointerUpdate, at, true)
			mouse(wmPointerUp, at, false)
			if slices.Contains(h.tabs, tab) || len(h.tabs) != count-1 {
				t.Fatal("one native click did not close the tab")
			}
		})
	}

	t.Run("window buttons", func(t *testing.T) {
		h = sidebarInputTest(t, 2, 6)
		for _, label := range []string{"最小化", "最大化", "关闭窗口"} {
			row := h.controlBounds(t, label, 0)
			at := row.Min.Add(row.Size().Div(2))
			mouse(wmPointerUpdate, at, false)
			mouse(wmPointerDown, at, false)
			mouse(wmPointerUpdate, at, true)
			mouse(wmPointerUp, at, false)
		}
		for i := range h.winButtons {
			history := h.winButtons[i].History()
			if len(history) != 1 || history[0].End.IsZero() || history[0].Cancelled {
				t.Fatalf("window button %d click was cancelled: %+v", i, history)
			}
		}
	})
	t.Run("new tab button", func(t *testing.T) {
		if !pty.Available() {
			t.Skip("ConPTY unavailable")
		}
		dir := t.TempDir()
		h = sidebarInputTest(t, 2, 6)
		h.cfg.Shells = []config.Shell{{Name: "cmd", Command: "cmd.exe", Args: []string{"/D", "/Q"}, Dir: dir}}
		h.cfg.DefaultShell = 0
		row := h.controlBounds(t, "新建标签页", 0)
		at := row.Min.Add(row.Size().Div(2))
		mouse(wmPointerUpdate, at, false)
		mouse(wmPointerDown, at, false)
		mouse(wmPointerUpdate, at, true)
		mouse(wmPointerUp, at, false)
		if len(h.tabs) != 7 || h.active != 6 {
			t.Fatalf("one click did not create and activate a new tab: tabs=%d active=%d", len(h.tabs), h.active)
		}
	})
}

func TestPointerMessagePositionPreservesOtherInput(t *testing.T) {
	read := readNativePointerInfo
	defer func() { readNativePointerInfo = read }()
	const hwnd, params = uintptr(10), uintptr(0xffceffb0)
	for _, tc := range []struct {
		name string
		msg  uint32
		info nativePointerInfo
		ok   bool
	}{
		{name: "unavailable", msg: wmPointerUpdate},
		{name: "touch", msg: wmPointerUpdate, info: nativePointerInfo{Type: 2, Target: hwnd, Flags: 0x10}, ok: true},
		{name: "other window", msg: wmPointerUpdate, info: nativePointerInfo{Type: ptMouse, Target: hwnd + 1, Flags: 0x10}, ok: true},
		{name: "keyboard", msg: wmChar, info: nativePointerInfo{Type: ptMouse, Target: hwnd}, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readNativePointerInfo = func(uint32) (nativePointerInfo, bool) { return tc.info, tc.ok }
			if got := pointerMessagePosition(hwnd, tc.msg, 7, params); got != params {
				t.Fatalf("lParam = %#x, want %#x", got, params)
			}
		})
	}
}

func TestPointerMessageCursorFallbackAndInputBoundaries(t *testing.T) {
	read, cursor := readNativePointerInfo, readNativeMouseCursor
	defer func() { readNativePointerInfo, readNativeMouseCursor = read, cursor }()
	const hwnd = uintptr(10)
	for _, tc := range []struct {
		name     string
		msg      uint32
		flags    uint32
		cursorOK bool
		want     nativePoint
	}{
		{name: "captured update", msg: wmPointerUpdate, flags: 0x10, cursorOK: true, want: nativePoint{517, 76}},
		{name: "cursor unavailable", msg: wmPointerUpdate, flags: 0x10, want: nativePoint{414, 61}},
		{name: "hover", msg: wmPointerUpdate, cursorOK: true, want: nativePoint{414, 61}},
		{name: "press", msg: wmPointerDown, flags: 0x10, cursorOK: true, want: nativePoint{414, 61}},
		{name: "release", msg: wmPointerUp, cursorOK: true, want: nativePoint{414, 61}},
		{name: "wheel", msg: wmPointerWheel, flags: 0x10, cursorOK: true, want: nativePoint{414, 61}},
		{name: "horizontal wheel", msg: wmPointerHWheel, flags: 0x10, cursorOK: true, want: nativePoint{414, 61}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readNativePointerInfo = func(uint32) (nativePointerInfo, bool) {
				return nativePointerInfo{Type: ptMouse, Target: hwnd, Flags: tc.flags, Pixel: nativePoint{414, 61}}, true
			}
			readNativeMouseCursor = func(uintptr) (nativePoint, bool) {
				if tc.msg != wmPointerUpdate || tc.flags == 0 {
					t.Error("sampled the current cursor across a discrete input boundary")
				}
				return nativePoint{517, 76}, tc.cursorOK
			}
			params := pointerMessagePosition(hwnd, tc.msg, 7, uintptr(uint32(414)|uint32(61)<<16))
			got := nativePoint{int32(int16(params)), int32(int16(params >> 16))}
			if got != tc.want {
				t.Fatalf("position = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNativePointerInfoLayout(t *testing.T) {
	var info nativePointerInfo
	wantSize, wantPixel, wantPerformance := uintptr(96), uintptr(32), uintptr(80)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		wantSize, wantPixel, wantPerformance = 88, 24, 72
	}
	if unsafe.Sizeof(info) != wantSize || unsafe.Offsetof(info.Pixel) != wantPixel || unsafe.Offsetof(info.PerformanceCount) != wantPerformance {
		t.Fatalf("POINTER_INFO layout: size=%d pixel=%d performance=%d", unsafe.Sizeof(info), unsafe.Offsetof(info.Pixel), unsafe.Offsetof(info.PerformanceCount))
	}
}
