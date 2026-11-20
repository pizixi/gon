package ui

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowMoveSubclassID = 1

const (
	wmPaint            = 0x000f
	wmWindowPosChanged = 0x0047
	wmNCDestroy        = 0x0082
	wmNCCalcSize       = 0x0083
	wmSizing           = 0x0214
	wmMoving           = 0x0216
	wmEnterSizeMove    = 0x0231
	wmExitSizeMove     = 0x0232
	wmDPIChanged       = 0x02e0
	swpNoSize          = 0x0001
	swpFrameChanged    = 0x0020
	swpShowWindow      = 0x0040
	swpHideWindow      = 0x0080
	swpStateChanged    = 0x8000
)

var (
	comctl32                 = windows.NewLazySystemDLL("comctl32.dll")
	procSetWindowSubclass    = comctl32.NewProc("SetWindowSubclass")
	procRemoveWindowSubclass = comctl32.NewProc("RemoveWindowSubclass")
	procDefSubclassProc      = comctl32.NewProc("DefSubclassProc")
	procDefWindowProc        = user32.NewProc("DefWindowProcW")
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procInvalidateRect       = user32.NewProc("InvalidateRect")
	procValidateRect         = user32.NewProc("ValidateRect")
	procCopyWindowPosition   = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlMoveMemory")
	windowMoveCallback       uintptr
	windowMoveStates         sync.Map
)

func init() {
	windowMoveCallback = syscall.NewCallback(windowMoveProc)
}

type windowMoveState struct {
	ime         terminalIME
	inSizeMove  bool
	moving      bool
	dpiChanging bool
	width       int32
	height      int32
}

type windowPosition struct {
	hwnd, insertAfter   uintptr
	x, y, width, height int32
	flags               uint32
}

// installWindowMoveFilter runs on the HWND's message thread.
func installWindowMoveFilter(hwnd uintptr) {
	if procSetWindowSubclass.Find() != nil {
		return
	}
	ok, _, _ := procSetWindowSubclass.Call(hwnd, windowMoveCallback, windowMoveSubclassID, 0)
	if ok != 0 {
		windowMoveStates.Store(hwnd, new(windowMoveState))
	}
}

func installTerminalIME(hwnd uintptr, a *App) {
	if value, ok := windowMoveStates.Load(hwnd); ok {
		value.(*windowMoveState).ime.app = a
	}
}

func windowMoveProc(hwnd uintptr, msg uint32, wParam, lParam, id, _ uintptr) uintptr {
	value, ok := windowMoveStates.Load(hwnd)
	if ok {
		move := value.(*windowMoveState)
		lParam = pointerMessagePosition(hwnd, msg, wParam, lParam)
		if result, handled := move.ime.handle(hwnd, msg, wParam, lParam); handled {
			return result
		}
		switch msg {
		case wmNCCalcSize:
			// Gio's borderless fullscreen window fills the monitor. Keep its
			// client area the same size so it includes the taskbar region.
			const wsMaximized, wsOverlappedWindow = 0x01000000, 0x00cf0000
			styleIndex := int32(-16) // GWL_STYLE
			style, _, _ := getWindowLong.Call(hwnd, uintptr(styleIndex))
			if style&wsMaximized != 0 && style&wsOverlappedWindow == 0 {
				return 0
			}
		case wmEnterSizeMove:
			var bounds [4]int32
			procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds[0])))
			move.width, move.height = bounds[2]-bounds[0], bounds[3]-bounds[1]
			move.inSizeMove = true
		case wmExitSizeMove:
			move.inSizeMove = false
			if move.moving {
				move.moving = false
				procInvalidateRect.Call(hwnd, 0, 0)
			}
		case wmMoving:
			move.moving = move.inSizeMove
		case wmSizing:
			move.moving = false
		case wmPaint:
			if move.moving && !move.dpiChanging {
				// Let DWM keep moving the last submitted surface. Acknowledge
				// the paint so it does not keep waking the native move loop.
				procValidateRect.Call(hwnd, 0)
				return 0
			}
		case wmDPIChanged:
			wasChanging := move.dpiChanging
			move.dpiChanging = true
			defer func() { move.dpiChanging = wasChanging }()
		case wmWindowPosChanged:
			if move.inSizeMove && !move.dpiChanging && lParam != 0 {
				var pos windowPosition
				procCopyWindowPosition.Call(uintptr(unsafe.Pointer(&pos)), lParam, unsafe.Sizeof(pos))
				sameSize := pos.width == move.width && pos.height == move.height
				if keepsWindowLayout(pos.flags, sameSize) {
					// Gio synchronously presents a frame for every WINDOWPOSCHANGED.
					// DWM can move the existing frame when the client size is unchanged.
					result, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
					return result
				}
				if pos.flags&swpNoSize == 0 {
					move.width, move.height = pos.width, pos.height
				}
			}
		case wmNCDestroy:
			windowMoveStates.Delete(hwnd)
			procRemoveWindowSubclass.Call(hwnd, windowMoveCallback, id)
		}
	}
	result, _, _ := procDefSubclassProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return result
}

func keepsWindowLayout(flags uint32, sameSize bool) bool {
	const layoutFlags = swpFrameChanged | swpShowWindow | swpHideWindow | swpStateChanged
	return flags&layoutFlags == 0 && (sameSize || flags&swpNoSize != 0)
}
