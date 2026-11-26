package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"gon/internal/config"
	"gon/internal/term"
)

// TestSaveScreenshot renders a live app frame with a real shell to a PNG. It is
// a manual inspection aid: set GON_SHOT to the output path to enable it.
func TestSaveScreenshot(t *testing.T) {
	out := os.Getenv("GON_SHOT")
	if out == "" {
		t.Skip("set GON_SHOT to render a screenshot")
	}
	cfg := config.Default()
	cfg.Theme.Palette = nil
	if wp := os.Getenv("GON_WALLPAPER"); wp != "" {
		cfg.Wallpaper = wp
	}
	if n := os.Getenv("GON_TABS"); n != "" {
		if v, err := strconv.Atoi(n); err == nil {
			cfg.Tabs = v
		}
	}
	if o := os.Getenv("GON_OPACITY"); o != "" {
		if f, err := strconv.ParseFloat(o, 64); err == nil {
			cfg.Window.Opacity = f
		}
	}
	a := New(cfg, nil, "")

	const w, h = 1180, 760
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Skipf("headless GPU unavailable: %v", err)
	}
	defer win.Release()

	t.Logf("opacity=%v bgalpha=%d", cfg.Window.Opacity, a.view.BgAlpha)
	a.started = true
	a.lastSize = image.Pt(w, h)
	if os.Getenv("GON_SPLIT") != "" {
		if err := a.StartTabs(); err != nil {
			t.Fatalf("StartTabs: %v", err)
		}
		a.SplitActive(SplitRight)
		a.SplitActive(SplitDown)
		a.SplitActive(SplitDown)
		a.activeTab().Focus = 1
		time.Sleep(1500 * time.Millisecond)
	}
	if os.Getenv("GON_SPLIT") == "" {
		if err := a.StartTabs(); err != nil {
			t.Fatalf("StartTabs: %v", err)
		}
	}
	defer a.shutdown()

	// Wait for the shell to print its prompt.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var txt string
		if p := a.currentPane(); p != nil {
			p.Screen(func(scr *term.Screen) {
				for y := 0; y < scr.Rows; y++ {
					txt += scr.Text(scr.ScrollbackLen() + y)
				}
			})
		}
		if len(strings.TrimSpace(txt)) > 0 {
			t.Logf("screen text: %q", strings.TrimSpace(txt))
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if os.Getenv("GON_SCROLL") != "" {
		if p := a.currentPane(); p != nil {
			cmd := "1..60 | ForEach-Object { 'line ' + $_ + '  the quick brown fox jumps over the lazy dog' }"
			p.SendTextInput(cmd + "\r")
			time.Sleep(1200 * time.Millisecond)
			p.ScrollBy(20)
			time.Sleep(150 * time.Millisecond)
		}
	}

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(w, h)),
	}
	if os.Getenv("GON_DEMO") != "" {
		if p := a.currentPane(); p != nil {
			script := filepath.Join(os.TempDir(), "gon-demo.ps1")
			body := "1..14 | % { '{0}[3{1}mline {2,-2}{0}[0m the quick brown fox {0}[1;9{1}mjumps{0}[0m over the lazy dog' -f [char]27,($_%7),$_ }"
			_ = os.WriteFile(script, []byte(body), 0o644)
			p.SendTextInput("& '" + script + "'" + "\r")
			time.Sleep(1400 * time.Millisecond)
		}
	}

	if os.Getenv("GON_MENU") != "" {
		a.menuOpen = true
	}
	if q := os.Getenv("GON_SEARCH"); q != "" {
		a.openSearch()
		a.search.editor.SetText(q)
		a.setSearchQuery(q)
	}
	a.layout(gtx)
	if err := win.Frame(&ops); err != nil {
		t.Fatalf("frame: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	t.Logf("wrote %s", out)
}
