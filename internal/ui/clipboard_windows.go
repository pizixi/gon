package ui

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")
	pngClipboardFormat         = sync.OnceValue(func() uintptr {
		name, _ := windows.UTF16PtrFromString("PNG")
		format, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
		return format
	})
)

func clipboardHasImage() bool {
	const (
		cfBitmap = 2
		cfDIB    = 8
		cfDIBV5  = 17
	)
	for _, format := range [...]uintptr{cfBitmap, cfDIB, cfDIBV5, pngClipboardFormat()} {
		if format == 0 {
			continue
		}
		if available, _, _ := isClipboardFormatAvailable.Call(format); available != 0 {
			return true
		}
	}
	return false
}
