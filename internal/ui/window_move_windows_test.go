package ui

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"gioui.org/unit"
	"golang.org/x/sys/windows"

	"gon/internal/config"
)

func TestWindowMoveDefersPaintUntilRelease(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	thread := uintptr(windows.GetCurrentThreadId())
	original, _, _ := user32.NewProc("GetThreadDesktop").Call(thread)
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("gon-test-%d-%d", os.Getpid(), thread))
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

	class, _ := windows.UTF16PtrFromString("STATIC")
	createWindow := user32.NewProc("CreateWindowExW")
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 120, 120, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Skipf("Win32 window unavailable: %v", err)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	user32.NewProc("ShowWindow").Call(hwnd, 4) // SW_SHOWNOACTIVATE

	updates, paints := 0, 0
	probe := syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam, _, _ uintptr) uintptr {
		switch msg {
		case wmWindowPosChanged:
			updates++
		case wmPaint:
			paints++
		}
		result, _, _ := procDefSubclassProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return result
	})
	if ok, _, err := procSetWindowSubclass.Call(hwnd, probe, 2, 0); ok == 0 {
		t.Fatalf("install probe: %v", err)
	}
	defer procRemoveWindowSubclass.Call(hwnd, probe, 2)
	installWindowMoveFilter(hwnd)
	if _, ok := windowMoveStates.Load(hwnd); !ok {
		t.Fatal("move filter was not installed")
	}

	send := user32.NewProc("SendMessageW")
	getUpdateRect := user32.NewProc("GetUpdateRect")
	move := user32.NewProc("SetWindowPos")
	const (
		noActivateOrZOrder = 0x0010 | 0x0004
		swpNoMove          = 0x0002
	)
	bounds := [4]int32{32, 36, 152, 156}

	send.Call(hwnd, wmEnterSizeMove, 0, 0)
	send.Call(hwnd, wmMoving, 0, uintptr(unsafe.Pointer(&bounds)))
	move.Call(hwnd, 0, 32, 36, 120, 120, noActivateOrZOrder|swpNoSize)
	if updates != 0 {
		t.Fatal("position-only drag reached the redraw handler")
	}
	unchanged := windowPosition{hwnd: hwnd, flags: swpNoSize | swpNoMove}
	send.Call(hwnd, wmWindowPosChanged, 0, uintptr(unsafe.Pointer(&unchanged)))
	if updates != 0 {
		t.Fatal("redundant position notification reached the redraw handler")
	}
	procInvalidateRect.Call(hwnd, 0, 0)
	send.Call(hwnd, wmPaint, 0, 0)
	if paints != 0 {
		t.Fatal("paint reached the redraw handler during move")
	}
	if pending, _, _ := getUpdateRect.Call(hwnd, 0, 0); pending != 0 {
		t.Fatal("deferred paint left an outstanding update region")
	}

	send.Call(hwnd, wmSizing, 8, uintptr(unsafe.Pointer(&bounds))) // WMSZ_BOTTOMRIGHT
	move.Call(hwnd, 0, 32, 36, 180, 150, noActivateOrZOrder|swpNoMove)
	if updates != 1 {
		t.Fatalf("resize notifications = %d, want 1", updates)
	}
	send.Call(hwnd, wmPaint, 0, 0)
	if paints != 1 {
		t.Fatalf("paint during resize = %d, want 1", paints)
	}

	send.Call(hwnd, wmMoving, 0, uintptr(unsafe.Pointer(&bounds)))
	procValidateRect.Call(hwnd, 0)
	send.Call(hwnd, wmExitSizeMove, 0, 0)
	if pending, _, _ := getUpdateRect.Call(hwnd, 0, 0); pending == 0 {
		t.Fatal("release did not schedule a refresh")
	}
	send.Call(hwnd, wmPaint, 0, 0)
	if paints != 2 {
		t.Fatalf("paint after release = %d, want 2", paints)
	}
	move.Call(hwnd, 0, 45, 50, 180, 150, noActivateOrZOrder|swpNoSize)
	if updates != 2 {
		t.Fatalf("notifications after leaving drag = %d, want 2", updates)
	}

	// A native IME composition cannot leak uncommitted keystrokes into the
	// terminal, and Unicode messages reach the terminal as complete characters.
	app := &App{}
	pane := newInputPane()
	app.imeTarget.Store(&imeTarget{pane: pane})
	installTerminalIME(hwnd, app)
	send.Call(hwnd, wmIMEStartComposition, 0, 0)
	send.Call(hwnd, wmIMEComposition, 0, compositionString)
	send.Call(hwnd, wmKeyDown, 0x0d, 0)
	if len(pane.writeQ) != 0 {
		t.Fatal("preedit content reached the terminal")
	}
	send.Call(hwnd, wmIMEEndComposition, 0, 0)
	send.Call(hwnd, wmIMEChar, '中', 0)
	send.Call(hwnd, wmChar, 0xd83d, 0)
	send.Call(hwnd, wmChar, 0xde42, 0)
	if first, second := takeWrite(t, pane), takeWrite(t, pane); first != "中" || second != "🙂" {
		t.Fatalf("native input delivered %q and %q", first, second)
	}
}

func TestStartupWindowFitsWorkAreaOnSecondaryMonitor(t *testing.T) {
	work := image.Rect(-1600, 48, 0, 900)
	target := image.Rect(-1250, 0, 400, 1240)
	if got := fitWindowRect(target, work); got != work {
		t.Fatalf("oversized window = %v, want %v", got, work)
	}
	inside := image.Rect(-1100, 200, -200, 800)
	if got := fitWindowRect(inside, work); got != inside {
		t.Fatalf("normal startup window moved from %v to %v", inside, got)
	}
}

func TestConfiguredWindowGeometryUsesTargetMonitor(t *testing.T) {
	work := image.Rect(-1920, 0, 0, 1040)
	window := config.Default().Window
	window.Width, window.Height = 900, 540
	size, minimum := windowSize(window, work, unit.Metric{PxPerDp: 1.5})
	if size != image.Pt(1350, 810) || minimum != image.Pt(840, 540) {
		t.Fatalf("window pixels = %v, minimum %v", size, minimum)
	}
	if got, want := positionedWindowRect(size, work, window.Position), image.Rect(-1635, 115, -285, 925); got != want {
		t.Fatalf("centered window = %v, want %v", got, want)
	}
	custom := config.WindowPosition{X: -1600, Y: 70}
	if got, want := positionedWindowRect(size, work, custom), image.Rect(-1600, 70, -250, 880); got != want {
		t.Fatalf("custom window = %v, want %v", got, want)
	}
	custom.X, custom.Y = -100, 1000
	if got, want := positionedWindowRect(size, work, custom), image.Rect(-1350, 230, 0, 1040); got != want {
		t.Fatalf("clamped window = %v, want %v", got, want)
	}
}
