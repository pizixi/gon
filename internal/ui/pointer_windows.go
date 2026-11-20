package ui

import (
	"runtime"
	"unsafe"
)

const (
	wmPointerUpdate = 0x0245
	ptMouse         = 4
	pointerButtons  = 0x01f0
)

type nativePoint struct{ X, Y int32 }

// nativePointerInfo matches Win32 POINTER_INFO, including its trailing
// alignment on both 32-bit and 64-bit Windows.
type nativePointerInfo struct {
	Type, ID, FrameID, Flags uint32
	Device, Target           uintptr
	Pixel, Himetric          nativePoint
	PixelRaw, HimetricRaw    nativePoint
	Time, HistoryCount       uint32
	InputData                int32
	KeyStates                uint32
	PerformanceCount         uint64
	ButtonChange             uint32
	_                        uint32
}

var (
	getPointerInfo        = user32.NewProc("GetPointerInfo")
	readNativePointerInfo = queryNativePointerInfo
	readNativeMouseCursor = queryNativeMouseCursor
)

func queryNativePointerInfo(id uint32) (nativePointerInfo, bool) {
	var info nativePointerInfo
	ok, _, _ := getPointerInfo.Call(uintptr(id), uintptr(unsafe.Pointer(&info)))
	return info, ok != 0
}

func queryNativeMouseCursor(hwnd uintptr) (nativePoint, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	context, _, _ := getWindowDPIContext.Call(hwnd)
	previous, _, _ := setThreadDPIContext.Call(context)
	if previous == 0 {
		return nativePoint{}, false
	}
	defer setThreadDPIContext.Call(previous)
	var point nativePoint
	ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	return point, ok != 0
}

func pointerMessagePosition(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg != wmPointerUpdate {
		return lParam
	}
	info, ok := readNativePointerInfo(uint32(wParam & 0xffff))
	if !ok || info.Type != ptMouse || info.Target != hwnd || info.Flags&pointerButtons == 0 {
		return lParam
	}
	// RDP can DPI-virtualize both lParam and POINTER_INFO for captured mouse
	// updates, while press/release and hover remain in physical pixels. Such
	// an update falsely leaves a button or starts a tab drag. Read the cursor
	// in the HWND's DPI context for held-button motion only; discrete input
	// keeps its own event position and ordering.
	point, ok := readNativeMouseCursor(hwnd)
	if !ok {
		return lParam
	}
	return uintptr(uint32(uint16(point.X)) | uint32(uint16(point.Y))<<16)
}
