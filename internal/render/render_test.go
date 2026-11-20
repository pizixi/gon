package render

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"

	"gon/internal/fontset"
	"gon/internal/term"
)

func testView(t *testing.T) *View {
	t.Helper()
	fam := fontset.LoadFamily("Consolas", nil)
	if fam == nil || fam.Nominal() == nil {
		fam = fontset.LoadFamily("", nil)
	}
	if fam == nil || fam.Nominal() == nil {
		t.Skip("no usable font available")
	}
	v := NewView(fam, 16)
	v.Pal = term.NewPalette(0xe0e0e0, 0x101010)
	return v
}

func TestMetricsAreSane(t *testing.T) {
	v := testView(t)
	m := v.Metrics()
	if m.CellW < 4 || m.CellH < 8 {
		t.Fatalf("bad metrics %+v", m)
	}
	if m.Baseline <= 0 || m.Baseline >= m.CellH {
		t.Fatalf("bad baseline %+v", m)
	}
	if cols, rows := v.GridFrom(800, 600); cols < 10 || rows < 5 {
		t.Fatalf("grid %dx%d", cols, rows)
	}
}

func TestAtlasRasterizesGlyph(t *testing.T) {
	v := testView(t)
	face := v.Family.Pick('M', false, false)
	if face == nil {
		t.Skip("no face")
	}
	gid, ok := face.NominalGlyph('M')
	if !ok {
		t.Skip("no glyph for M")
	}
	key := Key{Face: face.Path, GID: gid, PPEM: 16, RGB: 0xffffff}
	sp := v.Atlas.Lookup(key, face)
	if !sp.OK || sp.R.Empty() {
		t.Fatalf("sprite not produced: %+v", sp)
	}
	if got := sp.page.imageOp().Size(); got != sp.R.Max {
		t.Fatalf("texture size = %v, want occupied area %v", got, sp.R.Max)
	}
	data, gen := v.Atlas.Snapshot()
	if gen == 0 {
		t.Fatal("zero generation")
	}
	// The sprite rectangle must contain non-zero coverage.
	nonZero := 0
	linearEdges := false
	for y := sp.R.Min.Y; y < sp.R.Max.Y; y++ {
		for x := sp.R.Min.X; x < sp.R.Max.X; x++ {
			i := (y*sp.page.side + x) * 4
			if data[i+3] != 0 {
				nonZero++
			}
			if data[i+3] > 0 && data[i+3] < 255 && data[i] > data[i+3] {
				linearEdges = true
			}
		}
	}
	if nonZero == 0 {
		t.Fatal("sprite has no opaque pixels")
	}
	if !linearEdges {
		t.Fatal("the glyph edge lost coverage when sampled as sRGB")
	}
}

func TestAtlasCachesAndResets(t *testing.T) {
	v := testView(t)
	face := v.Family.Pick('A', false, false)
	if face == nil {
		t.Skip("no face")
	}
	gid, ok := face.NominalGlyph('A')
	if !ok {
		t.Skip("no glyph")
	}
	key := Key{Face: face.Path, GID: gid, PPEM: 14, RGB: 0xff0000}
	first := v.Atlas.Lookup(key, face)
	_, gen1 := v.Atlas.Snapshot()
	second := v.Atlas.Lookup(key, face)
	_, gen2 := v.Atlas.Snapshot()
	if gen1 != gen2 {
		t.Fatal("cache hit regenerated the atlas")
	}
	if first.R != second.R {
		t.Fatal("cache hit moved the sprite")
	}
	v.Atlas.Reset()
	_, gen3 := v.Atlas.Snapshot()
	if gen3 == gen2 {
		t.Fatal("reset did not bump the generation")
	}
	if got := first.page.imageOp().Size(); got != first.R.Max {
		t.Fatalf("reset invalidated a queued sprite: texture size %v", got)
	}
}

func TestAtlasSharesMasksAcrossColorsAndEvictsOldPages(t *testing.T) {
	v := testView(t)
	face := v.Family.Pick('M', false, false)
	gid, ok := face.NominalGlyph('M')
	if !ok {
		t.Skip("no glyph for M")
	}
	key := Key{Face: face.Path, FaceIndex: face.Index, GID: gid, PPEM: 96}
	first := v.Atlas.Lookup(key, face)
	first.page.imageOp()
	for i := range 1600 {
		key.RGB = uint32(i + 1)
		if sprite := v.Atlas.Lookup(key, face); !sprite.OK {
			t.Fatalf("colour %d had no sprite", i)
		}
	}
	if n := len(v.Atlas.masks); n != 1 {
		t.Fatalf("colour variants allocated %d masks, want 1", n)
	}
	if v.Atlas.pixels > atlasPixels || first.page == v.Atlas.pages[0] {
		t.Fatal("old pages were not evicted within the atlas budget")
	}
	if first.page.imageOp().Size().X == 0 {
		t.Fatal("eviction invalidated a texture already used by a frame")
	}
}

func TestFitShrinksText(t *testing.T) {
	v := testView(t)
	txt := v.TextRenderer()
	s := "a very long title that will not fit"
	fitted := txt.Fit(s, 14, 60)
	if txt.Measure(fitted, 14) > 60 {
		t.Fatalf("fitted text too wide: %q", fitted)
	}
	if fitted == s {
		t.Fatal("expected the text to be shortened")
	}
}

func TestCursorStopsSchedulingFramesWhenIdle(t *testing.T) {
	v := testView(t)
	v.CursorBlinkMS = 500
	scr := term.NewScreen(80, 24, 0)
	var ops op.Ops
	var router input.Router
	for _, elapsed := range []time.Duration{100 * time.Millisecond, 6 * time.Second} {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Source: router.Source(), Now: v.cursorEpoch.Add(elapsed)}
		v.DrawRect(gtx, scr, 640, 400, 0, Selection{}, 0)
		router.Frame(&ops)
		got, scheduled := router.WakeupTime()
		if elapsed < time.Second {
			if want := v.cursorEpoch.Add(500 * time.Millisecond); !scheduled || !got.Equal(want) {
				t.Fatalf("next blink = %v (%v), want %v", got, scheduled, want)
			}
		} else if scheduled {
			t.Fatalf("idle cursor scheduled a frame at %v", got)
		}
	}
}

func TestSelectionGeometry(t *testing.T) {
	sel := Selection{L0: 2, C0: 3, L1: 4, C1: 5, Active: true}
	if !sel.Active || sel.L0 != 2 || sel.C1 != 5 {
		t.Fatal("selection fields mangled")
	}
	_ = image.Rect(0, 0, 1, 1)
}
