package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"gon/internal/config"
)

func TestWallpaperBlurChangesGradually(t *testing.T) {
	var w wallpaper
	start := time.Unix(1, 0)
	if got := w.blurAt(start, 0); got != 0 {
		t.Fatalf("initial blur = %v", got)
	}
	targetAt := start.Add(time.Second)
	if got := w.blurAt(targetAt, 96); got != 0 {
		t.Fatalf("blur jumped to %v", got)
	}
	if got := w.blurAt(targetAt.Add(wallpaperBlurDuration/2), 96); got <= 0 || got >= 96 {
		t.Fatalf("blur should be in transition, got %v", got)
	}
	if got := w.blurAt(targetAt.Add(wallpaperBlurDuration), 96); got != 96 {
		t.Fatalf("settled blur = %v, want 96", got)
	}
}

func TestWallpaperCoversWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wall.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 0x20, G: 0x90, B: 0x40, A: 0xff})
		}
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg := config.Default()
	cfg.Wallpaper = path
	cfg.Window.Blur = 0
	a := New(cfg, nil, "")
	a.started = true
	defer a.shutdown()
	tab := new(Tab)
	tab.addPane(newInputPane(), SplitRight)
	tab.addPane(newInputPane(), SplitRight)
	tab.addPane(newInputPane(), SplitDown)
	a.tabs = []*Tab{tab}

	const w, h = 400, 300
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
	// The dimmed wallpaper should be continuous across the title bar and panes.
	c := img.RGBAAt(20, h/2)
	if c.G <= c.R || c.G <= c.B {
		t.Fatalf("wallpaper not visible: %v", c)
	}
	for _, pt := range []image.Point{{20, 10}, {w / 2, a.hdrH - 1}, {240, h / 2}, {360, h - 30}} {
		if got := img.RGBAAt(pt.X, pt.Y); got != c {
			t.Errorf("wallpaper at %v = %v, want %v", pt, got, c)
		}
	}
}

func TestWallpaperMissingFileFallsBack(t *testing.T) {
	cfg := config.Default()
	cfg.Wallpaper = filepath.Join(t.TempDir(), "does-not-exist.png")
	a := New(cfg, nil, "")
	a.started = true

	const w, h = 200, 150
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
	a.paintBackground(gtx, image.Pt(w, h))
	if err := win.Frame(&ops); err != nil {
		t.Fatalf("frame: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	c := img.RGBAAt(w/2, h/2)
	if c.R > 60 || c.G > 60 || c.B > 60 {
		t.Fatalf("expected a dark gradient fallback, got %v", c)
	}
}
