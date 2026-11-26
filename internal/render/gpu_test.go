package render

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"gon/internal/term"
)

// drawHeadless renders fn into an offscreen surface and returns the image.
func drawHeadless(t *testing.T, w, h int, fn func(gtx layout.Context)) *image.RGBA {
	t.Helper()
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
	fn(gtx)
	if err := win.Frame(&ops); err != nil {
		t.Fatalf("frame: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	return img
}

// toRGBA converts an opaque NRGBA to the premultiplied RGBA read back from the
// GPU surface.
func toRGBA(c color.NRGBA) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

func TestDrawRectProducesPixels(t *testing.T) {
	v := testView(t)
	scr := term.NewScreen(40, 10, 100)
	term.NewParser(scr).Write([]byte("\x1b[31mhello\x1b[0m world\r\nsecond line"))

	const w, h = 400, 200
	img := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
	})

	// A pixel in an empty row inside the grid is the terminal background.
	m := v.Metrics()
	bg := img.RGBAAt(m.CellW*20+2, m.CellH*5+2)
	want := v.Pal.Bg
	if bg.R != uint8(want>>16) || bg.G != uint8(want>>8) || bg.B != uint8(want) {
		t.Fatalf("background = %v, want #%06x", bg, want)
	}

	// Some pixels in the first text row must differ from the background.
	diff := 0
	for y := 0; y < m.CellH; y++ {
		for x := 0; x < 8*m.CellW; x++ {
			if c := img.RGBAAt(x, y); c != bg {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("no glyph pixels rendered in the first row")
	}
}

func TestTerminalGridGlyphsMeetAtCellEdges(t *testing.T) {
	v := testView(t)
	v.ShowCursor = false
	m := v.Metrics()
	scr := term.NewScreen(3, 2, 0)
	term.NewParser(scr).Write([]byte("\x1b]10;#abcdef\x07\x1b]11;#102030\x07│──\r\n│▀"))
	w, h := 3*m.CellW, 2*m.CellH
	img := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
	})
	fg := toRGBA(rgb(0xabcdef))
	for _, point := range []image.Point{
		image.Pt(m.CellW/2, m.CellH-1), image.Pt(m.CellW/2, m.CellH),
		image.Pt(2*m.CellW-1, m.CellH/2), image.Pt(2*m.CellW, m.CellH/2),
		image.Pt(2*m.CellW-1, m.CellH),
	} {
		if got := img.RGBAAt(point.X, point.Y); got != fg {
			t.Errorf("grid stroke at %v = %v, want %v", point, got, fg)
		}
	}
	if got := img.RGBAAt(w-1, h-1); got != toRGBA(rgb(0x102030)) {
		t.Errorf("terminal background = %v", got)
	}
}

func TestRoundedPowerlineSeparatorsMeetSegmentBackground(t *testing.T) {
	v := testView(t)
	defer v.Family.Close()
	v.ShowCursor = false
	for _, size := range []int{15, 23} {
		v.SetFontSize(size)
		m := v.Metrics()
		scr := term.NewScreen(6, 1, 0)
		term.NewParser(scr).Write([]byte("\x1b[38;2;152;87;164m \ue0b6\x1b[48;2;152;87;164m  \x1b[49m\ue0b4 "))
		w, h := 6*m.CellW, m.CellH
		img := drawHeadless(t, w, h, func(gtx layout.Context) {
			v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
		})

		segment := toRGBA(rgb(0x9857a4))
		for _, x := range []int{2*m.CellW - 1, 2 * m.CellW, 4*m.CellW - 1, 4 * m.CellW} {
			for y := 1; y < h-1; y++ {
				if got := img.RGBAAt(x, y); got != segment {
					t.Fatalf("at font size %d, join pixel (%d, %d) = %v, want %v", size, x, y, got, segment)
				}
			}
		}
		bg := toRGBA(rgb(v.Pal.Bg))
		if left := img.RGBAAt(m.CellW, 0); left != bg {
			t.Errorf("left cap corner at size %d = %v, want %v", size, left, bg)
		}
		if right := img.RGBAAt(5*m.CellW-1, h-1); right != bg {
			t.Errorf("right cap corner at size %d = %v, want %v", size, right, bg)
		}
	}
}

func TestMixedBoxDrawingMeetsAdjacentCells(t *testing.T) {
	v := testView(t)
	defer v.Family.Close()
	v.ShowCursor = false
	for _, size := range []int{15, 28} {
		v.SetFontSize(size)
		m := v.Metrics()
		s := term.NewScreen(4, 6, 0)
		term.NewParser(s).Write([]byte("╒══╕\r\n│  │\r\n╘══╛\r\n╓──╖\r\n║  ║\r\n╙──╜"))
		w, h := m.CellW, m.CellH
		img := drawHeadless(t, 4*w, 6*h, func(gtx layout.Context) {
			v.DrawRect(gtx, s, 4*w, 6*h, 0, Selection{}, 0)
		})
		fg := toRGBA(rgb(v.Pal.Fg))
		gap := max(1, w/9)
		for _, at := range []image.Point{
			image.Pt(w-1, h/2-gap), image.Pt(w, h/2-gap),
			image.Pt(w-1, h/2+gap), image.Pt(w, h/2+gap),
			image.Pt(w/2, h-1), image.Pt(w/2, h),
			image.Pt(w-1, 3*h+h/2), image.Pt(w, 3*h+h/2),
			image.Pt(w/2-gap, 4*h-1), image.Pt(w/2-gap, 4*h),
			image.Pt(w/2+gap, 4*h-1), image.Pt(w/2+gap, 4*h),
		} {
			if got := img.RGBAAt(at.X, at.Y); got != fg {
				t.Errorf("size %d: junction pixel %v = %v, want %v", size, at, got, fg)
			}
		}
		if got := img.RGBAAt(w-1, h/2); got != toRGBA(rgb(v.Pal.Bg)) {
			t.Errorf("size %d: double stroke lost its gap: %v", size, got)
		}
	}
}

func TestBlockCursorKeepsCombiningMarkOnSpace(t *testing.T) {
	v := testView(t)
	defer v.Family.Close()
	m := v.Metrics()
	s := term.NewScreen(1, 1, 0)
	p := term.NewParser(s)
	p.Write([]byte(" \u0301\r"))
	img := drawHeadless(t, m.CellW, m.CellH, func(gtx layout.Context) {
		v.DrawRect(gtx, s, m.CellW, m.CellH, 0, Selection{}, 0)
	})
	ink := toRGBA(rgb(v.CursorColor))
	mark := 0
	for y := range m.CellH {
		for x := range m.CellW {
			if img.RGBAAt(x, y) != ink {
				mark++
			}
		}
	}
	if mark == 0 || mark == m.CellW*m.CellH {
		t.Fatalf("block cursor rendered %d mark pixels", mark)
	}
}

func TestDrawRectScrollback(t *testing.T) {
	v := testView(t)
	scr := term.NewScreen(20, 3, 100)
	term.NewParser(scr).Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix"))

	const w, h = 300, 120
	img := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 3, Selection{}, 0)
	})
	if img == nil {
		t.Fatal("nil image")
	}
}

func TestDrawUIRendersText(t *testing.T) {
	v := testView(t)
	const w, h = 200, 60
	bg := color.NRGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff}
	img := drawHeadless(t, w, h, func(gtx layout.Context) {
		gtx.Constraints = layout.Exact(image.Pt(w, h))
		paint.FillShape(gtx.Ops, bg, clip.Rect(image.Rect(0, 0, w, h)).Op())
		v.DrawUI(gtx, "Hello", 4, 20, 16, 0xffffff, 0xff)
	})
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.RGBAAt(x, y) != toRGBA(bg) {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("UI text produced no pixels")
	}
}

func TestSelectionHighlight(t *testing.T) {
	v := testView(t)
	scr := term.NewScreen(20, 3, 100)
	term.NewParser(scr).Write([]byte("select this text"))

	const w, h = 300, 80
	plain := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
	})
	hl := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{L0: 0, C0: 0, L1: 0, C1: 6, Active: true}, 0x3f6ea8)
	})
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if plain.RGBAAt(x, y) != hl.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("selection highlight produced no pixels")
	}
}

func TestCursorShapes(t *testing.T) {
	v := testView(t)
	const w, h = 200, 60

	render := func(style int) *image.RGBA {
		scr := term.NewScreen(20, 3, 100)
		scr.CursorStyle = style
		term.NewParser(scr).Write([]byte("abc"))
		return drawHeadless(t, w, h, func(gtx layout.Context) {
			v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
		})
	}

	m := v.Metrics()
	bg := v.Pal.Bg
	bgPix := color.RGBA{R: uint8(bg >> 16), G: uint8(bg >> 8), B: uint8(bg), A: 0xff}

	bar := render(6) // steady vertical bar at cell 3
	x0 := 3 * m.CellW
	left := bar.RGBAAt(x0, m.CellH/2)
	right := bar.RGBAAt(x0+m.CellW-1, m.CellH/2)
	if left == bgPix {
		t.Fatal("bar cursor: left edge not painted")
	}
	if right != bgPix {
		t.Fatalf("bar cursor: right edge painted %v", right)
	}

	under := render(4) // steady underline
	bottom := under.RGBAAt(x0+m.CellW/2, m.CellH-2)
	top := under.RGBAAt(x0+m.CellW/2, 1)
	if bottom == bgPix {
		t.Fatal("underline cursor: bottom row not painted")
	}
	if top != bgPix {
		t.Fatalf("underline cursor: top row painted %v", top)
	}
}

func TestSelectionVisibleAtBottomWithScrollback(t *testing.T) {
	v := testView(t)
	scr := term.NewScreen(20, 3, 100)
	term.NewParser(scr).Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven"))
	sb := scr.ScrollbackLen()
	if sb == 0 {
		t.Fatal("expected scrollback")
	}
	// Select the first visible row (absolute line sb) with no scroll offset.
	const w, h = 300, 80
	plain := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{}, 0)
	})
	hl := drawHeadless(t, w, h, func(gtx layout.Context) {
		v.DrawRect(gtx, scr, w, h, 0, Selection{L0: sb, C0: 0, L1: sb, C1: 6, Active: true}, 0x3f6ea8)
	})
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if plain.RGBAAt(x, y) != hl.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("selection on the visible bottom row was not highlighted")
	}
}
