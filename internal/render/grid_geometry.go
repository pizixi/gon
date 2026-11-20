package render

import (
	"image"

	"gioui.org/f32"
)

// Grid specifies the cell geometry shared by drawing and hit testing.
type Grid struct {
	Bounds     image.Rectangle
	Cols, Rows int
	Metrics    Metrics
}

func (v *View) Grid(bounds image.Rectangle) Grid {
	cols, rows := v.GridFrom(bounds.Dx(), bounds.Dy())
	return Grid{Bounds: bounds, Cols: cols, Rows: rows, Metrics: v.metrics}
}

// Hit tests a position in the grid's local coordinate space.
func (g Grid) Hit(pos f32.Point, clamp bool) (cell, pixel image.Point, ok bool) {
	if g.Bounds.Empty() || g.Metrics.CellW <= 0 || g.Metrics.CellH <= 0 {
		return image.Point{}, image.Point{}, false
	}
	w := min(g.Bounds.Dx(), g.Cols*g.Metrics.CellW)
	h := min(g.Bounds.Dy(), g.Rows*g.Metrics.CellH)
	if !clamp && (pos.X < 0 || pos.Y < 0 || pos.X >= float32(w) || pos.Y >= float32(h)) {
		return image.Point{}, image.Point{}, false
	}
	pixel = image.Pt(max(0, min(w-1, int(pos.X))), max(0, min(h-1, int(pos.Y))))
	return image.Pt(pixel.X/g.Metrics.CellW, pixel.Y/g.Metrics.CellH), pixel, true
}
