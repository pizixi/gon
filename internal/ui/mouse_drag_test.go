package ui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/unit"

	"gon/internal/config"
	"gon/internal/term"
)

func mouseDragTest(t *testing.T, modes string) (*layoutTest, *Pane, func(int, int) image.Point) {
	t.Helper()
	a := New(config.Default(), nil, "")
	a.started = true
	t.Cleanup(a.shutdown)
	tb := new(Tab)
	p := newInputPane()
	tb.addPane(newInputPane(), SplitRight)
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(1280, 720), metric: unit.Metric{PxPerDp: 1.5, PxPerSp: 1.5}}
	h.frame()
	p.parser = term.NewParser(p.scr)
	p.applyOutput([]byte("\x1b[?" + modes + "h"))
	h.frame()
	grid := p.presented.grid
	point := func(col, row int) image.Point {
		m := grid.Metrics
		return grid.Bounds.Min.Add(image.Pt(col*m.CellW+m.CellW/2, row*m.CellH+m.CellH/2))
	}
	return h, p, point
}

func queueMouseDrag(h *layoutTest, at image.Point, mods key.Modifiers) {
	h.router.Queue(pointer.Event{
		Source: pointer.Mouse, Kind: pointer.Move, Buttons: pointer.ButtonPrimary,
		Position: f32.Pt(float32(at.X), float32(at.Y)), Modifiers: mods,
	})
}

func TestTUIMouseDragUsesLatestPositionWithoutBacklog(t *testing.T) {
	for _, tc := range []struct {
		name, modes    string
		pixels, native bool
	}{
		{name: "drag tracking", modes: "1002;1006", native: true},
		{name: "all motion tracking", modes: "1003;1006", native: true},
		{name: "pixel tracking", modes: "1002;1016", pixels: true, native: true},
		{name: "native cursor unavailable", modes: "1002;1006"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, p, point := mouseDragTest(t, tc.modes)
			h.selectionCursor = func() (image.Point, bool) { return point(9, 3), tc.native }
			h.mouse(pointer.Press, point(2, 1), pointer.ButtonPrimary)
			takeWrite(t, p)
			// More events than the input pipe queue can hold. Only the final
			// caret should reach the application, leaving room for a release.
			for i := range 64 {
				queueMouseDrag(h, point(3+i%4, 2), 0)
			}
			h.frame()
			col, row := 9, 3
			if !tc.native {
				col, row = 6, 2
			}
			x, y := col+1, row+1
			if tc.pixels {
				local := point(col, row).Sub(p.presented.grid.Bounds.Min)
				x, y = local.X+1, local.Y+1
			}
			if got, want := takeWrite(t, p), fmt.Sprintf("\x1b[<32;%d;%dM", x, y); got != want {
				t.Fatalf("drag report = %q, want %q", got, want)
			}
			if len(p.writeQ) != 0 {
				t.Fatal("stale drag reports were queued")
			}
			if _, _, _, _, selected := p.SelectionRange(); selected || h.selecting == p {
				t.Fatal("application drag started a Gon selection")
			}
			if !p.mouseInputRecent(time.Now()) {
				t.Fatal("drag response would use the normal output delay")
			}
			h.mouse(pointer.Release, point(col, row), 0)
			if got, want := takeWrite(t, p), fmt.Sprintf("\x1b[<0;%d;%dm", x, y); got != want {
				t.Fatalf("release report = %q, want %q", got, want)
			}
		})
	}
}

func TestTUIMouseDragKeepsInputBoundaries(t *testing.T) {
	for _, finish := range []pointer.Kind{pointer.Release, pointer.Cancel} {
		t.Run(finish.String(), func(t *testing.T) {
			h, p, point := mouseDragTest(t, "1002;1006")
			samples := 0
			h.selectionCursor = func() (image.Point, bool) {
				samples++
				return point(20, 10), true
			}
			h.mouse(pointer.Press, point(2, 1), pointer.ButtonPrimary)
			takeWrite(t, p)
			queueMouseDrag(h, point(3, 2), 0)
			queueMouseDrag(h, point(4, 2), 0)
			h.router.Queue(key.EditEvent{Text: "x"})
			queueMouseDrag(h, point(5, 2), key.ModCtrl)
			queueMouseDrag(h, point(6, 2), key.ModAlt)
			h.router.Queue(pointer.Event{Kind: finish, Position: f32.Pt(float32(point(7, 2).X), float32(point(7, 2).Y))})
			h.frame()
			last := "\x1b[<0;8;3m"
			if finish == pointer.Cancel {
				last = "\x1b[<0;7;3m"
			}
			for _, want := range []string{"\x1b[<32;5;3M", "x", "\x1b[<48;6;3M", "\x1b[<40;7;3M", last} {
				if got := takeWrite(t, p); got != want {
					t.Fatalf("input report = %q, want %q", got, want)
				}
			}
			if samples != 0 || p.mouseButton != 0 || len(p.writeQ) != 0 {
				t.Fatalf("samples=%d button=%d pending=%d after gesture ended", samples, p.mouseButton, len(p.writeQ))
			}
		})
	}
}

func TestShiftSelectionInTUIUsesLatestCursorLocally(t *testing.T) {
	h, p, point := mouseDragTest(t, "1002;1006")
	p.applyOutput([]byte("\x1b[2;1Ha b c d e f"))
	h.frame()
	h.selectionCursor = func() (image.Point, bool) { return point(7, 1), true }
	h.router.Queue(pointer.Event{
		Kind: pointer.Press, Buttons: pointer.ButtonPrimary, Modifiers: key.ModShift,
		Position: f32.Pt(float32(point(2, 1).X), float32(point(2, 1).Y)),
	})
	h.frame()
	queueMouseDrag(h, point(4, 1), key.ModShift)
	h.frame()
	if got := p.SelectedText(); got != "b c d" {
		t.Fatalf("local selection = %q, want b c d", got)
	}
	if len(p.writeQ) != 0 {
		t.Fatal("Shift selection sent mouse input to the TUI")
	}
}

func TestMouseResponseSkipsOutputBurstDelay(t *testing.T) {
	a := &App{win: new(app.Window)}
	p := newInputPane()
	p.visible.Store(true)
	p.mouseInputUntil.Store(time.Now().Add(time.Minute).UnixNano())
	a.scheduleOutputFrame(p)
	if a.outputPending.Load() {
		t.Fatal("mouse response was queued behind the output burst timer")
	}
	p.mouseInputUntil.Store(time.Now().Add(-time.Second).UnixNano())
	a.scheduleOutputFrame(p)
	if !a.outputPending.Load() {
		t.Fatal("ordinary output no longer uses burst coalescing")
	}
}
