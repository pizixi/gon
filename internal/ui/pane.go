package ui

import (
	"image"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"gioui.org/layout"
	"gioui.org/widget"

	"gon/internal/pty"
	"gon/internal/render"
	"gon/internal/term"
)

// Pane is one terminal session inside a tab.
type Pane struct {
	mu sync.Mutex

	scr    *term.Screen
	parser *term.Parser
	sess   *pty.Session

	cols, rows    int
	visible       atomic.Bool
	outputChanged atomic.Bool

	// scrollOffset is the number of lines scrolled back from the bottom.
	scrollOffset int

	// Selection in absolute line coordinates.
	selActive     bool
	selPending    bool
	selAnchorLine int
	selAnchorCol  int
	selCaretLine  int
	selCaretCol   int

	// rect is the pane geometry in window pixels.
	rect            image.Rectangle
	closeButton     widget.Clickable
	presented       panePresentation
	mouseButton     int // last reported mouse button plus one, zero when released
	mousePosition   image.Point
	lastMouseMotion string

	shellName string
	exited    bool
	exitCode  int

	onClipboard func(string)

	// writeQ decouples UI input from blocking pipe writes.
	writeQ    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	writeOnce sync.Once
}

type panePresentation struct {
	grid         render.Grid
	caret        image.Rectangle
	top, history int
	added, reset uint64
}

// NewPane starts a shell and attaches a screen of the given grid size.
func NewPane(shellName, command string, args []string, dir string, cols, rows, scrollback int) (*Pane, error) {
	return newPaneWithOptions(shellName, command, args, pty.Options{Dir: dir}, cols, rows, scrollback)
}

func newPaneWithOptions(shellName, command string, args []string, options pty.Options, cols, rows, scrollback int) (*Pane, error) {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	scr := term.NewScreen(cols, rows, scrollback)
	p := &Pane{scr: scr, cols: cols, rows: rows, shellName: shellName}
	p.parser = term.NewParser(scr)

	sess, err := pty.StartWithOptions(command, args, options, cols, rows)
	if err != nil {
		return nil, err
	}
	p.sess = sess
	p.parser.SetModernConsole(sess.ModernConsole())
	p.writeQ = make(chan []byte, 256)
	p.done = make(chan struct{})
	go p.writeLoop()
	// Replies are queued like user input: writing them inline would block the
	// parser (and the pane mutex) when the shell is not reading stdin.
	scr.Reply = func(s string) {
		p.Write([]byte(s))
	}
	scr.OnClipboard = func(s string) {
		// The parser invokes the callback while holding the pane lock.
		if fn := p.onClipboard; fn != nil {
			fn(s)
		}
	}
	return p, nil
}

// SetClipboardHandler installs the OSC 52 sink.
func (p *Pane) SetClipboardHandler(fn func(string)) {
	p.mu.Lock()
	p.onClipboard = fn
	p.mu.Unlock()
}

// markExited records the shell exit status once.
func (p *Pane) markExited(notify func(chromeChanged bool)) {
	p.mu.Lock()
	already := p.exited
	p.exited = true
	if ok, code := p.sess.Exited(); ok {
		p.exitCode = code
	}
	p.mu.Unlock()
	if !already && notify != nil {
		notify(true)
	}
}

// ReadLoop pumps shell output into the parser until the session ends.
func (p *Pane) ReadLoop(notify func(chromeChanged bool)) {
	// The pty may stay readable after the shell exits, so watch the session
	// directly to surface the exit status.
	if p.sess != nil {
		go func() {
			<-p.sess.Done()
			p.markExited(notify)
		}()
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := p.sess.Read(buf)
		if n > 0 {
			chromeChanged := p.applyOutput(buf[:n])
			p.outputChanged.Store(true)
			if notify != nil {
				notify(chromeChanged)
			}
		}
		if err != nil {
			break
		}
	}
	p.markExited(notify)
}

func (p *Pane) applyOutput(data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	length, added, reset := p.scr.ScrollbackState()
	title, progress := p.scr.Title, p.scr.Progress
	p.parser.Write(data)
	chromeChanged := title != p.scr.Title || progress != p.scr.Progress
	after, total, generation := p.scr.ScrollbackState()
	if generation != reset {
		top, repainted := p.scr.HistoryPositionAfterRepaint(length-p.scrollOffset, length, added, reset)
		if repainted && p.scrollOffset > 0 {
			p.scrollOffset = after - min(top, after)
		} else {
			p.scrollOffset = 0
		}
		p.selActive, p.selPending = false, false
		return chromeChanged
	}
	newRows := int(total - added)
	if p.scrollOffset != 0 {
		p.scrollOffset = min(after, p.scrollOffset+newRows)
	}
	p.historyDropped(length + newRows - after)
	return chromeChanged
}

func (p *Pane) historyDropped(lines int) {
	if lines <= 0 || (!p.selActive && !p.selPending) {
		return
	}
	p.selAnchorLine -= lines
	p.selCaretLine -= lines
	if p.selAnchorLine < 0 || p.selCaretLine < 0 {
		p.selActive, p.selPending = false, false
	}
}

// Resize applies a new grid size to the screen and the PTY.
// A supplied cell size is also used for terminal pixel size queries.
func (p *Pane) Resize(cols, rows int, cellSize ...image.Point) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	p.mu.Lock()
	if len(cellSize) != 0 && p.parser != nil {
		p.parser.SetCellSize(cellSize[0])
	}
	if cols == p.cols && rows == p.rows {
		p.mu.Unlock()
		return
	}
	top := image.Pt(0, p.scr.ViewTop(p.scrollOffset))
	anchor := image.Pt(p.selAnchorCol, p.selAnchorLine)
	caret := image.Pt(p.selCaretCol, p.selCaretLine)
	var positions []*image.Point
	if p.scrollOffset != 0 {
		positions = append(positions, &top)
	}
	if p.selActive || p.selPending {
		positions = append(positions, &anchor, &caret)
	}
	if p.parser != nil {
		p.parser.Resize(cols, rows, positions...)
	} else {
		p.scr.Resize(cols, rows, positions...)
	}
	p.cols, p.rows = cols, rows
	if p.scrollOffset != 0 {
		history := p.scr.ScrollbackLen()
		p.scrollOffset = history - max(0, min(top.Y, history))
	}
	p.selAnchorCol, p.selAnchorLine = anchor.X, anchor.Y
	p.selCaretCol, p.selCaretLine = caret.X, caret.Y
	if anchor.Y < 0 || caret.Y < 0 {
		p.selActive, p.selPending = false, false
	}
	p.mu.Unlock()
	if p.sess != nil {
		_ = p.sess.Resize(cols, rows)
	}
}

// writeLoop drains the input queue into the pty.
func (p *Pane) writeLoop() {
	for {
		select {
		case <-p.done:
			return
		case b := <-p.writeQ:
			if p.sess == nil {
				continue
			}
			for len(b) != 0 {
				n, err := p.sess.Write(b)
				if err != nil || n == 0 {
					return
				}
				b = b[n:]
			}
		}
	}
}

// Write queues bytes for the shell. It never blocks the caller.
func (p *Pane) Write(b []byte) {
	if len(b) == 0 || p.writeQ == nil {
		return
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case p.writeQ <- cp:
	default:
		// Queue full: drop rather than stall the UI.
	}
}

// SendText writes UTF-8 text as-is.
func (p *Pane) SendText(s string) {
	if s != "" {
		p.Write([]byte(s))
	}
}

// Paste inserts text honouring bracketed paste mode.
func (p *Pane) Paste(text string) {
	p.mu.Lock()
	bracketed := p.scr.BracketedPaste()
	if text != "" || bracketed {
		p.scrollOffset = 0
	}
	p.mu.Unlock()
	p.SendText(term.Paste(text, bracketed))
}

func (p *Pane) pasteClipboard(text string, hasImage bool) {
	if text == "" && hasImage {
		// Claude Code and Codex recognize Alt+V as image paste on Windows.
		p.SendKey("v", term.ModAlt)
		return
	}
	// An empty bracketed paste matches the Windows Terminal paste event.
	p.Paste(text)
}

// KeyState snapshots the modes needed for key encoding.
func (p *Pane) KeyState() term.KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return term.KeyState{
		AppCursor:      p.scr.AppCursorKeys(),
		AppKeypad:      p.scr.AppKeypad(),
		AltScreen:      p.scr.AltScreen(),
		BracketedPaste: p.scr.BracketedPaste(),
	}
}

// SendKey encodes and forwards a key event.
func (p *Pane) SendKey(name string, mods term.Mods) bool {
	seq, ok := term.EncodeKey(name, mods, p.KeyState())
	if !ok {
		return false
	}
	p.mu.Lock()
	p.scrollOffset = 0
	p.mu.Unlock()
	p.SendText(seq)
	return true
}

// SendTextInput forwards typed text and returns to the live view.
func (p *Pane) SendTextInput(s string) {
	p.mu.Lock()
	p.scrollOffset = 0
	p.mu.Unlock()
	p.SendText(s)
}

// MouseMode reports the application's mouse tracking and encoding.
func (p *Pane) MouseMode() (mode int, encoding term.MouseEncoding) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.MouseMode(), p.scr.MouseEncoding()
}

// SendMouse forwards a mouse event using cell coordinates.
func (p *Pane) SendMouse(button int, pos image.Point, press bool, mods term.Mods, encoding term.MouseEncoding) {
	seq := term.EncodeMouse(button, pos.X, pos.Y, press, mods, encoding)
	if button >= 32 && button < 64 {
		if p.lastMouseMotion == seq {
			return
		}
		p.lastMouseMotion = seq
	} else {
		p.lastMouseMotion = ""
	}
	p.SendText(seq)
}

func (p *Pane) sendFocus(focused bool) {
	p.mu.Lock()
	enabled := p.scr.FocusEvents()
	p.mu.Unlock()
	if enabled {
		if focused {
			p.SendText("\x1b[I")
		} else {
			p.SendText("\x1b[O")
		}
	}
}

func (p *Pane) scrollWheel(lines int) {
	p.mu.Lock()
	alternate, appCursor := p.scr.AlternateScroll(), p.scr.AppCursorKeys()
	p.mu.Unlock()
	if !alternate {
		p.ScrollBy(lines)
		return
	}
	key := "↑"
	if lines < 0 {
		key, lines = "↓", -lines
	}
	seq, _ := term.EncodeKey(key, 0, term.KeyState{AppCursor: appCursor})
	p.SendText(strings.Repeat(seq, min(lines, 64)))
}

// ScrollBy adjusts the scrollback view offset.
func (p *Pane) ScrollBy(lines int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scrollOffset += lines
	max := p.scr.ScrollbackLen()
	if p.scrollOffset > max {
		p.scrollOffset = max
	}
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
}

// SetScrollOffset clamps and stores the scrollback offset.
func (p *Pane) SetScrollOffset(off int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if off > p.scr.ScrollbackLen() {
		off = p.scr.ScrollbackLen()
	}
	if off < 0 {
		off = 0
	}
	p.scrollOffset = off
}

// ScrollbackLen reports how many history lines are available.
func (p *Pane) ScrollbackLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.ScrollbackLen()
}

func (p *Pane) SetScrollbackLimit(limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.scr.ScrollbackLen()
	p.scr.SetScrollbackLimit(limit)
	remaining := p.scr.ScrollbackLen()
	p.scrollOffset = min(p.scrollOffset, remaining)
	p.historyDropped(previous - remaining)
}

// ScrollOffset reports the current scrollback offset.
func (p *Pane) ScrollOffset() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scrollOffset
}

// Screen runs fn with the screen under the pane lock.
func (p *Pane) Screen(fn func(*term.Screen)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p.scr)
}

func (p *Pane) drawTerminal(gtx layout.Context, view *render.View, grid render.Grid, color uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	history, added, reset := p.scr.ScrollbackState()
	top := p.scr.ViewTop(p.scrollOffset)
	if p.scrollOffset == 0 {
		frame, _ := p.scr.DisplayFrame(gtx.Now)
		top, reset = p.scr.FramePosition(frame)
	}
	x, y := p.scr.Cursor()
	m := grid.Metrics
	caret := image.Rect(x*m.CellW, y*m.CellH, (x+1)*m.CellW, (y+1)*m.CellH).Add(grid.Bounds.Min).Intersect(grid.Bounds)
	p.presented = panePresentation{grid: grid, caret: caret, top: top, history: history, added: added, reset: reset}
	l0, c0, l1, c1, active := p.selectionRangeLocked()
	sel := render.Selection{L0: l0, C0: c0, L1: l1, C1: c1, Active: active}
	view.DrawRect(gtx, p.scr, grid.Bounds.Dx(), grid.Bounds.Dy(), p.scrollOffset, sel, color)
}

// Title returns the shell-provided title or the shell name.
func (p *Pane) Title() string {
	title, _, _ := p.titleStatus()
	if title == "" {
		return p.shellName
	}
	return title
}

func (p *Pane) titleStatus() (string, term.Progress, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scr == nil {
		return "", term.Progress{}, p.exited
	}
	return p.scr.Title, p.scr.Progress, p.exited
}

// ShellName returns the configured shell name.
func (p *Pane) ShellName() string { return p.shellName }

// Exited reports whether the shell has exited.
func (p *Pane) Exited() (bool, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.exitCode
}

// Close terminates the session.
func (p *Pane) Close() {
	p.closeOnce.Do(func() {
		p.visible.Store(false)
		if p.done != nil {
			close(p.done)
		}
		if p.sess != nil {
			p.sess.Close()
		}
	})
}

// Size returns the current grid size.
func (p *Pane) Size() (cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows
}

// SetRect records the pane geometry.
func (p *Pane) SetRect(r image.Rectangle) { p.rect = r }

// Rect returns the pane geometry.
func (p *Pane) Rect() image.Rectangle { return p.rect }

// ---------------------------------------------------------------------------
// Selection

// SelectionStart records the anchor for a pointer selection.
func (p *Pane) SelectionStart(cx, row int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	abs := p.presentedRow(row)
	if abs < 0 {
		return
	}
	p.selActive, p.selPending = false, true
	p.selAnchorLine, p.selAnchorCol = abs, cx
	p.selCaretLine, p.selCaretCol = abs, cx
}

// SelectionExtend moves the selection caret to a viewport cell.
func (p *Pane) SelectionExtend(cx, row int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.selActive && !p.selPending {
		return
	}
	abs := p.presentedRow(row)
	if abs < 0 {
		return
	}
	p.selCaretLine = abs
	p.selCaretCol = cx
	if p.selPending && (p.selCaretLine != p.selAnchorLine || cx != p.selAnchorCol) {
		p.selActive, p.selPending = true, false
	}
}

// SelectRange selects an inclusive cell range in viewport coordinates.
func (p *Pane) SelectRange(row0, col0, row1, col1 int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	base := p.scr.ScrollbackLen() - p.scrollOffset
	p.selActive, p.selPending = true, false
	p.selAnchorLine, p.selAnchorCol = base+row0, col0
	p.selCaretLine, p.selCaretCol = base+row1, col1
}

// presentedRow is called with the pane lock held. It maps a click on the
// submitted frame into the current buffer when more output has arrived.
func (p *Pane) presentedRow(row int) int {
	frame := p.presented
	if frame.grid.Bounds.Empty() {
		return p.scr.ViewTop(p.scrollOffset) + row
	}
	length, added, reset := p.scr.ScrollbackState()
	if frame.reset != reset {
		return -1
	}
	dropped := max(0, frame.history+int(added-frame.added)-length)
	return frame.top + row - dropped
}

func (p *Pane) selectPresentedRange(row, first, last int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if abs := p.presentedRow(row); abs >= 0 {
		p.selActive, p.selPending = true, false
		p.selAnchorLine, p.selAnchorCol = abs, first
		p.selCaretLine, p.selCaretCol = abs, last
	}
}

// WordRange returns the inclusive column range of the word at a viewport cell.
func (p *Pane) WordRange(row, col int) (start, end int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	abs := p.presentedRow(row)
	line := p.scr.AbsLine(abs)
	if len(line) == 0 || col < 0 || col >= len(line) {
		return 0, 0, false
	}
	isWord := func(r rune) bool {
		return r != 0 && r != ' ' && !strings.ContainsRune("()[]{}'\"`,;:|<>", r)
	}
	col = leadingColumn(line, col)
	if !isWord(line[col].R) {
		return col, trailingColumn(line, col), true
	}
	s := col
	for s > 0 {
		left := leadingColumn(line, s-1)
		if !isWord(line[left].R) {
			break
		}
		s = left
	}
	e := trailingColumn(line, col)
	for e+1 < len(line) && isWord(line[e+1].R) {
		e = trailingColumn(line, e+1)
	}
	return s, e, true
}

// LineRange returns the last non-blank column of a viewport row.
func (p *Pane) LineRange(row int) (end int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	abs := p.presentedRow(row)
	line := p.scr.AbsLine(abs)
	if len(line) == 0 {
		return 0, false
	}
	for i := len(line) - 1; i >= 0; i-- {
		if line[i].R != 0 && line[i].R != ' ' {
			return trailingColumn(line, i), true
		}
	}
	return 0, true
}

// URLAt returns the http(s) URL at a viewport cell, if any.
func (p *Pane) URLAt(row, col int) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	abs := p.presentedRow(row)
	line := p.scr.AbsLine(abs)
	if len(line) == 0 || col < 0 || col >= len(line) {
		return "", false
	}
	isURLChar := func(r rune) bool {
		switch r {
		case 0, ' ', '"', 0x27, '`', '<', '>', '(', ')', '[', ']', '{', '}':
			return false
		}
		return true
	}
	s := col
	for s > 0 && isURLChar(line[s-1].R) {
		s--
	}
	e := col
	for e+1 < len(line) && isURLChar(line[e+1].R) {
		e++
	}
	var b strings.Builder
	for i := s; i <= e; i++ {
		b.WriteString(line[i].Text())
	}
	tok := strings.Trim(b.String(), ".,;:!?")
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
		return tok, true
	}
	return "", false
}

// SelectAbs selects an inclusive absolute cell range.
func (p *Pane) SelectAbs(abs0, col0, abs1, col1 int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selActive, p.selPending = true, false
	p.selAnchorLine, p.selAnchorCol = abs0, col0
	p.selCaretLine, p.selCaretCol = abs1, col1
}

// Find searches scrollback and the screen for query, starting after fromAbs.
// forward selects the next match, otherwise the previous one; the search wraps
// around the buffer.
func (p *Pane) Find(query string, fromAbs int, forward bool) (abs, col0, col1 int, ok bool) {
	if query == "" {
		return 0, 0, 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	q := []rune(strings.ToLower(query))
	total := p.scr.TotalLines()
	var runes []rune
	var cols []int

	scan := func(start int, fwd bool) (int, int, int, bool) {
		for i := 0; i < total; i++ {
			var line int
			if fwd {
				line = start + 1 + i
				if line >= total {
					break
				}
			} else {
				line = start - 1 - i
				if line < 0 {
					break
				}
			}
			cells := p.scr.AbsLine(line)
			runes, cols = runes[:0], cols[:0]
			for col, cell := range cells {
				for _, r := range cell.Text() {
					runes, cols = append(runes, r), append(cols, col)
				}
			}
			idx := runeIndexFold(runes, q)
			if idx < 0 {
				continue
			}
			return line, cols[idx], trailingColumn(cells, cols[idx+len(q)-1]), true
		}
		return 0, 0, 0, false
	}

	if abs, c0, c1, ok := scan(fromAbs, forward); ok {
		return abs, c0, c1, true
	}
	// Wrap around.
	if forward {
		return scan(-1, true)
	}
	return scan(total, false)
}

// runeIndexFold returns the rune index of needle (already lower-cased) in
// haystack, ignoring case.
func runeIndexFold(haystack, q []rune) int {
	if len(q) == 0 || len(q) > len(haystack) {
		return -1
	}
	for i := 0; i+len(q) <= len(haystack); i++ {
		match := true
		for j, r := range q {
			if unicode.ToLower(haystack[i+j]) != r {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// ScrollToAbs scrolls so the given absolute line is roughly centred.
func (p *Pane) ScrollToAbs(abs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sb := p.scr.ScrollbackLen()
	target := sb - abs + p.rows/2
	if target < 0 {
		target = 0
	}
	if target > sb {
		target = sb
	}
	p.scrollOffset = target
}

// SelectionClear drops the selection.
func (p *Pane) SelectionClear() {
	p.mu.Lock()
	p.selActive, p.selPending = false, false
	p.mu.Unlock()
}

// SelectionRange returns the normalized selection and whether it is active.
func (p *Pane) SelectionRange() (l0, c0, l1, c1 int, active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectionRangeLocked()
}

func (p *Pane) selectionRangeLocked() (l0, c0, l1, c1 int, active bool) {
	if !p.selActive {
		return 0, 0, 0, 0, false
	}
	l0, c0 = p.selAnchorLine, p.selAnchorCol
	l1, c1 = p.selCaretLine, p.selCaretCol
	if l0 > l1 || (l0 == l1 && c0 > c1) {
		l0, l1 = l1, l0
		c0, c1 = c1, c0
	}
	c0 = leadingColumn(p.scr.AbsLine(l0), c0)
	c1 = trailingColumn(p.scr.AbsLine(l1), c1)
	return l0, c0, l1, c1, true
}

func leadingColumn(cells []term.Cell, col int) int {
	if col > 0 && col < len(cells) && cells[col].Cont {
		return col - 1
	}
	return col
}

func trailingColumn(cells []term.Cell, col int) int {
	if col >= 0 && col+1 < len(cells) && cells[col].W == 2 {
		return col + 1
	}
	return col
}

// SelectedText returns the selected text.
func (p *Pane) SelectedText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	l0, c0, l1, c1, active := p.selectionRangeLocked()
	if !active {
		return ""
	}
	var out strings.Builder
	for line := l0; line <= l1; line++ {
		cells := p.scr.AbsLine(line)
		start, end := 0, len(cells)
		if line == l0 {
			start = c0
		}
		if line == l1 {
			end = c1 + 1
		}
		if start < 0 {
			start = 0
		}
		if end > len(cells) {
			end = len(cells)
		}
		wrap := p.scr.WrapColumn(line)
		if wrap != 0 {
			end = min(end, wrap)
		} else if line != l1 {
			for end > start && cells[end-1].IsBlank() && !cells[end-1].Cont {
				end--
			}
		}
		for x := start; x < end; x++ {
			out.WriteString(cells[x].Text())
		}
		if line != l1 && wrap == 0 {
			out.WriteByte('\n')
		}
	}
	return trimRight(out.String())
}

func trimRight(s string) string {
	end := len(s)
	for end > 0 {
		c := s[end-1]
		if c != ' ' && c != '\n' && c != '\t' {
			break
		}
		end--
	}
	return s[:end]
}

// AbsRowForRow converts a viewport row to an absolute line index using the
// current scroll offset.
func (p *Pane) AbsRowForRow(row int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.ScrollbackLen() - p.scrollOffset + row
}
