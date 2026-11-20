package term

import (
	"image"
	"strings"
	"testing"
)

func feed(s *Screen, data string) {
	p := NewParser(s)
	p.Write([]byte(data))
}

func TestBasicText(t *testing.T) {
	s := NewScreen(20, 5, 100)
	feed(s, "hello")
	if got := s.Text(s.ScrollbackLen()); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if x, y := s.Cursor(); x != 5 || y != 0 {
		t.Fatalf("cursor %d,%d", x, y)
	}
}

func TestCRLF(t *testing.T) {
	s := NewScreen(20, 5, 100)
	feed(s, "abc\r\ndef")
	if got := s.Text(s.ScrollbackLen()); got != "abc" {
		t.Fatalf("line0 %q", got)
	}
	if got := s.Text(s.ScrollbackLen() + 1); got != "def" {
		t.Fatalf("line1 %q", got)
	}
}

func TestWrap(t *testing.T) {
	s := NewScreen(5, 3, 100)
	feed(s, "abcdefg")
	if got := s.Text(s.ScrollbackLen()); got != "abcde" {
		t.Fatalf("line0 %q", got)
	}
	if got := s.Text(s.ScrollbackLen() + 1); got != "fg" {
		t.Fatalf("line1 %q", got)
	}
}

func TestScrollback(t *testing.T) {
	s := NewScreen(10, 2, 100)
	feed(s, "1\r\n2\r\n3\r\n4")
	if s.ScrollbackLen() != 2 {
		t.Fatalf("scrollback %d", s.ScrollbackLen())
	}
	if got := s.Text(0); got != "1" {
		t.Fatalf("abs0 %q", got)
	}
}

func TestSGRColors(t *testing.T) {
	s := NewScreen(20, 3, 10)
	feed(s, "\x1b[31mR\x1b[0mN")
	c := s.CellAt(0, 0)
	if c.A.FG.Mode != ColorIndexed || c.A.FG.Idx != 1 {
		t.Fatalf("fg %+v", c.A.FG)
	}
	c2 := s.CellAt(1, 0)
	if c2.A.FG.Mode != ColorDefault {
		t.Fatalf("default fg %+v", c2.A.FG)
	}
}

func TestTrueColor(t *testing.T) {
	s := NewScreen(20, 3, 10)
	feed(s, "\x1b[38;2;10;20;30mX")
	c := s.CellAt(0, 0)
	if c.A.FG.Mode != ColorRGB || c.A.FG.R != 10 || c.A.FG.G != 20 || c.A.FG.B != 30 {
		t.Fatalf("fg %+v", c.A.FG)
	}
}

func TestCursorMove(t *testing.T) {
	s := NewScreen(20, 5, 10)
	feed(s, "\x1b[3;4HZ")
	if x, y := s.Cursor(); x != 4 || y != 2 {
		t.Fatalf("cursor %d,%d", x, y)
	}
	if got := s.Text(s.ScrollbackLen() + 2); got != "   Z" {
		t.Fatalf("line %q", got)
	}
}

func TestEraseLine(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "abcdef\x1b[1K")
	if got := s.Text(s.ScrollbackLen()); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestAltScreen(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "main")
	feed(s, "\x1b[?1049h")
	if !s.AltScreen() {
		t.Fatal("expected alt screen")
	}
	feed(s, "alt")
	if got := s.Text(s.ScrollbackLen()); got != "alt" {
		t.Fatalf("alt %q", got)
	}
	feed(s, "\x1b[?1049l")
	if s.AltScreen() {
		t.Fatal("expected main screen")
	}
	if got := s.Text(s.ScrollbackLen()); got != "main" {
		t.Fatalf("main %q", got)
	}
}

func TestAlternateScreenKeepsMainHistorySeparate(t *testing.T) {
	s := NewScreen(20, 2, 10)
	p := NewParser(s)
	p.Write([]byte("earlier shell output\r\none\r\ntwo"))
	p.Write([]byte("\x1b[?1049hfullscreen\x1b[?1049h\x1b[3J\x1b7"))
	if s.ScrollbackLen() != 0 || s.Text(0) != "fullscreen" {
		t.Fatalf("alternate viewport = %q with %d history rows", s.Text(0), s.ScrollbackLen())
	}
	p.Write([]byte("\x1b[?1049l!"))
	if s.ScrollbackLen() != 1 || s.Text(0) != "earlier shell output" || s.Text(2) != "two!" {
		t.Fatalf("main viewport did not restore: %q / %q", s.Text(0), s.Text(2))
	}
}

func TestModernTUIQueriesPreserveCursorAndReportTheme(t *testing.T) {
	s := NewScreen(25, 8, 10)
	s.SetThemeColors(0xabcdef, 0x102030, 0x234567)
	var replies strings.Builder
	s.Reply = func(text string) { replies.WriteString(text) }
	p := NewParser(s)
	p.SetCellSize(image.Pt(8, 15))
	p.Write([]byte("\x1b[3;4H\x1b[?u\x1b[>1u\x1b[>q\x1b]11;?\x07\x1b[?996n\x1b[14t\x1b[16t\x1b[18t"))
	if x, y := s.Cursor(); x != 3 || y != 2 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
	want := "\x1bP>|gon\x1b\\\x1b]11;rgb:1010/2020/3030\x1b\\\x1b[?997;1n" +
		"\x1b[4;120;200t\x1b[6;15;8t\x1b[8;8;25t"
	if got := replies.String(); got != want {
		t.Fatalf("terminal replies = %q, want %q", got, want)
	}
	p.Write([]byte("\x1b[?1004h\x1b[?2031h\x1b]11;#fff\x1b\\"))
	if !s.FocusEvents() || !strings.HasSuffix(replies.String(), "\x1b[?997;2n") {
		t.Fatalf("mode or color notification missing: %q", replies.String())
	}
}

func TestSynchronizedOutputPresentsOnlyCompletedFrame(t *testing.T) {
	s := NewScreen(10, 3, 0)
	p := NewParser(s)
	p.Write([]byte("before\r\x1b[?2026hafter"))
	frame, deadline := s.DisplayFrame(s.syncUntil.Add(-1))
	if deadline.IsZero() || frame.Text(0) != "before" {
		t.Fatalf("partial update became visible: %q", frame.Text(0))
	}
	p.Write([]byte("!\x1b[?2026l"))
	frame, deadline = s.DisplayFrame(deadline)
	if !deadline.IsZero() || frame.Text(0) != "after!" {
		t.Fatalf("completed frame = %q", frame.Text(0))
	}
}

func TestLegacyResizeRepaintDoesNotAddHistory(t *testing.T) {
	s := NewScreen(12, 2, 10)
	p := NewParser(s)
	p.Write([]byte("old\r\ncurrent\r\nend"))
	p.Resize(14, 2)
	p.Write([]byte("\x1b[25l\x1b[1S\x1b[Hcopy\x1b[?25h"))
	if s.ScrollbackLen() != 1 || s.Text(0) != "old" {
		t.Fatalf("redraw leaked to history: %d rows, first %q", s.ScrollbackLen(), s.Text(0))
	}
	p.Write([]byte("\x1b[1S"))
	if s.ScrollbackLen() != 2 || s.Text(1) != "copy" {
		t.Fatal("ordinary scrolling did not resume after the repaint")
	}
}

func TestConPTYResizeRepaintKeepsBlankBackground(t *testing.T) {
	s := NewScreen(8, 4, 10)
	p := NewParser(s)
	p.Write([]byte("\x1b[5C\x1b[44mDo\x1b[m\r\n\x1b[44mstatus\x1b[K\x1b[m\r\n"))
	p.Resize(14, 4)
	p.Write([]byte("\x1b[25"))
	p.Write([]byte("l\x1b[H\x1b[5C\x1b[44mDo\x1b[K\x1b[m\r\n\x1b[44mstatus\x1b[K\x1b[m\r\n\x1b[?25h"))

	blue := Indexed(4)
	assertBackground := func(row, from, to int) {
		t.Helper()
		for x := 0; x < s.Cols; x++ {
			want := DefaultColor
			if x >= from && x < to {
				want = blue
			}
			if got := s.CellAt(x, row).A.BG; got != want {
				t.Errorf("background at %d,%d = %+v, want %+v", x, row, got, want)
			}
		}
	}
	assertBackground(0, 5, 7)
	assertBackground(1, 0, 8)
	if s.Text(0) != "     Do" || s.Text(1) != "status" {
		t.Fatal("the resize repaint changed the visible text")
	}

	p.Write([]byte("\x1b[?25l\x1b[44mbar\x1b[K\x1b[m\x1b[?25h"))
	assertBackground(2, 0, s.Cols)

	p.Resize(11, 3)
	p.Write([]byte("\x1b[25l\x1b[H\x1b[44mstatus\x1b[K\x1b[m\x1b[?25h"))
	assertBackground(0, 0, 8)
}

func TestScrollRegion(t *testing.T) {
	s := NewScreen(10, 5, 10)
	feed(s, "a\r\nb\r\nc\r\nd\r\ne")
	feed(s, "\x1b[2;4r")    // region rows 1..3
	feed(s, "\x1b[4;1H\nX") // LF at bottom of region scrolls it
	line := s.Text(s.ScrollbackLen() + 1)
	if !strings.HasPrefix(line, "c") {
		t.Fatalf("region scroll line1=%q", line)
	}
}

func TestWideChars(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "中文")
	if c := s.CellAt(0, 0); c.W != 2 {
		t.Fatalf("wide cell W=%d", c.W)
	}
	if x, _ := s.Cursor(); x != 4 {
		t.Fatalf("cursor %d", x)
	}
}

func TestResizeReflowsGraphemesAndHistoryOnce(t *testing.T) {
	s := NewScreen(9, 3, 20)
	p := NewParser(s)
	line := "A中👩🏻‍💻🇨🇳e\u0301Z012345"
	p.Write([]byte("header\r\n" + line + "\r\nprompt>"))
	mark := image.Pt(2, 1) // second cell of 中 in the old buffer
	assertContent := func() {
		t.Helper()
		var logical []string
		var pending string
		for y := range s.TotalLines() {
			pending += s.Text(y)
			if s.WrapColumn(y) == 0 {
				if pending != "" {
					logical = append(logical, pending)
				}
				pending = ""
			}
		}
		if got := strings.Join(logical, "\n"); got != "header\n"+line+"\nprompt>" {
			t.Fatalf("reflow lost or repeated cells: %q", got)
		}
		if !s.AbsLine(mark.Y)[mark.X].Cont {
			t.Fatalf("tracked position escaped the wide cell: %v", mark)
		}
	}
	assertContent()
	p.Resize(5, 2, &mark)
	assertContent()
	p.Resize(25, 6, &mark)
	assertContent()
	if x, y := s.Cursor(); x != 7 || y != 0 || s.Text(s.ScrollbackLen()) != "prompt>" {
		t.Fatalf("cursor restored to %d,%d", x, y)
	}
}

func TestResizeRepaintReplacesOverlappingHistory(t *testing.T) {
	const (
		shellLine = "ordinary repeated command output"
		oldTitle  = "█   Workspace terminal v2.1.328"
		newTitle  = "░   Workspace terminal v2.1.328"
		status    = "UTF-8 console active"
	)
	for _, tc := range []struct {
		name, erase, status string
		wantTitles          int
	}{
		{"whole display", "\x1b[2J", status, 1},
		{"all rows", strings.Repeat("\x1b[2K\x1b[B", 4), status, 1},
		{"one row", "\x1b[2K", status, 2},
		{"different contents", "\x1b[2J", "Connection pending", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScreen(60, 8, 30)
			p := NewParser(s)
			p.SetModernConsole(true)
			p.Write([]byte(strings.Join([]string{shellLine, shellLine, oldTitle, status,
				"previous layout", "old prompt", "old footer"}, "\r\n")))
			p.Resize(40, 4)
			if s.ScrollbackLen() != 3 || s.Text(2) != oldTitle {
				t.Fatalf("resize did not move the old title into history: %q", s.Text(2))
			}
			p.Write([]byte("\x1b[?2026h\x1b[H" + tc.erase + "\x1b[H" + newTitle + "\r\n" +
				tc.status + "\r\nnew layout\r\nnew prompt\r\nnew footer\x1b[?2026"))
			if got := s.ScrollbackLen(); got != 4 {
				t.Fatalf("uncommitted repaint has %d history rows, want 4", got)
			}
			p.Write([]byte("l"))
			titles := 0
			for row := range s.TotalLines() {
				if strings.Contains(s.Text(row), "Workspace terminal") {
					titles++
				}
			}
			if titles != tc.wantTitles || s.Text(0) != shellLine || s.Text(1) != shellLine {
				t.Fatalf("history has %d titles and shell rows %q / %q", titles, s.Text(0), s.Text(1))
			}
			if got := s.Text(s.TotalLines() - 1); got != "new footer" {
				t.Fatalf("last line = %q", got)
			}
		})
	}
}

func TestResizeRepaintStartsInsideHistoryLine(t *testing.T) {
	s := NewScreen(72, 8, 30)
	p := NewParser(s)
	const (
		start = "Answer 42: "
		tail  = "this passage continues from the preceding part of the line"
		next  = "another complete line of output"
	)
	p.Write([]byte("earlier shell output\r\n" + start + tail + "\r\n" + next + "\r\nold prompt"))
	p.Resize(72, 2)
	p.Write([]byte("\x1b[?2026h\x1b[2J\x1b[H" + tail + "\r\n" + next + "\r\nnew prompt\x1b[?2026l"))
	want := []string{"earlier shell output", "Answer 42:", tail, next, "new prompt"}
	if s.TotalLines() != len(want) || s.WrapColumn(1) != len(start) {
		t.Fatalf("repaint left %d rows and wrap column %d", s.TotalLines(), s.WrapColumn(1))
	}
	for row, line := range want {
		if got := s.Text(row); got != line {
			t.Errorf("line %d = %q, want %q", row, got, line)
		}
	}
}

func TestGraphemeWidthAndByteChunks(t *testing.T) {
	s := NewScreen(12, 2, 0)
	p := NewParser(s)
	for _, b := range []byte("A👩🏻‍💻🇨🇳e\u0301中Z") {
		p.Write([]byte{b})
	}
	for _, want := range []struct {
		col  int
		text string
	}{{0, "A"}, {1, "👩🏻‍💻"}, {3, "🇨🇳"}, {5, "e\u0301"}, {6, "中"}, {8, "Z"}} {
		if got := s.CellAt(want.col, 0).Text(); got != want.text {
			t.Errorf("text at column %d = %q, want %q", want.col, got, want.text)
		}
	}
}

func TestInsertDeleteChars(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "abcdef")
	feed(s, "\x1b[1;2H\x1b[2@") // insert 2 blanks at col 1
	if got := s.Text(s.ScrollbackLen()); got != "a  bcdef" {
		t.Fatalf("insert %q", got)
	}
	feed(s, "\x1b[1;1H\x1b[2P")
	if got := s.Text(s.ScrollbackLen()); got != " bcdef" {
		t.Fatalf("delete %q", got)
	}
}

func TestOSCTitle(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "\x1b]0;My Title\x07")
	if s.Title != "My Title" {
		t.Fatalf("title %q", s.Title)
	}
}

func TestBracketedPaste(t *testing.T) {
	s := NewScreen(10, 3, 10)
	feed(s, "\x1b[?2004h")
	if !s.BracketedPaste() {
		t.Fatal("expected bracketed paste")
	}
}

func TestEncodeKey(t *testing.T) {
	st := KeyState{}
	if got, _ := EncodeKey("←", 0, st); got != "\x1b[D" {
		t.Fatalf("left %q", got)
	}
	if got, _ := EncodeKey("←", ModCtrl, st); got != "\x1b[1;5D" {
		t.Fatalf("ctrl-left %q", got)
	}
	if got, _ := EncodeKey("a", ModCtrl, st); got != "\x01" {
		t.Fatalf("ctrl-a %q", got)
	}
	if got, _ := EncodeKey("F5", 0, st); got != "\x1b[15~" {
		t.Fatalf("F5 %q", got)
	}
	if got, _ := EncodeKey("Tab", ModShift, st); got != "\x1b[Z" {
		t.Fatalf("shift-tab %q", got)
	}
}

func TestPaste(t *testing.T) {
	if got := Paste("a\nb", true); got != "\x1b[200~a\nb\x1b[201~" {
		t.Fatalf("bracketed %q", got)
	}
	if got := Paste("a\r\nb", false); got != "a\nb" {
		t.Fatalf("plain %q", got)
	}
}
func TestScrollbackWrapsAndShrinks(t *testing.T) {
	s := NewScreen(8, 1, 3)
	p := NewParser(s)
	p.Write([]byte("A\r\nB\r\nC\r\nD\r\nE\r\n"))
	for i, want := range []string{"C", "D", "E"} {
		if got := s.Text(i); got != want {
			t.Fatalf("wrapped line %d = %q, want %q", i, got, want)
		}
	}
	s.SetScrollbackLimit(2)
	if s.ScrollbackLen() != 2 || s.Text(0) != "D" || s.Text(1) != "E" {
		t.Fatal("shrink did not retain the newest lines in order")
	}
	p.Write([]byte("F\r\n"))
	s.SetScrollbackLimit(4)
	p.Write([]byte("G\r\n"))
	if got := s.ScrollbackLen(); got != 3 || s.Text(0) != "E" || s.Text(2) != "G" {
		t.Fatal("growing a wrapped buffer lost line order")
	}
	s.SetScrollbackLimit(0)
	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("disabled history still contains %d lines", got)
	}
}
