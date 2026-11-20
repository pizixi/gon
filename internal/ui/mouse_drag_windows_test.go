package ui

import (
	"fmt"
	"image"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"gioui.org/io/pointer"
	"golang.org/x/sys/windows"

	"gon/internal/config"
	"gon/internal/pty"
	"gon/internal/term"
)

// This fixture paints its own selection through real console input/output,
// including synchronized updates. It never relies on Gon's local highlight.
func TestTUIHighlightArrivesBeforeMouseRelease(t *testing.T) {
	const childEnv = "GON_DRAG_TEST_CHILD"
	if os.Getenv(childEnv) == "1" {
		runMouseSelectionFixture(t)
		return
	}
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPaneWithOptions("mouse fixture", exe, []string{"-test.run=^TestTUIHighlightArrivesBeforeMouseRelease$"},
		pty.Options{Env: append(os.Environ(), childEnv+"=1")}, 80, 24, 1000)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Default(), nil, "")
	a.started = true
	tb := new(Tab)
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	t.Cleanup(a.shutdown)
	go p.ReadLoop(nil)
	h := &layoutTest{App: a, size: image.Pt(1000, 600)}
	h.frame()
	if !waitText(t, p, "mouse-fixture-ready", 5*time.Second) {
		t.Fatalf("fixture did not start: %s", screenText(p))
	}
	h.frame()
	grid := p.presented.grid
	point := func(col int) image.Point {
		m := grid.Metrics
		return grid.Bounds.Min.Add(image.Pt(col*m.CellW+m.CellW/2, m.CellH+m.CellH/2))
	}
	h.selectionCursor = func() (image.Point, bool) { return point(9), true }
	h.mouse(pointer.Press, point(2), pointer.ButtonPrimary)
	queueMouseDrag(h, point(4), 0)
	h.frame()
	if !waitText(t, p, "drag-highlight", 3*time.Second) {
		t.Fatalf("TUI did not repaint during drag: %s", screenText(p))
	}
	p.Screen(func(s *term.Screen) {
		frame, until := s.DisplayFrame(time.Now())
		if !until.IsZero() {
			t.Fatal("TUI completed its update but an old frame is still displayed")
		}
		line := frame.AbsLine(1)
		for col := 0; col < 26; col++ {
			want := term.DefaultColor
			if col >= 2 && col <= 9 {
				want = term.Indexed(4)
			}
			if line[col].A.BG != want {
				t.Fatalf("background at column %d = %+v, want %+v", col, line[col].A.BG, want)
			}
		}
	})
	if _, _, _, _, active := p.SelectionRange(); active {
		t.Fatal("fixture highlight was replaced by a local selection")
	}
	h.mouse(pointer.Release, point(9), 0)
	if !waitText(t, p, "release-highlight", 3*time.Second) {
		t.Fatalf("TUI did not receive release: %s", screenText(p))
	}
}

func runMouseSelectionFixture(t *testing.T) {
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	if err := windows.SetConsoleMode(in, windows.ENABLE_VIRTUAL_TERMINAL_INPUT|windows.ENABLE_MOUSE_INPUT|windows.ENABLE_EXTENDED_FLAGS); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(out, windows.ENABLE_PROCESSED_OUTPUT|windows.ENABLE_WRAP_AT_EOL_OUTPUT|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("\x1b[?1049h\x1b[?1002;1006h\x1b[?25l\x1b[2J\x1b[Hmouse-fixture-ready\x1b[2;1Habcdefghijklmnopqrstuvwxyz")
	var input [512]uint16
	var pending string
	anchor := 0
	for {
		var n uint32
		if err := windows.ReadConsole(in, &input[0], uint32(len(input)), &n, nil); err != nil || n == 0 {
			t.Fatalf("reading mouse input: %v", err)
		}
		pending += string(utf16.Decode(input[:n]))
		for {
			end := strings.IndexAny(pending, "Mm")
			if end < 0 {
				break
			}
			report := pending[:end+1]
			pending = pending[end+1:]
			var button, col, row int
			if _, err := fmt.Sscanf(report, "\x1b[<%d;%d;%d", &button, &col, &row); err != nil {
				t.Fatalf("mouse report %q: %v", report, err)
			}
			col = max(0, min(25, col-1))
			if button == 0 && report[end] == 'M' {
				anchor = col
				continue
			}
			text := "abcdefghijklmnopqrstuvwxyz"
			start, stop := min(anchor, col), max(anchor, col)+1
			status := "drag-highlight"
			if report[end] == 'm' {
				status = "release-highlight"
			}
			_, _ = fmt.Fprintf(os.Stdout, "\x1b[?2026h\x1b[2;1H%s\x1b[44m%s\x1b[0m%s\x1b[3;1H%s\x1b[K\x1b[?2026l",
				text[:start], text[start:stop], text[stop:], status)
		}
	}
}
