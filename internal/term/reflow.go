package term

import (
	"image"
	"slices"
)

type screenLine struct {
	cells   []Cell
	wrap    int
	padding int // omitted default blanks at the end of a hard history line
}

type reflowPosition struct {
	old, next image.Point
}

func (s *Screen) reflow(cols, rows int, grid []screenLine, cursor, saved *cursorState, positions []*image.Point) []screenLine {
	history := len(s.scrollback)
	at := func(y int) screenLine {
		if y < history {
			return s.historyLine(y)
		}
		return grid[y-history]
	}
	cursorPoint := func(c *cursorState) image.Point {
		x := c.x
		if c.wrap {
			x++
		}
		return image.Pt(x, history+c.y)
	}
	viewport := reflowPosition{old: image.Pt(0, history)}
	marks := []*reflowPosition{{old: cursorPoint(cursor)}, {old: cursorPoint(saved)}, &viewport}
	firstTracked := len(marks)
	for _, pos := range positions {
		marks = append(marks, &reflowPosition{old: *pos, next: image.Pt(-1, -1)})
	}
	// Empty rows below the cursor do not become history on a height change.
	endRow := history + s.Rows
	keepThrough := marks[0].old.Y
	for _, mark := range marks[1:] {
		keepThrough = max(keepThrough, min(endRow-1, mark.old.Y))
	}
	for endRow > keepThrough+1 {
		line := at(endRow - 1)
		if line.wrap != 0 || contentEnd(line.cells) != 0 {
			break
		}
		endRow--
	}
	lines := make([]screenLine, 0, endRow)
	firstLive := 0
	var pending []Cell
	var lineMarks []*reflowPosition
	flush := func(wrap int) {
		lines = append(lines, screenLine{cells: pending, wrap: wrap})
		pending = nil
	}
	for y := 0; y < endRow; y++ {
		if y == history {
			firstLive = len(lines)
		}
		line := at(y)
		lineMarks = lineMarks[:0]
		end := line.wrap
		if end == 0 {
			end = contentEnd(line.cells)
		}
		for _, mark := range marks {
			if mark.old.Y == y && mark.old.X >= 0 {
				lineMarks = append(lineMarks, mark)
				if line.wrap == 0 {
					end = max(end, mark.old.X)
				}
			}
		}
		if len(pending) == 0 && line.wrap == 0 && end <= cols {
			for _, mark := range lineMarks {
				mark.next = image.Pt(mark.old.X, len(lines))
			}
			// Keep marks within the original logical row even when its
			// trailing default blanks are omitted.
			n := min(end, len(line.cells))
			padding := min(end, len(line.cells)+line.padding) - n
			cells := line.cells[:n:n]
			if n == 0 {
				cells = nil
			}
			lines = append(lines, screenLine{cells: cells, padding: padding})
			continue
		}
		for x := 0; x < end; {
			glyph, sourceWidth := Blank, 1
			if x < len(line.cells) {
				glyph = line.cells[x]
				sourceWidth = max(1, int(glyph.W))
			}
			width := min(sourceWidth, cols)
			if len(pending)+width > cols {
				flush(len(pending))
			}
			for _, mark := range lineMarks {
				if x <= mark.old.X && mark.old.X < x+sourceWidth {
					mark.next = image.Pt(len(pending)+min(width-1, mark.old.X-x), len(lines))
				}
			}
			glyph.W = uint8(width)
			pending = append(pending, glyph)
			if width == 2 {
				pending = append(pending, Cell{Cont: true, A: glyph.A})
			}
			x += sourceWidth
		}
		for _, mark := range lineMarks {
			if mark.old.X >= end {
				mark.next = image.Pt(len(pending), len(lines))
			}
		}
		if line.wrap == 0 {
			flush(0)
		}
	}
	if len(pending) > 0 {
		flush(0)
	}
	// Keep the reflowed viewport boundary when the grid grows. The PTY's
	// cursor addresses live rows, so pulling history into them overwrites it.
	top := min(max(viewport.next.Y, len(lines)-rows), marks[0].next.Y)
	first := max(0, top-max(0, s.MaxScrollback))
	s.scrollback = append([]screenLine(nil), lines[first:top]...)
	// Copy rows spilling from the live grid so a few archived cells
	// cannot retain the original grid's backing array.
	for i := max(first, firstLive); i < top; i++ {
		line := &s.scrollback[i-first]
		line.cells = slices.Clone(line.cells)
	}
	s.oldest = 0
	next := newGrid(cols, rows)
	for y, line := range lines[top:min(len(lines), top+rows)] {
		copy(next[y].cells, line.cells)
		next[y].wrap = line.wrap
	}
	if top+rows < len(lines) {
		next[rows-1].wrap = 0
	}
	for i, c := range []*cursorState{cursor, saved} {
		pos := marks[i].next
		c.x, c.y = clampInt(pos.X, 0, cols-1), clampInt(pos.Y-top, 0, rows-1)
		c.wrap = pos.X == cols
	}
	for i, pos := range positions {
		next := marks[firstTracked+i].next
		if next.Y < first || next.Y >= top+rows {
			*pos = image.Pt(-1, -1)
		} else {
			*pos = image.Pt(clampInt(next.X, 0, cols-1), next.Y-first)
		}
	}
	return next
}

func contentEnd(cells []Cell) int {
	end := len(cells)
	for end > 0 && cells[end-1].IsBlank() && !cells[end-1].Cont {
		end--
	}
	return end
}
