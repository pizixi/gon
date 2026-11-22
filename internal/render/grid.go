package render

import (
	"image"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

const (
	leftEdge = 1 << iota
	rightEdge
	topEdge
	bottomEdge
	heavyEdge
	roundEdge
)

var boxEdges = map[rune]uint8{
	'─': leftEdge | rightEdge, '━': leftEdge | rightEdge | heavyEdge,
	'│': topEdge | bottomEdge, '┃': topEdge | bottomEdge | heavyEdge,
	'┌': rightEdge | bottomEdge, '┏': rightEdge | bottomEdge | heavyEdge,
	'┐': leftEdge | bottomEdge, '┓': leftEdge | bottomEdge | heavyEdge,
	'└': rightEdge | topEdge, '┗': rightEdge | topEdge | heavyEdge,
	'┘': leftEdge | topEdge, '┛': leftEdge | topEdge | heavyEdge,
	'├': rightEdge | topEdge | bottomEdge, '┣': rightEdge | topEdge | bottomEdge | heavyEdge,
	'┤': leftEdge | topEdge | bottomEdge, '┫': leftEdge | topEdge | bottomEdge | heavyEdge,
	'┬': leftEdge | rightEdge | bottomEdge, '┳': leftEdge | rightEdge | bottomEdge | heavyEdge,
	'┴': leftEdge | rightEdge | topEdge, '┻': leftEdge | rightEdge | topEdge | heavyEdge,
	'┼': leftEdge | rightEdge | topEdge | bottomEdge, '╋': leftEdge | rightEdge | topEdge | bottomEdge | heavyEdge,
	'╭': rightEdge | bottomEdge | roundEdge, '╮': leftEdge | bottomEdge | roundEdge,
	'╰': rightEdge | topEdge | roundEdge, '╯': leftEdge | topEdge | roundEdge,
	'╴': leftEdge, '╵': topEdge, '╶': rightEdge, '╷': bottomEdge,
	'╸': leftEdge | heavyEdge, '╹': topEdge | heavyEdge, '╺': rightEdge | heavyEdge, '╻': bottomEdge | heavyEdge,
}

var doubleBoxEdges = map[rune]uint8{
	'═': leftEdge | rightEdge, '║': topEdge | bottomEdge,
	'╔': rightEdge | bottomEdge, '╗': leftEdge | bottomEdge,
	'╚': rightEdge | topEdge, '╝': leftEdge | topEdge,
	'╠': rightEdge | topEdge | bottomEdge, '╣': leftEdge | topEdge | bottomEdge,
	'╦': leftEdge | rightEdge | bottomEdge, '╩': leftEdge | rightEdge | topEdge,
	'╬': leftEdge | rightEdge | topEdge | bottomEdge,
}

// drawGridGlyph fits rules and blocks to cell edges, regardless of line height.
func drawGridGlyph(gtx layout.Context, r rune, cell image.Rectangle, color uint32) bool {
	if r >= '\ue0b0' && r <= '\ue0b3' {
		drawPowerline(gtx, r, cell, color)
		return true
	}
	if r < 0x2500 || r > 0x259f {
		return false
	}
	fill := func(rect image.Rectangle) {
		if !rect.Empty() {
			paint.FillShape(gtx.Ops, rgb(color), clip.Rect(rect).Op())
		}
	}
	x0, y0, x1, y1 := cell.Min.X, cell.Min.Y, cell.Max.X, cell.Max.Y
	w, h := cell.Dx(), cell.Dy()
	cx, cy := x0+w/2, y0+h/2
	light, heavy := max(1, w/9), max(2, w/5)
	if edges := doubleBoxEdges[r]; edges != 0 {
		lo, hi := light/2, (light+1)/2
		gap := max(1, light)
		for _, lane := range [2]int{-gap, gap} {
			x, y := cx+lane, cy+lane
			if (edges == leftEdge|bottomEdge) || (edges == rightEdge|topEdge) {
				x = cx - lane
			}
			if edges&leftEdge != 0 {
				fill(image.Rect(x0, y-lo, x+hi, y+hi))
			}
			if edges&rightEdge != 0 {
				fill(image.Rect(x-lo, y-lo, x1, y+hi))
			}
			if edges&topEdge != 0 {
				fill(image.Rect(x-lo, y0, x+hi, y+hi))
			}
			if edges&bottomEdge != 0 {
				fill(image.Rect(x-lo, y-lo, x+hi, y1))
			}
		}
		return true
	}
	if edges := boxEdges[r]; edges != 0 {
		thick := light
		if edges&heavyEdge != 0 {
			thick = heavy
		}
		lo, hi := thick/2, (thick+1)/2
		if edges&roundEdge != 0 {
			// A rounded corner joins a horizontal and a vertical rule.
			x, y := x0, y0
			if edges&rightEdge != 0 {
				x = x1
			}
			if edges&bottomEdge != 0 {
				y = y1
			}
			a, b := float32(cx-lo)+float32(thick)/2, float32(cy-lo)+float32(thick)/2
			dx, dy := float32(x)-a, float32(y)-b
			var path clip.Path
			path.Begin(gtx.Ops)
			path.MoveTo(f32.Pt(float32(x), b))
			path.LineTo(f32.Pt(a+dx/2, b))
			path.QuadTo(f32.Pt(a, b), f32.Pt(a, b+dy/2))
			path.LineTo(f32.Pt(a, float32(y)))
			paint.FillShape(gtx.Ops, rgb(color), clip.Stroke{Path: path.End(), Width: float32(thick)}.Op())
			return true
		}
		if edges&leftEdge != 0 {
			fill(image.Rect(x0, cy-lo, cx+hi, cy+hi))
		}
		if edges&rightEdge != 0 {
			fill(image.Rect(cx-lo, cy-lo, x1, cy+hi))
		}
		if edges&topEdge != 0 {
			fill(image.Rect(cx-lo, y0, cx+hi, cy+hi))
		}
		if edges&bottomEdge != 0 {
			fill(image.Rect(cx-lo, cy-lo, cx+hi, y1))
		}
		return true
	}
	if (r >= '┄' && r <= '┋') || (r >= '╌' && r <= '╏') {
		offset := int(r - '┄')
		count := 3 + offset/4
		if r >= '╌' {
			offset, count = int(r-'╌'), 2
		}
		thick := light
		if offset%2 != 0 {
			thick = heavy
		}
		vertical := offset%4 >= 2
		span := w
		if vertical {
			span = h
		}
		for i := range count {
			start, end := i*span/count, (i+1)*span/count
			if end-start > 1 {
				end--
			}
			if vertical {
				fill(image.Rect(cx-thick/2, y0+start, cx+(thick+1)/2, y0+end))
			} else {
				fill(image.Rect(x0+start, cy-thick/2, x0+end, cy+(thick+1)/2))
			}
		}
		return true
	}
	switch {
	case r >= '▁' && r <= '█':
		fill(image.Rect(x0, y1-max(1, h*int(r-'▁'+1)/8), x1, y1))
	case r >= '▉' && r <= '▏':
		fill(image.Rect(x0, y0, x0+max(1, w*int('▏'-r+1)/8), y1))
	case r == '▀':
		fill(image.Rect(x0, y0, x1, cy))
	case r == '▐':
		fill(image.Rect(cx, y0, x1, y1))
	case r == '▔':
		fill(image.Rect(x0, y0, x1, y0+max(1, h/8)))
	case r == '▕':
		fill(image.Rect(x1-max(1, w/8), y0, x1, y1))
	case r >= '▖' && r <= '▟':
		mask := [...]uint8{4, 8, 1, 13, 9, 7, 11, 2, 6, 14}[r-'▖']
		quadrants := [...]image.Rectangle{
			image.Rect(x0, y0, cx, cy), image.Rect(cx, y0, x1, cy),
			image.Rect(x0, cy, cx, y1), image.Rect(cx, cy, x1, y1),
		}
		for i, q := range quadrants {
			if mask&(1<<i) != 0 {
				fill(q)
			}
		}
	default:
		return false
	}
	return true
}

func drawPowerline(gtx layout.Context, r rune, cell image.Rectangle, color uint32) {
	left, right := float32(cell.Min.X), float32(cell.Max.X)
	top, bottom := float32(cell.Min.Y), float32(cell.Max.Y)
	if r >= '\ue0b2' {
		left, right = right, left
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(left, top))
	path.LineTo(f32.Pt(right, (top+bottom)/2))
	path.LineTo(f32.Pt(left, bottom))
	if r == '\ue0b0' || r == '\ue0b2' {
		path.Close()
		paint.FillShape(gtx.Ops, rgb(color), clip.Outline{Path: path.End()}.Op())
	} else {
		// Finish recording the path before writing the cell clip to Gio's ops.
		stroke := clip.Stroke{Path: path.End(), Width: 1}
		bounds := clip.Rect(cell).Push(gtx.Ops)
		paint.FillShape(gtx.Ops, rgb(color), stroke.Op())
		bounds.Pop()
	}
}
