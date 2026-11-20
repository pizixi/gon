package term

import (
	"time"
	"unicode"
)

type resizePaint struct {
	until     time.Time
	lookback  int
	nextErase int // next row to erase, plus one. Zero until a synchronized update begins.
}

type historyCut struct {
	generation, added uint64
	length, from, to  int
}

func (s *Screen) noteResizeErase(row, count int) {
	paint := &s.resizePaint
	if paint.nextErase != row+1 || s.syncFrame.historyAdded != s.historyAdded {
		return
	}
	paint.nextErase += count
}

func (s *Screen) finishResizePaint() {
	paint := &s.resizePaint
	if paint.nextErase != s.Rows+1 || s.syncFrame.historyReset != s.historyReset {
		paint.nextErase = 0
		return
	}
	lookback := paint.lookback
	*paint = resizePaint{}
	before := s.syncFrame
	scrolled := int(s.historyAdded - before.historyAdded)
	boundary := s.ScrollbackLen() - scrolled
	if boundary <= 0 {
		return
	}

	// Repainting a frame taller than the viewport can start farther back
	// than a screenful, especially after narrowing wrapped text.
	start, col := s.findRepaintStart(before, boundary, lookback+scrolled)
	if col > 0 {
		// Keep the portion of the old row preceding a mid-line repaint.
		line := &s.scrollback[(s.oldest+start)%len(s.scrollback)]
		line.cells = append([]Cell(nil), line.cells[:col]...)
		line.wrap = col
		start++
	} else if start > 0 {
		// The replaced first row no longer continues the preceding row.
		s.scrollback[(s.oldest+start-1)%len(s.scrollback)].wrap = 0
	}
	if start == boundary && col == 0 {
		return
	}
	s.removeRepaintedHistory(start, boundary)
}

func (s *Screen) findRepaintStart(before *Screen, boundary, lookback int) (row, col int) {
	var replayed, candidate [160]rune
	prefix := repaintText(replayed[:0], boundary, 0, s.AbsLine)
	const minMatch = 32
	if len(prefix) < minMatch {
		return boundary, 0
	}
	oldLine := func(row int) []Cell {
		if row < boundary {
			return s.AbsLine(row)
		}
		return before.AbsLine(row - boundary)
	}
	row = boundary
	best := minMatch - 1
	for y := boundary - 1; y >= max(0, boundary-lookback); y-- {
		hasText := false
		for x, cell := range oldLine(y) {
			if !repaintTextRune(cell.R) {
				continue
			}
			if cell.R == prefix[0] {
				other := repaintText(candidate[:0], y, x, oldLine)
				n := 0
				for n < len(prefix) && n < len(other) && prefix[n] == other[n] {
					n++
				}
				if n > best {
					row, col, best = y, 0, n
					if hasText {
						col = x
					}
					if best == len(prefix) {
						return row, col
					}
				}
			}
			hasText = true
		}
	}
	return row, col
}

func repaintText(text []rune, row, col int, lineAt func(int) []Cell) []rune {
	// Ignore whitespace and decorative glyphs so text can match across styles
	// and across line breaks chosen at different widths.
	for end := row + 12; row < end; row++ {
		cells := lineAt(row)
		for _, cell := range cells[min(col, len(cells)):] {
			if repaintTextRune(cell.R) {
				text = append(text, cell.R)
				if len(text) == cap(text) {
					return text
				}
			}
		}
		col = 0
	}
	return text
}

func repaintTextRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func (s *Screen) removeRepaintedHistory(from, to int) {
	length := len(s.scrollback)
	if s.oldest != 0 {
		s.retainHistory(length)
	}
	copy(s.scrollback[from:], s.scrollback[to:])
	newLength := length - (to - from)
	clear(s.scrollback[newLength:])
	s.scrollback = s.scrollback[:newLength]
	s.historyReset++
	s.historyCut = historyCut{s.historyReset, s.historyAdded, length, from, to}
}

// HistoryPositionAfterRepaint maps an old scrollback position after a full
// repaint removed overlapping lines. Other buffer resets return false.
func (s *Screen) HistoryPositionAfterRepaint(row, length int, added, generation uint64) (int, bool) {
	cut := s.historyCut
	if s.historyReset != generation+1 || cut.generation != s.historyReset {
		return 0, false
	}
	beforeDrop := max(0, length+int(cut.added-added)-cut.length)
	row = max(0, row-beforeDrop)
	row -= clampInt(row-cut.from, 0, cut.to-cut.from)
	afterDrop := max(0, cut.length-(cut.to-cut.from)+int(s.historyAdded-cut.added)-s.ScrollbackLen())
	return max(0, row-afterDrop), true
}
