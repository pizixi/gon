package ui

import (
	"image"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmChar                = 0x0102
	wmUniChar             = 0x0109
	wmKeyDown             = 0x0100
	wmSysKeyDown          = 0x0104
	wmIMEStartComposition = 0x010d
	wmIMEEndComposition   = 0x010e
	wmIMEComposition      = 0x010f
	wmIMENotify           = 0x0282
	wmIMEChar             = 0x0286
	compositionResult     = 0x0800
	compositionString     = 0x0008
)

var (
	imm32                   = windows.NewLazySystemDLL("imm32.dll")
	immGetContext           = imm32.NewProc("ImmGetContext")
	immReleaseContext       = imm32.NewProc("ImmReleaseContext")
	immGetCompositionString = imm32.NewProc("ImmGetCompositionStringW")
	immSetCompositionWindow = imm32.NewProc("ImmSetCompositionWindow")
	immSetCandidateWindow   = imm32.NewProc("ImmSetCandidateWindow")
)

type imeTarget struct {
	pane  *Pane
	caret image.Rectangle
}

// terminalIME belongs to the native window thread. A terminal streams input to
// a process, so only committed IME text can be sent to it.
type terminalIME struct {
	app       *App
	composing *Pane
	high      uint16
}

func (i *terminalIME) handle(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	if i.app == nil {
		return 0, false
	}
	switch msg {
	case wmChar, wmUniChar, wmKeyDown, wmSysKeyDown, wmIMEStartComposition, wmIMEComposition, wmIMEEndComposition, wmIMENotify, wmIMEChar:
	default:
		return 0, false
	}
	target := i.app.imeTarget.Load()
	if target == nil {
		i.composing, i.high = nil, 0
		return 0, false
	}
	forward := func(params uintptr) (uintptr, bool) {
		result, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, params)
		return result, true
	}
	send := func(text string) {
		if text != "" {
			target.pane.SendTextInput(text)
			i.app.invalidate()
		}
	}
	switch msg {
	case wmIMEStartComposition:
		i.composing, i.high = target.pane, 0
		placeIMEWindow(hwnd, target.caret)
		return forward(lParam)
	case wmIMEComposition:
		if lParam&compositionResult != 0 {
			if i.composing == nil || i.composing == target.pane {
				send(committedIMEText(hwnd))
			}
			i.composing = nil
			lParam &^= compositionResult
			if lParam == 0 {
				return 0, true
			}
		}
		if lParam&compositionString != 0 {
			i.composing = target.pane
		}
		placeIMEWindow(hwnd, target.caret)
		return forward(lParam)
	case wmIMEEndComposition:
		i.composing = nil
		return forward(lParam)
	case wmIMENotify:
		if wParam == 0x0005 { // IMN_OPENCANDIDATE
			placeIMEWindow(hwnd, target.caret)
		}
		return 0, false
	case wmKeyDown, wmSysKeyDown:
		if i.composing != nil {
			return forward(lParam)
		}
	case wmIMEChar, wmChar:
		ch := uint16(wParam)
		// Backspace, Return and other control keys already have a key event.
		if msg == wmIMEChar || utf16.IsSurrogate(rune(ch)) || i.high != 0 {
			if r, ok := i.utf16Rune(ch); ok && r >= ' ' && r != 0x7f {
				send(string(r))
			}
			return 0, true
		}
	case wmUniChar:
		if wParam == 0xffff { // UNICODE_NOCHAR
			return 1, true
		}
		if r := rune(wParam); r >= ' ' && r != 0x7f && utf8.ValidRune(r) {
			send(string(r))
		}
		return 0, true
	}
	return 0, false
}

func (i *terminalIME) utf16Rune(ch uint16) (rune, bool) {
	if ch >= 0xd800 && ch <= 0xdbff {
		i.high = ch
		return 0, false
	}
	high := i.high
	i.high = 0
	if ch >= 0xdc00 && ch <= 0xdfff {
		if high == 0 {
			return 0, false
		}
		return utf16.DecodeRune(rune(high), rune(ch)), true
	}
	return rune(ch), true
}

func committedIMEText(hwnd uintptr) string {
	ctx, _, _ := immGetContext.Call(hwnd)
	if ctx == 0 {
		return ""
	}
	defer immReleaseContext.Call(hwnd, ctx)
	size, _, _ := immGetCompositionString.Call(ctx, compositionResult, 0, 0)
	if n := int32(size); n <= 0 || n > 1<<20 || n%2 != 0 {
		return ""
	}
	text := make([]uint16, int32(size)/2)
	read, _, _ := immGetCompositionString.Call(ctx, compositionResult, uintptr(unsafe.Pointer(&text[0])), size)
	if n := int32(read); n > 0 && n <= int32(size) && n%2 == 0 {
		return string(utf16.Decode(text[:n/2]))
	}
	return ""
}

func placeIMEWindow(hwnd uintptr, caret image.Rectangle) {
	if caret.Empty() {
		return
	}
	ctx, _, _ := immGetContext.Call(hwnd)
	if ctx == 0 {
		return
	}
	defer immReleaseContext.Call(hwnd, ctx)
	type pointL struct{ X, Y int32 }
	point := pointL{X: int32(caret.Min.X), Y: int32(caret.Min.Y)}
	area := windows.Rect{Left: int32(caret.Min.X), Top: int32(caret.Min.Y), Right: int32(caret.Max.X), Bottom: int32(caret.Max.Y)}
	composition := struct {
		style uint32
		point pointL
		area  windows.Rect
	}{style: 0x0002, point: point} // CFS_POINT
	immSetCompositionWindow.Call(ctx, uintptr(unsafe.Pointer(&composition)))
	candidate := struct {
		index, style uint32
		point        pointL
		area         windows.Rect
	}{style: 0x0080, point: point, area: area} // CFS_EXCLUDE
	immSetCandidateWindow.Call(ctx, uintptr(unsafe.Pointer(&candidate)))
}
