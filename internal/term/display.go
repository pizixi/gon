package term

import "time"

// SetThemeColors sets the defaults used in terminal color query replies.
func (s *Screen) SetThemeColors(fg, bg, cursor uint32) {
	s.themeFg, s.themeBg, s.themeCursor = fg, bg, cursor
}

func (s *Screen) displayColor(n int) (*Color, uint32) {
	switch n {
	case 10:
		return &s.FgColor, s.themeFg
	case 11:
		return &s.BgColor, s.themeBg
	default:
		return &s.CursorColor, s.themeCursor
	}
}

func (s *Screen) colorScheme() int {
	bg := s.BgColor.RGBOr(s.themeBg)
	luminance := 299*int(bg>>16) + 587*int(bg>>8&255) + 114*int(bg&255)
	if luminance >= 128000 {
		return 2
	}
	return 1
}

func (s *Screen) modeStatus(private bool, num int) int {
	var enabled bool
	if !private {
		switch num {
		case 4:
			enabled = s.insertMode
		case 20:
			enabled = s.newlineMode
		default:
			return 0
		}
	} else {
		switch num {
		case 1:
			enabled = s.appCursorKeys
		case 6:
			enabled = s.originMode
		case 7:
			enabled = s.autowrap
		case 12:
			enabled = s.CursorStyle == 0 || s.CursorStyle%2 == 1
		case 25:
			enabled = s.CursorVisible
		case 47, 1047, 1049:
			enabled = s.alt
		case MousePressOnly, MouseClicks, MouseDrag, MouseMotion:
			enabled = s.mouseMode == num
		case 1004:
			enabled = s.focusEvents
		case 1005, 1006, 1015, 1016:
			enabled = s.mouseEncoding == mouseFormat(num)
		case 1007:
			enabled = s.alternateScroll
		case 2004:
			enabled = s.bracketedPaste
		case 2026:
			enabled = time.Now().Before(s.syncUntil)
		case 2031:
			enabled = s.colorSchemeEvents
		default:
			return 0
		}
	}
	if enabled {
		return 1
	}
	return 2
}

const syncOutputTimeout = 200 * time.Millisecond

func (s *Screen) synchronizeOutput(on bool) {
	if !on {
		s.finishResizePaint()
		s.syncUntil = time.Time{}
		return
	}
	now := time.Now()
	if now.Before(s.syncUntil) {
		return
	}
	if s.syncFrame == nil {
		s.syncFrame = &Screen{}
	}
	lines := s.syncFrame.lines
	if s.syncFrame.Cols != s.Cols || s.syncFrame.Rows != s.Rows {
		lines = newGrid(s.Cols, s.Rows)
	}
	for y, line := range s.lines {
		copy(lines[y].cells, line.cells)
		lines[y].wrap = line.wrap
	}
	*s.syncFrame = Screen{
		Cols: s.Cols, Rows: s.Rows, lines: lines,
		CursorX: s.CursorX, CursorY: s.CursorY, CursorVisible: s.CursorVisible, CursorStyle: s.CursorStyle,
		FgColor: s.FgColor, BgColor: s.BgColor, CursorColor: s.CursorColor,
		historyAdded: s.historyAdded, historyReset: s.historyReset, frameHistory: s.ScrollbackLen(),
	}
	s.resizePaint.nextErase = 0
	if now.Before(s.resizePaint.until) {
		s.resizePaint.nextErase = 1
	}
	s.syncUntil = now.Add(syncOutputTimeout)
}

// FramePosition maps a synchronized viewport to indices in the live buffer.
func (s *Screen) FramePosition(frame *Screen) (top int, generation uint64) {
	if frame == s {
		return s.ScrollbackLen(), s.historyReset
	}
	dropped := max(0, frame.frameHistory+int(s.historyAdded-frame.historyAdded)-s.ScrollbackLen())
	return frame.frameHistory - dropped, frame.historyReset
}

// DisplayFrame returns the last complete viewport during synchronized output.
func (s *Screen) DisplayFrame(now time.Time) (*Screen, time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(s.syncUntil) {
		return s.syncFrame, s.syncUntil
	}
	return s, time.Time{}
}
