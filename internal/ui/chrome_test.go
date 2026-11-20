package ui

import (
	"image"
	"testing"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"gon/internal/config"
)

// renderChrome lays out the app chrome without any panes and returns the image.
func renderChrome(t *testing.T, w, h int) *image.RGBA {
	t.Helper()
	cfg := config.Default()
	a := New(cfg, nil, "")
	a.started = true // skip starting shells
	a.lastSize = image.Pt(w, h)

	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Skipf("headless GPU unavailable: %v", err)
	}
	defer win.Release()

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(w, h)),
	}
	a.layout(gtx)
	if err := win.Frame(&ops); err != nil {
		t.Fatalf("frame: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	return img
}

func TestChromeRendersDark(t *testing.T) {
	const w, h = 900, 600
	img := renderChrome(t, w, h)

	// The window must not be left at the clear colour (white).
	white := 0
	for y := 0; y < h; y += 7 {
		for x := 0; x < w; x += 7 {
			c := img.RGBAAt(x, y)
			if c.R > 240 && c.G > 240 && c.B > 240 {
				white++
			}
		}
	}
	if white > 10 {
		t.Fatalf("%d sampled pixels are pure white; chrome did not paint", white)
	}

	// The title bar and the sidebar should be darker than the wallpaper area.
	title := img.RGBAAt(w/2, 10)
	if title.R > 90 || title.G > 90 || title.B > 90 {
		t.Fatalf("title bar too bright: %v", title)
	}
}

func TestChromeSharesBackdropAcrossColumns(t *testing.T) {
	const w, h = 900, 600
	img := renderChrome(t, w, h)
	side := img.RGBAAt(20, h/2)
	content := img.RGBAAt(w-40, h/2)
	if side != content {
		t.Fatalf("sidebar %v and content %v differ over the shared backdrop", side, content)
	}
}

func TestCloseConfirmationWithOpenTabs(t *testing.T) {
	a := New(config.Default(), nil, "")
	t.Cleanup(a.shutdown)
	a.started = true
	a.handleWindowClosing(new(app.ClosingEvent))
	if a.closeDialog.open {
		t.Fatal("an empty window opened the confirmation")
	}

	a.tabs = []*Tab{{Title: "PowerShell"}, {Title: "Command Prompt"}}
	var router input.Router
	var ops op.Ops
	frame := func() {
		ops.Reset()
		a.layout(layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(800, 600)),
		})
		router.Frame(&ops)
	}
	a.handleWindowClosing(new(app.ClosingEvent))
	frame()
	if !a.closeDialog.open || !router.Source().Focused(&a.closeDialog.cancel) {
		t.Fatal("the confirmation did not focus Cancel")
	}
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	frame()
	if a.closeDialog.open || a.closeDialog.approved || len(a.tabs) != 2 {
		t.Fatal("canceling the confirmation changed the tabs")
	}

	a.handleWindowClosing(new(app.ClosingEvent))
	frame()
	router.Queue(key.Event{Name: key.NameTab, State: key.Press})
	frame()
	if !router.Source().Focused(&a.closeDialog.confirm) {
		t.Fatal("Tab did not focus Close window")
	}
	router.Queue(
		key.Event{Name: key.NameReturn, State: key.Press},
		key.Event{Name: key.NameReturn, State: key.Release},
	)
	frame()
	if !a.closeDialog.approved {
		t.Fatal("the Close window button did not confirm the close")
	}
}
