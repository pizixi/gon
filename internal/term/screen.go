package term

import (
	"image"
	"slices"
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

// Screen is a terminal screen buffer with a scrollback history.
//
// Screen is not safe for concurrent use.
type Screen struct {
	Cols, Rows    int
	MaxScrollback int

	lines        []screenLine
	scrollback   []screenLine
	oldest       int
	historyAdded uint64
	historyReset uint64
	historyCut   historyCut
	repainting   bool
	resizePaint  resizePaint

	CursorX, CursorY int
	CursorVisible    bool
	cursorBlink      bool
	CursorStyle      int // 0..6, DECSCUSR
	wrapPending      bool

	scrollTop, scrollBot int
	originMode           bool
	autowrap             bool
	insertMode           bool
	appCursorKeys        bool
	appKeypad            bool
	bracketedPaste       bool
	mouseMode            int // DEC tracking mode, 0 when disabled
	mouseEncoding        MouseEncoding
	focusEvents          bool
	alternateScroll      bool
	colorSchemeEvents    bool
	newlineMode          bool

	// Title is set through OSC 0/1/2.
	Title string
	// Progress is set through OSC 9;4.
	Progress Progress
	// BelCount counts BEL characters; the UI may use it for a visual bell.
	BelCount int
	// Reply, when set, receives terminal responses (DA, DSR, ...).
	Reply func(string)
	// OnClipboard, when set, receives text set by OSC 52.
	OnClipboard func(string)

	lastGlyph Cell
	glyphAt   int

	attr                          Attr
	savedCursor                   cursorState
	g0, g1                        byte
	shift1                        bool
	alt                           bool
	mainLines                     []screenLine
	mainCursor                    cursorState
	mainSavedCursor               cursorState
	mainScrollTop                 int
	mainScrollBot                 int
	tabstops                      []bool
	Dirty                         bool
	FgColor, BgColor, CursorColor Color
	themeFg, themeBg, themeCursor uint32
	syncFrame                     *Screen
	frameHistory                  int
	syncUntil                     time.Time
}

type cursorState struct {
	x, y   int
	attr   Attr
	origin bool
	g0, g1 byte
	shift  bool
	wrap   bool
}

// NewScreen creates a screen and resets it.
func NewScreen(cols, rows, scrollback int) *Screen {
	s := &Screen{Cols: max(1, cols), Rows: max(1, rows), MaxScrollback: scrollback}
	s.SetThemeColors(0xd4d4d4, 0x1e1e1e, 0xd4d4d4)
	s.Reset()
	return s
}

// Blank returns the cell used for erasing with the current background.
func (s *Screen) Blank() Cell {
	return Cell{R: ' ', W: 1, A: Attr{BG: s.attr.BG}}
}

// Reset performs a full terminal reset (RIS).
func (s *Screen) Reset() {
	s.attr = Attr{}
	s.CursorVisible = true
	s.CursorStyle = 1
	s.autowrap = true
	s.insertMode = false
	s.originMode = false
	s.appCursorKeys = false
	s.appKeypad = false
	s.bracketedPaste = false
	s.mouseMode = 0
	s.mouseEncoding = MouseX10
	s.focusEvents = false
	s.alternateScroll = true
	s.colorSchemeEvents = false
	s.newlineMode = false
	s.Progress = Progress{}
	s.repainting = false
	s.resizePaint = resizePaint{}
	s.syncUntil = time.Time{}
	s.alt = false
	s.mainLines = nil
	s.scrollTop = 0
	s.scrollBot = s.Rows - 1
	s.CursorX, s.CursorY = 0, 0
	s.wrapPending = false
	s.g0, s.g1 = 'B', 'B'
	s.shift1 = false
	s.savedCursor = cursorState{}
	s.eraseScrollback()
	s.tabstops = make([]bool, s.Cols)
	for i := range s.tabstops {
		s.tabstops[i] = i%8 == 0
	}
	s.lines = newGrid(s.Cols, s.Rows)
	s.FgColor, s.BgColor, s.CursorColor = DefaultColor, DefaultColor, DefaultColor
	s.lastGlyph = Cell{}
	s.endGrapheme()
	s.Dirty = true
}

// Resize reflows the main buffer and updates any supplied absolute positions.
func (s *Screen) Resize(cols, rows int, positions ...*image.Point) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == s.Cols && rows == s.Rows && s.lines != nil {
		return
	}
	s.historyReset++
	s.resizePaint = resizePaint{}
	s.endGrapheme()
	if s.alt {
		s.mainLines = s.reflow(cols, rows, s.mainLines, &s.mainCursor, &s.mainSavedCursor, nil)
		s.lines = resizeGrid(s.lines, cols, rows)
		for _, pos := range positions {
			if pos.Y >= rows {
				*pos = image.Pt(-1, -1)
			} else if pos.X >= 0 {
				pos.X = min(pos.X, cols-1)
			}
		}
		s.mainScrollTop, s.mainScrollBot = 0, rows-1
		s.CursorX, s.CursorY = min(s.CursorX, cols-1), min(s.CursorY, rows-1)
		s.wrapPending = false
	} else {
		cursor := s.cursorState()
		s.lines = s.reflow(cols, rows, s.lines, &cursor, &s.savedCursor, positions)
		s.CursorX, s.CursorY, s.wrapPending = cursor.x, cursor.y, cursor.wrap
		if len(s.scrollback) != 0 {
			// Lines the application wrapped at the old width may occupy
			// several old rows for each row of the wider replacement.
			lookback := max(s.Rows, rows*((cols+s.Cols-1)/s.Cols))
			s.resizePaint = resizePaint{until: time.Now().Add(time.Second), lookback: lookback}
		}
	}
	s.Cols, s.Rows = cols, rows
	s.scrollTop, s.scrollBot = 0, rows-1
	// Rebuild tab stops.
	ts := make([]bool, cols)
	for i := range ts {
		ts[i] = i%8 == 0
	}
	s.tabstops = ts
	s.Dirty = true
}

func newGrid(cols, rows int) []screenLine {
	cells := make([]Cell, cols*rows)
	for i := range cells {
		cells[i] = Blank
	}
	lines := make([]screenLine, rows)
	for y := range lines {
		end := (y + 1) * cols
		lines[y].cells = cells[y*cols : end : end]
	}
	return lines
}

func resizeGrid(old []screenLine, cols, rows int) []screenLine {
	lines := newGrid(cols, rows)
	for y, src := range old[:min(len(old), rows)] {
		dst := &lines[y]
		copy(dst.cells, src.cells)
		dst.wrap = min(src.wrap, cols)
		// A wide glyph must not be cut in half.
		if cols < len(src.cells) && dst.cells[cols-1].W == 2 {
			dst.cells[cols-1] = Blank
		}
	}
	return lines
}

// CellAt returns the cell at screen coordinates.
func (s *Screen) CellAt(x, y int) Cell {
	if x < 0 || y < 0 || x >= s.Cols || y >= s.Rows {
		return Blank
	}
	return s.lines[y].cells[x]
}

// TotalLines is the number of scrollback lines plus screen rows.
func (s *Screen) TotalLines() int { return s.ScrollbackLen() + s.Rows }

// ScrollbackLen reports how many history lines are stored.
func (s *Screen) ScrollbackLen() int {
	if s.alt {
		return 0
	}
	return len(s.scrollback)
}

// ScrollbackState lets a viewer keep its place when the history ring advances.
func (s *Screen) ScrollbackState() (length int, added, reset uint64) {
	return s.ScrollbackLen(), s.historyAdded, s.historyReset
}

func (s *Screen) eraseScrollback() {
	s.scrollback, s.oldest = nil, 0
	s.historyReset++
}

// SetScrollbackLimit releases older lines immediately when the limit shrinks.
func (s *Screen) SetScrollbackLimit(limit int) {
	s.MaxScrollback = max(0, limit)
	if limit <= 0 {
		s.scrollback, s.oldest = nil, 0
	} else if len(s.scrollback) > limit {
		s.retainHistory(limit)
	}
}

// AbsLine returns the stored cells of an absolute line. Index 0 is the oldest
// scrollback line. Columns between len(AbsLine(abs)) and LineWidth(abs) are
// default blanks. Treat the slice as read-only and use it before the next update.
func (s *Screen) AbsLine(abs int) []Cell {
	return s.line(abs).cells
}

// LineWidth returns the number of columns in an absolute line, including
// any omitted default blanks, or zero for an invalid line.
func (s *Screen) LineWidth(abs int) int {
	line := s.line(abs)
	return len(line.cells) + line.padding
}

// WrapColumn is the end of a row that continues on the next display row.
func (s *Screen) WrapColumn(abs int) int { return s.line(abs).wrap }

func (s *Screen) line(abs int) screenLine {
	if abs < 0 {
		return screenLine{}
	}
	history := s.ScrollbackLen()
	if abs < history {
		return s.historyLine(abs)
	}
	y := abs - history
	if y >= s.Rows {
		return screenLine{}
	}
	return s.lines[y]
}

// ViewTop returns the absolute line index shown at the top of the viewport when
// scrolled back by offset lines.
func (s *Screen) ViewTop(offset int) int {
	history := s.ScrollbackLen()
	return history - max(0, min(offset, history))
}

// Text returns the visible text of an absolute line, right-trimmed.
func (s *Screen) Text(abs int) string {
	return strings.TrimRight(lineText(s.AbsLine(abs)), " \x00")
}

func lineText(line []Cell) string {
	var b strings.Builder
	for _, c := range line {
		if c.W == 0 {
			continue
		}
		if c.R == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(c.R)
		b.WriteString(c.Combining)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Writing

// PutRune writes one printable rune at the cursor.
func (s *Screen) PutRune(r rune) {
	if r == 0 {
		return
	}
	if s.activeCharset() == '0' {
		r = decGraphic(r)
	}
	if s.extendGrapheme(r) {
		return
	}
	w := 1
	if r >= 0x80 {
		w = uniseg.StringWidth(string(r))
	}
	if w > 0 {
		s.putGlyph(Cell{R: r, W: uint8(min(w, s.Cols, 2)), A: s.attr})
	}
}

func (s *Screen) endGrapheme() { s.glyphAt = -1 }

func (s *Screen) putGlyph(glyph Cell) {
	if glyph.W == 2 && s.CursorX == s.Cols-1 && !s.autowrap {
		glyph.W = 1
	}
	w := int(glyph.W)
	s.wrapBefore(w)
	if s.insertMode {
		s.insertCells(w)
	}
	s.writeGlyph(glyph)
}

func (s *Screen) wrapBefore(w int) {
	if s.wrapPending {
		s.wrapPending = false
		if s.autowrap {
			s.CursorX = 0
			s.lineFeed(s.Cols)
		} else {
			s.CursorX = s.Cols - 1
		}
	}
	if w == 2 && s.CursorX > s.Cols-2 {
		if s.autowrap {
			end := s.CursorX
			s.CursorX = 0
			s.lineFeed(end)
		} else {
			s.CursorX = s.Cols - 2
		}
	}
}

func (s *Screen) writeGlyph(glyph Cell) {
	w := int(glyph.W)
	y, x := s.CursorY, s.CursorX
	row := s.lines[y].cells
	s.clearWideAt(row, x)
	if w == 2 {
		s.clearWideAt(row, x+1)
		row[x+1] = Cell{Cont: true, A: glyph.A}
	}
	row[x] = glyph
	s.lastGlyph, s.glyphAt = glyph, y*s.Cols+x
	s.wrapPending = x+w >= s.Cols
	if s.wrapPending {
		s.CursorX = s.Cols - 1
	} else {
		s.CursorX += w
	}
	s.Dirty = true
}

func (s *Screen) extendGrapheme(r rune) bool {
	if r < 0x80 || s.glyphAt < 0 {
		return false
	}
	y, x := s.glyphAt/s.Cols, s.glyphAt%s.Cols
	row := s.lines[y].cells
	glyph := row[x]
	if len(glyph.Combining) >= 128 {
		return false
	}
	// Test the full cluster before advancing: modifiers, flags and ZWJ emoji
	// can extend the previous cell even when the new rune has its own width.
	_, rest, width, _ := uniseg.FirstGraphemeClusterInString(glyph.Text()+string(r), -1)
	if rest != "" {
		return false
	}
	glyph.Combining += string(r)
	width = max(1, min(width, s.Cols, 2))
	if x+width > s.Cols && !s.autowrap {
		width = 1
	}
	if uint8(width) == glyph.W {
		row[x], s.lastGlyph = glyph, glyph
		s.Dirty = true
		return true
	}
	s.CursorX, s.CursorY = x, y
	s.wrapPending = false
	if s.CursorX+width > s.Cols && s.autowrap {
		row[x] = s.Blank()
	}
	s.wrapBefore(width)
	glyph.W = uint8(width)
	s.writeGlyph(glyph)
	return true
}

func (s *Screen) clearWideAt(row []Cell, x int) {
	switch row[x].W {
	case 0: // right half: clear the left half too
		if x > 0 {
			row[x-1] = s.Blank()
		}
	case 2: // left half: clear the right half too
		if x+1 < len(row) {
			row[x+1] = s.Blank()
		}
	}
}

func (s *Screen) activeCharset() byte {
	if s.shift1 {
		return s.g1
	}
	return s.g0
}

// LineFeed moves the cursor down, scrolling if needed.
func (s *Screen) LineFeed() {
	s.lineFeed(0)
}

func (s *Screen) lineFeed(wrap int) {
	s.endGrapheme()
	s.lines[s.CursorY].wrap = wrap
	if s.CursorY == s.scrollBot {
		s.scrollUp(1)
	} else if s.CursorY < s.Rows-1 {
		s.CursorY++
	}
	if s.newlineMode {
		s.CursorX = 0
	}
	s.wrapPending = false
	s.Dirty = true
}

// ReverseIndex moves the cursor up, scrolling down if needed.
func (s *Screen) ReverseIndex() {
	if s.CursorY == s.scrollTop {
		s.scrollDown(1)
	} else if s.CursorY > 0 {
		s.CursorY--
	}
	s.wrapPending = false
	s.Dirty = true
}

// CarriageReturn moves the cursor to column 0.
func (s *Screen) CarriageReturn() {
	s.CursorX = 0
	s.wrapPending = false
}

// Backspace moves the cursor one column left.
func (s *Screen) Backspace() {
	if s.CursorX > 0 {
		s.CursorX--
	}
	s.wrapPending = false
}

// Tab advances to the next tab stop.
func (s *Screen) Tab() {
	x := s.CursorX + 1
	for x < s.Cols && x < len(s.tabstops) && !s.tabstops[x] {
		x++
	}
	if x > s.Cols-1 {
		x = s.Cols - 1
	}
	s.CursorX = x
	s.wrapPending = false
}

// BackTab moves to the previous tab stop.
func (s *Screen) BackTab() {
	x := s.CursorX - 1
	for x > 0 && x < len(s.tabstops) && !s.tabstops[x] {
		x--
	}
	if x < 0 {
		x = 0
	}
	s.CursorX = x
	s.wrapPending = false
}

// SetTabStop sets a tab stop at the cursor column.
func (s *Screen) SetTabStop() {
	if s.CursorX < len(s.tabstops) {
		s.tabstops[s.CursorX] = true
	}
}

// ClearTabStops clears tab stops: mode 0 at the cursor, 3 everywhere.
func (s *Screen) ClearTabStops(mode int) {
	switch mode {
	case 0:
		if s.CursorX < len(s.tabstops) {
			s.tabstops[s.CursorX] = false
		}
	case 3:
		for i := range s.tabstops {
			s.tabstops[i] = false
		}
	}
}

// scrollUp scrolls the scroll region up by n lines.
func (s *Screen) scrollUp(n int) {
	top, bot := s.scrollTop, s.scrollBot
	if n <= 0 || top > bot {
		return
	}
	if n > bot-top+1 {
		n = bot - top + 1
	}
	if top == 0 && !s.alt && !s.repainting && s.MaxScrollback > 0 {
		for _, line := range s.lines[:n] {
			s.appendHistory(line)
		}
	}
	s.scrollRegionUp(top, bot, n)
}

func (s *Screen) appendHistory(line screenLine) {
	s.historyAdded++
	// Hard line tails use the shared default blank implicitly. Keep their
	// column count so searching and hit testing still see the same row.
	cells := line.cells
	if line.wrap == 0 {
		end := len(cells)
		for end > 0 && cells[end-1] == Blank {
			end--
		}
		line.padding = len(cells) - end
		cells = cells[:end]
	}
	size, limit := len(s.scrollback), s.MaxScrollback
	if size > limit || (size < limit && s.oldest != 0) {
		// A changed limit can require linearizing an already wrapped buffer.
		size = min(size, limit)
		s.retainHistory(size)
	}
	if size < limit {
		line.cells = append([]Cell(nil), cells...)
		s.scrollback = append(s.scrollback, line)
		return
	}
	old := s.scrollback[s.oldest].cells
	if len(cells) == 0 || cap(old)/2 > len(cells) {
		old = nil
	} else if len(cells) < len(old) {
		clear(old[len(cells):])
	}
	line.cells = append(old[:0], cells...)
	s.scrollback[s.oldest] = line
	s.oldest = (s.oldest + 1) % size
}

func (s *Screen) retainHistory(count int) {
	history := make([]screenLine, count)
	first := len(s.scrollback) - count
	for i := range history {
		history[i] = s.historyLine(first + i)
	}
	s.scrollback, s.oldest = history, 0
}

// scrollDown scrolls the scroll region down by n lines.
func (s *Screen) scrollDown(n int) {
	top, bot := s.scrollTop, s.scrollBot
	if n <= 0 || top > bot {
		return
	}
	s.scrollRegionDown(top, bot, n)
}

func (s *Screen) clearRow(y int) {
	if y < 0 || y >= s.Rows {
		return
	}
	line := &s.lines[y]
	line.wrap = 0
	b := s.Blank()
	for i := range line.cells {
		line.cells[i] = b
	}
}

func (s *Screen) clearRange(y, x0, x1 int, blank Cell) {
	if y < 0 || y >= s.Rows {
		return
	}
	x0, x1 = max(0, x0), min(s.Cols, x1)
	if x0 >= x1 {
		return
	}
	row := s.lines[y].cells
	if x0 > 0 && row[x0].W == 0 {
		x0--
	}
	if x1 < s.Cols && row[x1].W == 0 {
		x1++
	}
	for x := x0; x < x1; x++ {
		row[x] = blank
	}
	if x1 == s.Cols {
		s.lines[y].wrap = 0
	}
	s.Dirty = true
}

// ---------------------------------------------------------------------------
// Erasing

// EraseInDisplay implements ED.
func (s *Screen) EraseInDisplay(mode int) {
	if mode == 2 || (mode == 0 && s.CursorX == 0 && s.CursorY == 0) {
		s.noteResizeErase(0, s.Rows)
	}
	switch mode {
	case 0:
		s.clearRange(s.CursorY, s.CursorX, s.Cols, s.Blank())
		for y := s.CursorY + 1; y < s.Rows; y++ {
			s.clearRow(y)
		}
	case 1:
		for y := 0; y < s.CursorY; y++ {
			s.clearRow(y)
		}
		s.clearRange(s.CursorY, 0, s.CursorX+1, s.Blank())
	case 2:
		for y := 0; y < s.Rows; y++ {
			s.clearRow(y)
		}
	case 3:
		if !s.alt {
			s.eraseScrollback()
		}
	}
	s.wrapPending = false
	s.Dirty = true
}

// EraseInLine implements EL.
func (s *Screen) EraseInLine(mode int) {
	if mode == 2 || (mode == 0 && s.CursorX == 0) {
		s.noteResizeErase(s.CursorY, 1)
	}
	switch mode {
	case 0:
		s.clearRange(s.CursorY, s.CursorX, s.Cols, s.Blank())
	case 1:
		s.clearRange(s.CursorY, 0, s.CursorX+1, s.Blank())
	case 2:
		s.clearRange(s.CursorY, 0, s.Cols, s.Blank())
	}
	s.wrapPending = false
	s.Dirty = true
}

type coloredLineTail struct {
	text  string
	col   int
	color Color
}

// Remember colored blanks already present before a legacy resize repaint.
// Lines can move vertically, so match the written prefix and its column.
func (s *Screen) coloredLineTails() map[coloredLineTail]int {
	var tails map[coloredLineTail]int
	for y := 0; y < s.Rows; y++ {
		row := s.lines[y].cells
		end := len(row)
		for end > 0 && row[end-1].IsBlank() {
			end--
		}
		if end == 0 || row[end-1].A.BG == DefaultColor {
			continue
		}
		bg := row[end-1].A.BG
		start := end
		for start > 0 && row[start-1].R == ' ' && row[start-1].A.BG == bg {
			start--
		}
		if start == end {
			continue
		}
		if tails == nil {
			tails = make(map[coloredLineTail]int)
		}
		key := coloredLineTail{text: lineText(row[:start]), col: start, color: bg}
		if prior := tails[key]; prior == 0 || end < prior {
			tails[key] = end
		}
	}
	return tails
}

// Legacy ConPTY sometimes erases to the end of a repainted row using the
// last glyph's background. Preserve the known span of colored blanks.
func (s *Screen) eraseResizeTail(tails map[coloredLineTail]int) {
	x, y := s.CursorX, s.CursorY
	end := x
	if len(tails) > 0 {
		prefix := s.lines[y].cells[:x]
		key := coloredLineTail{text: lineText(prefix), col: x, color: s.attr.BG}
		end = max(x, min(s.Cols, tails[key]))
	}
	s.clearRange(y, x, end, s.Blank())
	s.clearRange(y, end, s.Cols, Blank)
	s.wrapPending = false
}

// EraseChars implements ECH.
func (s *Screen) EraseChars(n int) {
	if n < 1 {
		n = 1
	}
	s.clearRange(s.CursorY, s.CursorX, s.CursorX+n, s.Blank())
}

// InsertChars implements ICH.
func (s *Screen) InsertChars(n int) {
	s.insertCells(n)
	s.Dirty = true
}

func (s *Screen) insertCells(n int) {
	if n < 1 || s.CursorX >= s.Cols {
		return
	}
	row := s.lines[s.CursorY].cells
	if n > s.Cols-s.CursorX {
		n = s.Cols - s.CursorX
	}
	copy(row[s.CursorX+n:], row[s.CursorX:s.Cols-n])
	b := s.Blank()
	for i := s.CursorX; i < s.CursorX+n; i++ {
		row[i] = b
	}
	s.repairWide(row)
}

// DeleteChars implements DCH.
func (s *Screen) DeleteChars(n int) {
	if n < 1 || s.CursorX >= s.Cols {
		return
	}
	row := s.lines[s.CursorY].cells
	if n > s.Cols-s.CursorX {
		n = s.Cols - s.CursorX
	}
	copy(row[s.CursorX:], row[s.CursorX+n:])
	b := s.Blank()
	for i := s.Cols - n; i < s.Cols; i++ {
		row[i] = b
	}
	s.repairWide(row)
	s.Dirty = true
}

func (s *Screen) repairWide(row []Cell) {
	for x, c := range row {
		if (c.Cont && (x == 0 || row[x-1].W != 2)) ||
			(c.W == 2 && (x+1 == len(row) || !row[x+1].Cont)) {
			row[x] = Cell{R: ' ', W: 1, A: Attr{BG: c.A.BG}}
		}
	}
}

// InsertLines implements IL.
func (s *Screen) InsertLines(n int) {
	if s.CursorY < s.scrollTop || s.CursorY > s.scrollBot || n < 1 {
		return
	}
	s.scrollRegionDown(s.CursorY, s.scrollBot, n)
}

// DeleteLines implements DL.
func (s *Screen) DeleteLines(n int) {
	if s.CursorY < s.scrollTop || s.CursorY > s.scrollBot || n < 1 {
		return
	}
	s.scrollRegionUp(s.CursorY, s.scrollBot, n)
}

func (s *Screen) scrollRegionUp(top, bot, n int) {
	n = min(n, bot-top+1)
	rotateRows(s.lines[top:bot+1], n)
	for y := bot - n + 1; y <= bot; y++ {
		s.clearRow(y)
	}
	s.Dirty = true
}

func (s *Screen) scrollRegionDown(top, bot, n int) {
	n = min(n, bot-top+1)
	rotateRows(s.lines[top:bot+1], bot-top+1-n)
	for y := top; y < top+n; y++ {
		s.clearRow(y)
	}
	s.Dirty = true
}

// rotateRows moves rows and their wrap positions together, reusing their cells.
func rotateRows(lines []screenLine, n int) {
	if n == 0 || n == len(lines) {
		return
	}
	if n == 1 {
		first := lines[0]
		copy(lines, lines[1:])
		lines[len(lines)-1] = first
		return
	}
	slices.Reverse(lines[:n])
	slices.Reverse(lines[n:])
	slices.Reverse(lines)
}

// ---------------------------------------------------------------------------
// Cursor

// MoveTo implements CUP/HVP (0 based input, origin aware).
func (s *Screen) MoveTo(x, y int) {
	if s.originMode {
		y += s.scrollTop
		if y > s.scrollBot {
			y = s.scrollBot
		}
	}
	s.CursorX = clampInt(x, 0, s.Cols-1)
	s.CursorY = clampInt(y, 0, s.Rows-1)
	s.wrapPending = false
}

// MoveRel moves the cursor by a delta, clamped to the scroll region when the
// cursor is inside it (like xterm).
func (s *Screen) MoveRel(dx, dy int) {
	y := s.CursorY
	if y >= s.scrollTop && y <= s.scrollBot {
		y = clampInt(y+dy, s.scrollTop, s.scrollBot)
	} else {
		y = clampInt(y+dy, 0, s.Rows-1)
	}
	s.CursorY = y
	s.CursorX = clampInt(s.CursorX+dx, 0, s.Cols-1)
	s.wrapPending = false
}

// SetColumn implements CHA/HPA.
func (s *Screen) SetColumn(x int) {
	s.CursorX = clampInt(x, 0, s.Cols-1)
	s.wrapPending = false
}

// SetRow implements VPA.
func (s *Screen) SetRow(y int) {
	if s.originMode {
		y = clampInt(y+s.scrollTop, s.scrollTop, s.scrollBot)
	}
	s.CursorY = clampInt(y, 0, s.Rows-1)
	s.wrapPending = false
}

// SaveCursor implements DECSC.
func (s *Screen) SaveCursor() {
	s.savedCursor = s.cursorState()
}

func (s *Screen) cursorState() cursorState {
	return cursorState{
		x: s.CursorX, y: s.CursorY, attr: s.attr,
		origin: s.originMode, g0: s.g0, g1: s.g1, shift: s.shift1, wrap: s.wrapPending,
	}
}

// RestoreCursor implements DECRC.
func (s *Screen) RestoreCursor() {
	s.restoreCursor(s.savedCursor)
}

func (s *Screen) restoreCursor(c cursorState) {
	s.CursorX = clampInt(c.x, 0, s.Cols-1)
	s.CursorY = clampInt(c.y, 0, s.Rows-1)
	s.attr = c.attr
	s.originMode = c.origin
	s.g0, s.g1 = c.g0, c.g1
	if s.g0 == 0 {
		s.g0, s.g1 = 'B', 'B'
	}
	s.shift1 = c.shift
	s.wrapPending = c.wrap && s.CursorX == s.Cols-1
}

// SetScrollRegion implements DECSTBM (0 based, inclusive).
func (s *Screen) SetScrollRegion(top, bot int) {
	top = clampInt(top, 0, s.Rows-1)
	bot = clampInt(bot, 0, s.Rows-1)
	if top >= bot {
		top, bot = 0, s.Rows-1
	}
	s.scrollTop, s.scrollBot = top, bot
	s.MoveTo(0, 0)
}

// SetAltScreen switches between the main screen and an empty alternate buffer.
func (s *Screen) SetAltScreen(on, saveCursor bool) {
	if on == s.alt {
		return
	}
	s.historyReset++
	s.resizePaint = resizePaint{}
	if on {
		s.mainLines = s.lines
		s.mainCursor, s.mainSavedCursor = s.cursorState(), s.savedCursor
		s.mainScrollTop, s.mainScrollBot = s.scrollTop, s.scrollBot
		s.savedCursor = cursorState{}
		s.scrollTop, s.scrollBot = 0, s.Rows-1
		s.lines = newGrid(s.Cols, s.Rows)
		s.alt = true
		s.CursorX, s.CursorY = 0, 0
		s.wrapPending = false
	} else {
		if s.mainLines != nil {
			s.lines = s.mainLines
		} else {
			s.lines = newGrid(s.Cols, s.Rows)
		}
		s.mainLines = nil
		s.alt = false
		s.savedCursor = s.mainSavedCursor
		s.scrollTop, s.scrollBot = s.mainScrollTop, s.mainScrollBot
		if saveCursor {
			s.restoreCursor(s.mainCursor)
		} else {
			s.CursorX = clampInt(s.mainCursor.x, 0, s.Cols-1)
			s.CursorY = clampInt(s.mainCursor.y, 0, s.Rows-1)
			s.wrapPending = false
		}
	}
	s.Dirty = true
}

// Bell rings the terminal bell.
func (s *Screen) Bell() {
	s.BelCount++
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Cell returns the cell at (x,y) in the active screen, or a blank cell if out of range.
func (s *Screen) Cell(x, y int) Cell {
	if y < 0 || y >= s.Rows || x < 0 || x >= s.Cols {
		return s.Blank()
	}
	return s.lines[y].cells[x]
}

// Cursor reports the cursor position.
func (s *Screen) Cursor() (x, y int) { return s.CursorX, s.CursorY }

// CursorHidden reports whether the cursor should be drawn as hidden.
func (s *Screen) CursorHidden() bool { return !s.CursorVisible }

// CursorBlink reports whether the cursor blinks.
func (s *Screen) CursorBlink() bool { return s.cursorBlink }

// CursorVisibleState reports the visibility for blink timing.
func (s *Screen) CursorVisibleState() bool { return s.CursorVisible }

// DefaultAttr returns the current screen default attribute.
func (s *Screen) DefaultAttr() Attr { return s.attr }

// ScrollbackLine returns the stored cells of a history line (0 = oldest).
func (s *Screen) ScrollbackLine(i int) []Cell {
	return s.historyLine(i).cells
}

func (s *Screen) historyLine(i int) screenLine {
	if i < 0 || i >= len(s.scrollback) {
		return screenLine{}
	}
	return s.scrollback[(s.oldest+i)%len(s.scrollback)]
}

// BracketedPaste reports whether bracketed paste mode (2004) is enabled.
func (s *Screen) BracketedPaste() bool { return s.bracketedPaste }

// AppCursorKeys reports whether DECCKM (application cursor keys) is enabled.
func (s *Screen) AppCursorKeys() bool { return s.appCursorKeys }

// AppKeypad reports whether application keypad mode is enabled.
func (s *Screen) AppKeypad() bool { return s.appKeypad }

// AltScreen reports whether the alternate screen buffer is active.
func (s *Screen) AltScreen() bool { return s.alt }

// MouseMode reports the mouse reporting mode: 0 off, 1 click, 2 drag, 3 any.
func (s *Screen) MouseMode() int { return s.mouseMode }

// MouseEncoding reports the format selected by the application.
func (s *Screen) MouseEncoding() MouseEncoding { return s.mouseEncoding }

func (s *Screen) FocusEvents() bool { return s.focusEvents }

func (s *Screen) AlternateScroll() bool { return s.alt && s.alternateScroll }
