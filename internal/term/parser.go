package term

import (
	"encoding/base64"
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Parser states.
const (
	stGround = iota
	stEsc
	stEscInterm
	stCSI
	stOSC
	stOSCEsc
	stDCS
	stDCSEsc
	stCharset
	stHash
)

// Parser turns a byte stream into screen updates.
type Parser struct {
	s  *Screen
	st int

	priv       byte
	interm     []byte
	groups     [][]int
	params     []int
	groupStart int
	val        int
	has        bool

	str   []byte
	desig byte

	resizeUntil   time.Time
	resizeTails   map[coloredLineTail]int
	modernConsole bool

	// Incremental UTF-8 decoding.
	u     [4]byte
	un    int
	uneed int

	cellSize image.Point
}

// NewParser returns a parser writing into s.
func NewParser(s *Screen) *Parser { return &Parser{s: s} }

// SetModernConsole disables the legacy system ConPTY resize workaround.
func (p *Parser) SetModernConsole(modern bool) { p.modernConsole = modern }

// SetCellSize sets the rendered grid cell size in pixels for window queries.
func (p *Parser) SetCellSize(size image.Point) { p.cellSize = size }

// Resize updates the grid before ConPTY sends its repaint for the new size.
func (p *Parser) Resize(cols, rows int, positions ...*image.Point) {
	if cols != p.s.Cols || rows != p.s.Rows {
		if !p.modernConsole {
			p.resizeTails = p.s.coloredLineTails()
			p.resizeUntil = time.Now().Add(time.Second)
		}
		p.s.Resize(cols, rows, positions...)
	}
}

// Write feeds bytes to the parser.
func (p *Parser) Write(b []byte) {
	if !p.resizeUntil.IsZero() && time.Now().After(p.resizeUntil) {
		p.finishRepaint()
	}
	for _, c := range b {
		p.byte(c)
	}
}

func (p *Parser) byte(c byte) {
	s := p.s
	if p.st == stGround {
		// UTF-8 accumulation first.
		if p.un > 0 {
			if c&0xc0 == 0x80 {
				p.u[p.un] = c
				p.un++
				if p.un == p.uneed {
					r, _ := utf8.DecodeRune(p.u[:p.un])
					p.un = 0
					s.PutRune(r)
				}
				return
			}
			p.un = 0
			s.PutRune(utf8.RuneError)
		}
		if c >= 0x80 {
			if n := utf8Len(c); n > 0 {
				p.u[0] = c
				p.un = 1
				p.uneed = n
			} else {
				s.PutRune(utf8.RuneError)
			}
			return
		}
		p.control(c)
		return
	}
	if c == 0x18 || c == 0x1a { // CAN, SUB
		p.st = stGround
		return
	}

	switch p.st {
	case stEsc:
		p.esc(c)
	case stEscInterm:
		p.st = stGround
	case stCSI:
		p.csi(c)
	case stOSC:
		switch c {
		case 0x07:
			p.osc()
			p.st = stGround
		case 0x1b:
			p.st = stOSCEsc
		default:
			if len(p.str) < 1<<16 {
				p.str = append(p.str, c)
			}
		}
	case stOSCEsc:
		if c == '\\' {
			p.osc()
			p.st = stGround
		} else {
			p.st = stEsc
			p.esc(c)
		}
	case stDCS:
		if c == 0x1b {
			p.st = stDCSEsc
		}
	case stDCSEsc:
		if c == '\\' {
			p.st = stGround
		} else {
			p.st = stDCS
		}
	case stCharset:
		switch p.desig {
		case '(':
			s.g0 = c
		case ')':
			s.g1 = c
		}
		p.st = stGround
	case stHash:
		if c == '8' { // DECALN
			for y := 0; y < s.Rows; y++ {
				for x := 0; x < s.Cols; x++ {
					s.cells[y*s.Cols+x] = Cell{R: 'E', W: 1}
				}
			}
			s.Dirty = true
		}
		p.st = stGround
	}
}

func utf8Len(c byte) int {
	switch {
	case c >= 0xc2 && c <= 0xdf:
		return 2
	case c >= 0xe0 && c <= 0xef:
		return 3
	case c >= 0xf0 && c <= 0xf4:
		return 4
	}
	return 0
}

// control handles C0 controls in the ground state.
func (p *Parser) control(c byte) {
	s := p.s
	if c < 0x20 || c == 0x7f {
		s.endGrapheme()
	}
	switch c {
	case 0x07:
		s.Bell()
	case 0x08:
		s.Backspace()
	case 0x09:
		s.Tab()
	case 0x0a, 0x0b, 0x0c:
		s.LineFeed()
	case 0x0d:
		s.CarriageReturn()
	case 0x0e:
		s.shift1 = true
	case 0x0f:
		s.shift1 = false
	case 0x1b:
		p.st = stEsc
		p.priv = 0
		p.interm = p.interm[:0]
		p.resetParams()
		p.val, p.has = 0, false
	case 0x00:
		// NUL: ignore
	default:
		if c >= 0x20 && c < 0x7f {
			s.PutRune(rune(c))
		}
	}
}

func (p *Parser) esc(c byte) {
	s := p.s
	p.st = stGround
	switch c {
	case '[':
		p.st = stCSI
		p.priv = 0
		p.interm = p.interm[:0]
		p.resetParams()
		p.val, p.has = 0, false
	case ']':
		p.st = stOSC
		p.str = p.str[:0]
	case 'P', 'X', '^', '_':
		p.st = stDCS
	case '(', ')', '*', '+':
		p.desig = c
		p.st = stCharset
	case '#':
		p.st = stHash
	case ' ':
		p.st = stEscInterm
	case '7':
		s.SaveCursor()
	case '8':
		s.RestoreCursor()
	case 'D':
		s.LineFeed()
	case 'E':
		s.CarriageReturn()
		s.LineFeed()
	case 'M':
		s.ReverseIndex()
	case 'H':
		s.SetTabStop()
	case 'c':
		s.Reset()
		p.finishRepaint()
	case '=':
		s.appKeypad = true
	case '>':
		s.appKeypad = false
	case 'N', 'O', 'Z':
		// Single shift / identify: ignored.
	case '\\':
		// ST
	default:
		// Unknown escape: ignore.
	}
}

// ---------------------------------------------------------------------------
// CSI

func (p *Parser) addSub() {
	p.params = append(p.params, p.val)
	p.val = 0
	p.has = false
}

func (p *Parser) addGroup() {
	p.addSub()
	p.groups = append(p.groups, p.params[p.groupStart:])
	p.groupStart = len(p.params)
}

func (p *Parser) resetParams() {
	// Keep the common short CSI sequences allocation-free after warmup.
	clear(p.groups)
	p.groups, p.params, p.groupStart = p.groups[:0], p.params[:0], 0
	if cap(p.params) > 256 || cap(p.groups) > 256 {
		p.groups, p.params = nil, nil
	}
}

func (p *Parser) finish() {
	if p.has || len(p.params) > p.groupStart {
		p.addGroup()
	}
}

func (p *Parser) arg(i, def int) int {
	if i >= len(p.groups) || len(p.groups[i]) == 0 {
		return def
	}
	return p.groups[i][0]
}

func (p *Parser) csi(c byte) {
	if len(p.params) >= 256 && c >= 0x20 && c < 0x40 {
		return
	}
	switch {
	case c >= '0' && c <= '9':
		p.val = p.val*10 + int(c-'0')
		if p.val > 1<<20 {
			p.val = 1 << 20
		}
		p.has = true
	case c == ';':
		p.addGroup()
	case c == ':':
		p.addSub()
	case c == '?' || c == '>' || c == '<' || c == '=':
		if len(p.groups) == 0 && len(p.params) == 0 && !p.has && p.priv == 0 {
			p.priv = c
		} else if len(p.interm) < 8 {
			p.interm = append(p.interm, c)
		}
	case c >= 0x20 && c <= 0x2f:
		if len(p.interm) < 8 {
			p.interm = append(p.interm, c)
		}
	case c >= 0x40 && c <= 0x7e:
		p.st = stGround
		p.final(c)
	case c == 0x1b:
		p.st = stEsc
	case c < 0x20:
		p.control(c)
	}
}

func (p *Parser) final(c byte) {
	s := p.s
	p.finish()
	if p.priv != 0 || len(p.interm) != 0 {
		p.extendedCSI(c)
		return
	}
	n := max(1, p.arg(0, 1))
	switch c {
	case 'A':
		s.MoveRel(0, -n)
	case 'B', 'e':
		s.MoveRel(0, n)
	case 'C', 'a':
		s.MoveRel(n, 0)
	case 'D':
		s.MoveRel(-n, 0)
	case 'E':
		s.MoveRel(0, n)
		s.CarriageReturn()
	case 'F':
		s.MoveRel(0, -n)
		s.CarriageReturn()
	case 'G', '`':
		s.SetColumn(n - 1)
	case 'H', 'f':
		s.MoveTo(p.arg(1, 1)-1, p.arg(0, 1)-1)
	case 'I':
		for i := 0; i < n; i++ {
			s.Tab()
		}
	case 'Z':
		for i := 0; i < n; i++ {
			s.BackTab()
		}
	case 'J':
		s.EraseInDisplay(p.arg(0, 0))
	case 'K':
		mode := p.arg(0, 0)
		if s.repainting && len(p.groups) == 0 && s.attr.BG != DefaultColor {
			s.eraseResizeTail(p.resizeTails)
		} else {
			s.EraseInLine(mode)
		}
	case 'L':
		s.InsertLines(n)
	case 'M':
		s.DeleteLines(n)
	case 'P':
		s.DeleteChars(n)
	case 'S':
		s.scrollUp(n)
	case 'T':
		s.scrollDown(n)
	case 'X':
		s.EraseChars(n)
	case '@':
		s.InsertChars(n)
	case 'b':
		if glyph := s.lastGlyph; glyph.R != 0 {
			glyph.A = s.attr
			glyph.W = uint8(min(int(glyph.W), s.Cols))
			for i := 0; i < n; i++ {
				s.putGlyph(glyph)
			}
		}
	case 'c':
		if p.arg(0, 0) == 0 {
			p.reply("\x1b[?62;22c")
		}
	case 'd':
		s.SetRow(n - 1)
	case 'g':
		s.ClearTabStops(p.arg(0, 0))
	case 'h':
		p.setModes(true)
	case 'l':
		p.setModes(false)
	case 'm':
		s.SGR(p.groups)
	case 'n':
		p.reportStatus(false)
	case 'r':
		bot := p.arg(1, s.Rows)
		if bot == 0 {
			bot = s.Rows
		}
		s.SetScrollRegion(n-1, bot-1)
	case 's':
		if len(p.groups) == 0 {
			s.SaveCursor()
		}
	case 'u':
		if len(p.groups) == 0 {
			s.RestoreCursor()
		}
	case 't':
		switch p.arg(0, 0) {
		case 14:
			if p.cellSize.X > 0 && p.cellSize.Y > 0 {
				p.reply(fmt.Sprintf("\x1b[4;%d;%dt", s.Rows*p.cellSize.Y, s.Cols*p.cellSize.X))
			}
		case 16:
			if p.cellSize.X > 0 && p.cellSize.Y > 0 {
				p.reply(fmt.Sprintf("\x1b[6;%d;%dt", p.cellSize.Y, p.cellSize.X))
			}
		case 18:
			p.reply(fmt.Sprintf("\x1b[8;%d;%dt", s.Rows, s.Cols))
		}
	}
}

func (p *Parser) extendedCSI(c byte) {
	param := p.arg(0, 0)
	switch string(p.interm) {
	case " ":
		if p.priv == 0 && c == 'q' && param <= 6 {
			p.s.CursorStyle = param
		}
	case "$":
		if c == 'p' && (p.priv == 0 || p.priv == '?') {
			prefix := ""
			if p.priv == '?' {
				prefix = "?"
			}
			p.reply(fmt.Sprintf("\x1b[%s%d;%d$y", prefix, param, p.s.modeStatus(p.priv == '?', param)))
		}
	case "":
		switch p.priv {
		case '>':
			if c == 'c' && param == 0 {
				p.reply("\x1b[>0;276;0c")
			}
			if c == 'q' && param == 0 {
				p.reply("\x1bP>|gon\x1b\\")
			}
		case '?':
			switch c {
			case 'h', 'l':
				p.setModes(c == 'h')
			case 'n':
				p.reportStatus(true)
			}
		}
	}
}

func (p *Parser) reportStatus(private bool) {
	s := p.s
	switch p.arg(0, 0) {
	case 5:
		if !private {
			p.reply("\x1b[0n")
		}
	case 6:
		row := s.CursorY + 1
		if s.originMode {
			row -= s.scrollTop
		}
		if private {
			p.reply(fmt.Sprintf("\x1b[?%d;%d;1R", row, s.CursorX+1))
		} else {
			p.reply(fmt.Sprintf("\x1b[%d;%dR", row, s.CursorX+1))
		}
	case 996:
		if private {
			p.reportColorScheme()
		}
	}
}

func (p *Parser) finishRepaint() {
	p.s.repainting = false
	p.resizeUntil = time.Time{}
	p.resizeTails = nil
}

func (p *Parser) setModes(on bool) {
	s := p.s
	if len(p.groups) == 0 {
		return
	}
	for _, g := range p.groups {
		if len(g) == 0 {
			continue
		}
		if p.priv == '?' {
			if g[0] == 25 && s.repainting {
				p.finishRepaint()
			}
			s.SetDecMode(g[0], on)
		} else if !on && g[0] == 25 && len(p.groups) == 1 && !p.resizeUntil.IsZero() {
			// Older ConPTY versions prefix the resize repaint with ANSI
			// CSI 25 l and finish with the DEC cursor visibility sequence.
			s.repainting = true
		} else {
			s.SetAnsiMode(g[0], on)
		}
	}
}

func (p *Parser) reply(s string) {
	if p.s.Reply != nil {
		p.s.Reply(s)
	}
}

// ---------------------------------------------------------------------------
// OSC

func (p *Parser) osc() {
	if len(p.str) == 0 {
		return
	}
	parts := strings.SplitN(string(p.str), ";", 2)
	num, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}
	s := p.s
	switch num {
	case 0, 1, 2:
		if rest != "" || num == 2 {
			s.Title = rest
			s.Dirty = true
		}
	case 4:
		// Color palette change: ignored, the theme wins.
	case 10, 11, 12:
		for _, value := range strings.Split(rest, ";") {
			if num > 12 {
				break
			}
			color, fallback := s.displayColor(num)
			if value == "?" {
				p.reply(fmt.Sprintf("\x1b]%d;%s\x1b\\", num, formatOSCColor(color.RGBOr(fallback))))
			} else if v, ok := parseOSCColor(value); ok {
				p.setDisplayColor(color, v)
			}
			num++
		}
	case 110, 111, 112:
		color, _ := s.displayColor(num - 100)
		p.setDisplayColor(color, DefaultColor)
	case 52:
		// Clipboard: OSC 52;c;base64
		fields := strings.SplitN(rest, ";", 2)
		if len(fields) == 2 {
			if data, err := base64.StdEncoding.DecodeString(fields[1]); err == nil {
				if s.OnClipboard != nil {
					s.OnClipboard(string(data))
				}
			}
		}
	case 8:
		// Hyperlinks: the text is rendered, the link target is ignored.
	case 9:
		if value, ok := parseProgress(rest); ok {
			s.Progress = value
		}
	}
}

func (p *Parser) setDisplayColor(dst *Color, value Color) {
	scheme := p.s.colorScheme()
	*dst = value
	p.s.Dirty = true
	if p.s.colorSchemeEvents && p.s.colorScheme() != scheme {
		p.reportColorScheme()
	}
}

func (p *Parser) reportColorScheme() {
	p.reply(fmt.Sprintf("\x1b[?997;%dn", p.s.colorScheme()))
}

func parseOSCColor(s string) (Color, bool) {
	var parts []string
	if tail, ok := strings.CutPrefix(s, "rgb:"); ok {
		parts = strings.Split(tail, "/")
	} else if tail, ok := strings.CutPrefix(s, "#"); ok && len(tail)%3 == 0 {
		n := len(tail) / 3
		parts = []string{tail[:n], tail[n : 2*n], tail[2*n:]}
	}
	if len(parts) != 3 {
		return DefaultColor, false
	}
	var rgb [3]int
	for i, part := range parts {
		if len(part) < 1 || len(part) > 4 {
			return DefaultColor, false
		}
		v, err := strconv.ParseUint(part, 16, 16)
		if err != nil {
			return DefaultColor, false
		}
		maxValue := (1 << (4 * len(part))) - 1
		rgb[i] = int(v) * 255 / maxValue
	}
	return RGB(rgb[0], rgb[1], rgb[2]), true
}

func formatOSCColor(rgb uint32) string {
	return fmt.Sprintf("rgb:%04x/%04x/%04x", uint32(uint8(rgb>>16))*257, uint32(uint8(rgb>>8))*257, uint32(uint8(rgb))*257)
}

// ---------------------------------------------------------------------------
// SGR and modes

// SGR applies a Select Graphic Rendition sequence.
func (s *Screen) SGR(groups [][]int) {
	if len(groups) == 0 {
		s.attr = Attr{}
		return
	}
	for i := 0; i < len(groups); i++ {
		g := groups[i]
		if len(g) == 0 {
			s.attr = Attr{}
			continue
		}
		code := g[0]
		switch {
		case code == 0:
			s.attr = Attr{}
		case code == 1:
			s.attr.Flags |= AttrBold
		case code == 2:
			s.attr.Flags |= AttrDim
		case code == 3:
			s.attr.Flags |= AttrItalic
		case code == 4:
			s.attr.Flags |= AttrUnderline
		case code == 5, code == 6:
			s.attr.Flags |= AttrBlink
		case code == 7:
			s.attr.Flags |= AttrReverse
		case code == 8:
			s.attr.Flags |= AttrInvisible
		case code == 9:
			s.attr.Flags |= AttrStrike
		case code == 21:
			s.attr.Flags &^= AttrBold
		case code == 22:
			s.attr.Flags &^= AttrBold | AttrDim
		case code == 23:
			s.attr.Flags &^= AttrItalic
		case code == 24:
			s.attr.Flags &^= AttrUnderline
		case code == 25:
			s.attr.Flags &^= AttrBlink
		case code == 27:
			s.attr.Flags &^= AttrReverse
		case code == 28:
			s.attr.Flags &^= AttrInvisible
		case code == 29:
			s.attr.Flags &^= AttrStrike
		case code >= 30 && code <= 37:
			s.attr.FG = Indexed(code - 30)
		case code == 38:
			if c, ok, adv := extColor(g, groups, i); ok {
				s.attr.FG = c
				i += adv
			}
		case code == 39:
			s.attr.FG = DefaultColor
		case code >= 40 && code <= 47:
			s.attr.BG = Indexed(code - 40)
		case code == 48:
			if c, ok, adv := extColor(g, groups, i); ok {
				s.attr.BG = c
				i += adv
			}
		case code == 49:
			s.attr.BG = DefaultColor
		case code == 58:
			// Underline color: parsed and dropped.
			_, _, adv := extColor(g, groups, i)
			i += adv
		case code >= 90 && code <= 97:
			s.attr.FG = Indexed(code - 90 + 8)
		case code >= 100 && code <= 107:
			s.attr.BG = Indexed(code - 100 + 8)
		}
	}
}

// extColor parses an extended color. It returns the color, whether it was
// valid, and how many following parameter groups it consumed.
func extColor(g []int, groups [][]int, i int) (Color, bool, int) {
	if len(g) >= 3 {
		switch g[1] {
		case 5:
			return Indexed(g[2]), true, 0
		case 2:
			vals := g[2:]
			if len(vals) > 3 {
				vals = vals[len(vals)-3:]
			}
			if len(vals) == 3 {
				return RGB(vals[0], vals[1], vals[2]), true, 0
			}
		}
	}
	if i+1 < len(groups) && len(groups[i+1]) > 0 {
		switch groups[i+1][0] {
		case 5:
			if i+2 < len(groups) && len(groups[i+2]) > 0 {
				return Indexed(groups[i+2][0]), true, 2
			}
		case 2:
			if i+4 < len(groups) {
				return RGB(groups[i+2][0], groups[i+3][0], groups[i+4][0]), true, 4
			}
		}
	}
	return DefaultColor, false, 0
}

// SetDecMode applies CSI ? Pm h/l.
func (s *Screen) SetDecMode(num int, on bool) {
	switch num {
	case 1:
		s.appCursorKeys = on
	case 6:
		s.originMode = on
		s.MoveTo(0, 0)
	case 7:
		s.autowrap = on
		if !on {
			s.wrapPending = false
		}
	case 12:
		if s.CursorStyle == 0 {
			s.CursorStyle = 1
		}
		if on && s.CursorStyle%2 == 0 {
			s.CursorStyle--
		} else if !on && s.CursorStyle%2 == 1 {
			s.CursorStyle++
		}
	case 25:
		s.CursorVisible = on
	case 47, 1047:
		s.SetAltScreen(on, false)
	case 1048:
		if on {
			s.SaveCursor()
		} else {
			s.RestoreCursor()
		}
	case 1049:
		s.SetAltScreen(on, true)
	case MousePressOnly, MouseClicks, MouseDrag, MouseMotion:
		if on {
			s.mouseMode = num
		} else if s.mouseMode == num {
			s.mouseMode = 0
		}
	case 1004:
		s.focusEvents = on
	case 1005, 1006, 1015, 1016:
		format := mouseFormat(num)
		if on {
			s.mouseEncoding = format
		} else if s.mouseEncoding == format {
			s.mouseEncoding = MouseX10
		}
	case 1007:
		s.alternateScroll = on
	case 2004:
		s.bracketedPaste = on
	case 2026:
		s.synchronizeOutput(on)
	case 2031:
		s.colorSchemeEvents = on
	}
	s.Dirty = true
}

func mouseFormat(mode int) MouseEncoding {
	switch mode {
	case 1005:
		return MouseUTF8
	case 1006:
		return MouseSGR
	case 1015:
		return MouseURXVT
	case 1016:
		return MousePixels
	}
	return MouseX10
}

// SetAnsiMode applies CSI Pm h/l.
func (s *Screen) SetAnsiMode(num int, on bool) {
	switch num {
	case 4:
		s.insertMode = on
	case 20:
		s.newlineMode = on
	}
	s.Dirty = true
}
