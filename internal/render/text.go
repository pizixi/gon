package render

import (
	"image"

	"gioui.org/layout"
	"github.com/rivo/uniseg"

	"gon/internal/fontset"
)

// Text draws UI strings through the View's glyph atlas.
type Text struct {
	v      *View
	family *fontset.Family
}

// Text returns the text renderer bound to this view.
func (v *View) TextRenderer() *Text {
	family := v.InterfaceFamily
	if family == nil {
		family = v.Family
	}
	return &Text{v: v, family: family}
}

// Ascent returns the distance from the baseline to the top of the line.
func (t *Text) Ascent(px int) int {
	nom := t.family.Nominal()
	if nom == nil || nom.Upem <= 0 {
		return px
	}
	v := int(nom.Ascent/float32(nom.Upem)*float32(px) + 0.5)
	if v <= 0 {
		v = px
	}
	return v
}

// LineHeight approximates the natural line height at px.
func (t *Text) LineHeight(px int) int { return t.Ascent(px) + px/4 }

// Measure returns the advance width of s at px.
func (t *Text) Measure(s string, px int) int {
	total := 0
	for _, r := range s {
		total += t.advance(r, px)
	}
	return total
}

func (t *Text) advance(r rune, px int) int {
	face := t.family.Pick(r, false, false)
	if face == nil || face.Upem <= 0 {
		return px / 2
	}
	gid, ok := face.NominalGlyph(r)
	if !ok {
		return px / 2
	}
	adv := int(face.HorizontalAdvance(gid)/face.Upem*float32(px) + 0.5)
	if adv <= 0 {
		return px / 2
	}
	return adv
}

// Fit shortens s with an ellipsis so it fits maxW pixels.
func (t *Text) Fit(s string, px, maxW int) string {
	if t.Measure(s, px) <= maxW {
		return s
	}
	const dots = "…"
	dw := t.Measure(dots, px)
	if dw > maxW {
		return ""
	}
	end, width := 0, 0
	clusters := uniseg.NewGraphemes(s)
	for clusters.Next() {
		width += t.Measure(clusters.Str(), px)
		if width+dw > maxW {
			break
		}
		_, end = clusters.Positions()
	}
	return s[:end] + dots
}

// Draw writes s with its baseline at (x, y) and returns the advance width.
// alpha scales glyph opacity (0 means fully opaque).
func (t *Text) Draw(gtx layout.Context, s string, x, y, px int, col uint32, alpha uint8) int {
	if alpha == 0 {
		alpha = 0xff
	}
	// Populate the atlas first, then upload once, then blit.
	places := t.v.glyphs[:0]
	pen := 0
	for _, r := range s {
		adv := t.advance(r, px)
		if r != ' ' {
			face := t.family.Pick(r, false, false)
			if gid, ok := face.NominalGlyph(r); ok {
				key := Key{Face: face.Path, FaceIndex: face.Index, GID: gid, PPEM: px, RGB: col, Alpha: alpha}
				sp := t.v.Atlas.Lookup(key, face)
				if sp.OK {
					places = append(places, placedGlyph{sprite: sp, baseline: image.Pt(x+pen, y)})
				}
			}
		}
		pen += adv
	}
	for _, glyph := range places {
		glyph.draw(gtx)
	}
	clear(places)
	t.v.glyphs = places[:0]
	return pen
}

// DrawCenter draws s horizontally centered within [x, x+w].
func (t *Text) DrawCenter(gtx layout.Context, s string, x, w, y, px int, col uint32, alpha uint8) int {
	tw := t.Measure(s, px)
	t.Draw(gtx, s, x+(w-tw)/2, y, px, col, alpha)
	return tw
}
