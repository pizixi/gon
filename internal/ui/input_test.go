package ui

import (
	"image"
	"io"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"gon/internal/config"
	"gon/internal/term"
)

// newInputPane builds a pane with a live input queue but no shell.
func newInputPane() *Pane {
	return &Pane{
		scr:    term.NewScreen(80, 24, 1000),
		cols:   80,
		rows:   24,
		writeQ: make(chan []byte, 16),
	}
}

func takeWrite(t *testing.T, p *Pane) string {
	t.Helper()
	select {
	case b := <-p.writeQ:
		return string(b)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no bytes were written to the shell")
		return ""
	}
}

func TestPaneForwardsTrackedMouseInSplit(t *testing.T) {
	var router input.Router
	var ops op.Ops
	a := New(config.Default(), nil, "")
	p := newInputPane()
	parser := term.NewParser(p.scr)
	parser.Write([]byte("\x1b[?1003;1006h"))
	metrics := a.view.Metrics()
	body := image.Rect(240, 60, 240+24*metrics.CellW+2*a.paneInsetPx, 60+10*metrics.CellH)
	area := a.terminalArea(body)
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		a.paneInput(gtx, p, body, true)
		router.Frame(&ops)
	}
	position := func(col, row int) f32.Point {
		return f32.Pt(float32(area.Min.X+col*metrics.CellW+1), float32(area.Min.Y+row*metrics.CellH+1))
	}
	frame()
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Press, Buttons: pointer.ButtonPrimary, Position: position(3, 2)})
	frame()
	if got := takeWrite(t, p); got != "\x1b[<0;4;3M" {
		t.Fatalf("press = %q", got)
	}
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Move, Buttons: pointer.ButtonPrimary, Position: position(5, 3)})
	frame()
	if got := takeWrite(t, p); got != "\x1b[<32;6;4M" {
		t.Fatalf("drag = %q", got)
	}
	router.Queue(pointer.Event{Kind: pointer.Cancel})
	frame()
	if got := takeWrite(t, p); got != "\x1b[<0;6;4m" {
		t.Fatalf("canceled press release = %q", got)
	}
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Press, Buttons: pointer.ButtonPrimary, Position: position(3, 2)})
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Release, Position: position(26, 3)})
	frame()
	if press, release := takeWrite(t, p), takeWrite(t, p); press != "\x1b[<0;4;3M" || release != "\x1b[<0;24;4m" {
		t.Fatalf("press and release outside pane = %q / %q", press, release)
	}
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Scroll, Position: position(3, 2), Scroll: f32.Pt(0, -120)})
	frame()
	if got := takeWrite(t, p); got != "\x1b[<64;4;3M" {
		t.Fatalf("wheel = %q", got)
	}

	for range p.rows + 2 {
		parser.Write([]byte("scrollback\r\n"))
	}
	p.ScrollBy(1)
	router.Queue(pointer.Event{Source: pointer.Mouse, Kind: pointer.Press, Buttons: pointer.ButtonPrimary, Position: position(3, 2)})
	frame()
	if len(p.writeQ) != 0 || a.selecting != p {
		t.Fatal("a press in scrollback should start a selection")
	}
}

// TestPaneInputFocusAndTyping checks that the pane takes keyboard focus and
// forwards typed text.
func TestPaneInputFocusAndTyping(t *testing.T) {
	var router input.Router
	var ops op.Ops
	a := New(config.Default(), nil, "")
	p := newInputPane()

	ctx := func() layout.Context {
		return layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(800, 600)),
		}
	}
	area := image.Rect(0, 0, 700, 400)

	// First frame: the pane registers its tag and requests focus.
	a.paneInput(ctx(), p, area, true)
	router.Frame(&ops)

	ops.Reset()
	router.Queue(key.EditEvent{Text: "hello"})
	a.paneInput(ctx(), p, area, true)
	router.Frame(&ops)

	if got := takeWrite(t, p); got != "hello" {
		t.Fatalf("typed text = %q, want %q", got, "hello")
	}
}

// TestPaneInputArrowKeys checks that special keys are encoded for the shell.
func TestPaneInputArrowKeys(t *testing.T) {
	var router input.Router
	var ops op.Ops
	a := New(config.Default(), nil, "")
	p := newInputPane()

	ctx := func() layout.Context {
		return layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(800, 600)),
		}
	}
	area := image.Rect(0, 0, 700, 400)

	a.paneInput(ctx(), p, area, true)
	router.Frame(&ops)

	ops.Reset()
	router.Queue(key.Event{Name: key.NameLeftArrow, State: key.Press})
	a.paneInput(ctx(), p, area, true)
	router.Frame(&ops)

	if got := takeWrite(t, p); got != "\x1b[D" {
		t.Fatalf("left arrow = %q", got)
	}
}

// TestPaneInputNoDoubleTyping guards against Windows delivering both a key
// event and an edit event for the same printable character.
func TestPaneInputNoDoubleTyping(t *testing.T) {
	var router input.Router
	var ops op.Ops
	a := New(config.Default(), nil, "")
	p := newInputPane()

	ctx := func() layout.Context {
		return layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(800, 600)),
		}
	}
	a.paneInput(ctx(), p, image.Rect(0, 0, 700, 400), true)
	router.Frame(&ops)

	ops.Reset()
	router.Queue(
		key.Event{Name: "a", State: key.Press},
		key.EditEvent{Text: "a"},
		key.Event{Name: key.NameSpace, State: key.Press},
		key.EditEvent{Text: " "},
	)
	a.paneInput(ctx(), p, image.Rect(0, 0, 700, 400), true)
	router.Frame(&ops)

	if got := takeWrite(t, p); got != "a" {
		t.Fatalf("first write = %q, want %q", got, "a")
	}
	if got := takeWrite(t, p); got != " " {
		t.Fatalf("second write = %q, want a space", got)
	}
	select {
	case extra := <-p.writeQ:
		t.Fatalf("unexpected extra write %q", extra)
	case <-time.After(60 * time.Millisecond):
	}
}

// TestPaneInputCtrlLetter checks that control chords still reach the shell.
func TestPaneInputCtrlLetter(t *testing.T) {
	var router input.Router
	var ops op.Ops
	a := New(config.Default(), nil, "")
	p := newInputPane()

	ctx := func() layout.Context {
		return layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(800, 600)),
		}
	}
	a.paneInput(ctx(), p, image.Rect(0, 0, 700, 400), true)
	router.Frame(&ops)

	ops.Reset()
	router.Queue(key.Event{Name: "c", Modifiers: key.ModCtrl, State: key.Press})
	a.paneInput(ctx(), p, image.Rect(0, 0, 700, 400), true)
	router.Frame(&ops)

	if got := takeWrite(t, p); got != "\x03" {
		t.Fatalf("ctrl+c = %q, want ^C", got)
	}
}

func TestTerminalClipboardPaste(t *testing.T) {
	a := New(config.Default(), nil, "")
	defer a.shutdown()
	a.started = true
	pane, other := newInputPane(), newInputPane()
	parser := term.NewParser(pane.scr)
	first, second := &Tab{Title: "First"}, &Tab{Title: "Second"}
	first.addPane(pane, SplitRight)
	second.addPane(other, SplitRight)
	a.tabs = []*Tab{first, second}
	h := &layoutTest{App: a, size: image.Pt(700, 400)}

	for _, tc := range []struct {
		name, text, want     string
		bracketed, switchTab bool
	}{
		{name: "bracketed text", text: "一\r\n二", want: "\x1b[200~一\n二\x1b[201~", bracketed: true},
		{name: "empty bracketed paste", want: "\x1b[200~\x1b[201~", bracketed: true, switchTab: true},
		{name: "plain text", text: "a\r\nb", want: "a\nb"},
		{name: "empty plain paste"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := "\x1b[?2004l"
			if tc.bracketed {
				mode = "\x1b[?2004h"
			}
			parser.Write([]byte(mode))
			a.activateTab(0)
			h.frame()
			h.router.Queue(key.Event{Name: "V", Modifiers: key.ModCtrl, State: key.Press})
			h.frame()
			if !h.router.ClipboardRequested() {
				t.Fatal("Ctrl+V did not request clipboard data")
			}
			if tc.switchTab {
				a.activateTab(1)
				h.frame()
			}
			h.router.Queue(transfer.DataEvent{
				Type: "application/text",
				Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader(tc.text)) },
			})
			h.frame()

			if tc.want != "" {
				if got := takeWrite(t, pane); got != tc.want {
					t.Fatalf("paste = %q, want %q", got, tc.want)
				}
			}
			if len(other.writeQ) != 0 || len(pane.writeQ) != 0 {
				t.Fatal("unexpected extra paste input")
			}
		})
	}
}

func TestClipboardImageUsesNativeShortcut(t *testing.T) {
	pane := newInputPane()
	term.NewParser(pane.scr).Write([]byte("\x1b[?2004h"))
	pane.pasteClipboard("", true)
	if got := takeWrite(t, pane); got != "\x1bv" {
		t.Fatalf("image paste shortcut = %q, want Alt+V", got)
	}
}
