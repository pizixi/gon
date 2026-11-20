// Package render rasterizes glyphs into a colour-aware atlas that the UI
// blits into the window with Gio.
package render

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"gioui.org/op/paint"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"

	"gon/internal/fontset"
)

// drawOver blends successive coverage passes (synthetic bold).
var drawOver = draw.Over

// Gio samples the atlas as sRGB and blends in linear light. Encode the
// premultiplied linear colour so glyph edges retain their original coverage.
var glyphColors = func() [256][256]uint8 {
	var table [256][256]uint8
	for channel := range table {
		c := float64(channel) / 255
		linear := c / 12.92
		if c > 0.04045 {
			linear = math.Pow((c+0.055)/1.055, 2.4)
		}
		for alpha := range table[channel] {
			v := linear * float64(alpha) / 255
			encoded := 12.92 * v
			if v > 0.0031308 {
				encoded = 1.055*math.Pow(v, 1/2.4) - 0.055
			}
			table[channel][alpha] = uint8(encoded*255 + 0.5)
		}
	}
	return table
}()

// Size is the edge length of the atlas texture in pixels.
const Size = 1024

// Key identifies one rasterized sprite.
type Key struct {
	Face      string
	FaceIndex uint16
	GID       font.GID
	PPEM      int
	Bold      bool
	RGB       uint32
	// Alpha scales sprite opacity, 0 meaning fully opaque.
	Alpha uint8
}

// Atlas packs glyphs into small pages, with a separate cache for their
// uncoloured coverage. A new colour only updates the current page.
type Atlas struct {
	page       *atlasPage
	pages      []*atlasPage
	pixels     int
	entries    map[Key]Sprite
	masks      map[Key]*bitmap
	maskBytes  int
	generation int
}

const (
	pageSide       = 256
	atlasPixels    = Size * Size
	maskCacheBytes = 2 << 20
	maskCacheCount = 2048
)

func NewAtlas() *Atlas {
	a := new(Atlas)
	a.Reset()
	return a
}

// Snapshot returns the active page's pixels and the atlas generation.
func (a *Atlas) Snapshot() ([]byte, int) {
	if a.page == nil {
		return nil, a.generation
	}
	return a.page.data, a.generation
}

func (a *Atlas) Reset() {
	if a.page != nil {
		a.page.seal()
	}
	a.page = nil
	a.pages = nil
	a.pixels = 0
	a.entries = make(map[Key]Sprite)
	a.masks = make(map[Key]*bitmap)
	a.maskBytes = 0
	a.generation++
}

type Sprite struct {
	R       image.Rectangle
	Left    int
	Top     int
	Advance int
	OK      bool
	page    *atlasPage
}

type atlasPage struct {
	side       int
	x, y, rowH int
	data       []byte
	keys       []Key
	used       image.Point
	tex        paint.ImageOp
	dirty      bool
	sealed     bool
}

func (p *atlasPage) seal() {
	p.sealed = true
	if !p.dirty {
		p.data = nil
	}
}

// imageOp snapshots only the occupied pixels. Gio requires the result to
// remain immutable, even when another glyph is added later in the frame.
func (p *atlasPage) imageOp() paint.ImageOp {
	if p.dirty {
		img := image.NewRGBA(image.Rectangle{Max: p.used})
		for y := 0; y < p.used.Y; y++ {
			dst, src := y*img.Stride, y*p.side*4
			copy(img.Pix[dst:dst+img.Stride], p.data[src:src+img.Stride])
		}
		p.tex = paint.NewImageOp(img)
		p.tex.Filter = paint.FilterNearest
		p.dirty = false
		if p.sealed {
			p.data = nil
		}
	}
	return p.tex
}

func (p *atlasPage) reserve(w, h int) (image.Rectangle, bool) {
	if p.x+w > p.side {
		p.x, p.y, p.rowH = 0, p.y+p.rowH+1, 0
	}
	if p.y+h > p.side {
		return image.Rectangle{}, false
	}
	r := image.Rect(p.x, p.y, p.x+w, p.y+h)
	p.x += w + 1
	p.rowH = max(p.rowH, h)
	p.used = image.Pt(max(p.used.X, r.Max.X), max(p.used.Y, r.Max.Y))
	return r, true
}

func (a *Atlas) startPage(side int) {
	if a.page != nil {
		a.page.seal()
	}
	for a.pixels+side*side > atlasPixels {
		old := a.pages[0]
		for _, key := range old.keys {
			delete(a.entries, key)
		}
		old.keys = nil
		a.pixels -= old.side * old.side
		a.pages[0] = nil
		a.pages = a.pages[1:]
	}
	p := &atlasPage{side: side, data: make([]byte, side*side*4)}
	a.pages = append(a.pages, p)
	a.page, a.pixels = p, a.pixels+side*side
}

// Lookup returns a sprite for the key, rasterizing its outline only when
// the coverage cache misses.
func (a *Atlas) Lookup(key Key, face *fontset.Face) Sprite {
	if sprite, ok := a.entries[key]; ok {
		return sprite
	}
	maskKey := key
	maskKey.RGB, maskKey.Alpha = 0, 0
	bm, found := a.masks[maskKey]
	if !found {
		bm = rasterize(face, maskKey)
		if bm == nil {
			return Sprite{}
		}
		if a.maskBytes+len(bm.pix) > maskCacheBytes || len(a.masks) >= maskCacheCount {
			clear(a.masks)
			a.maskBytes = 0
		}
		a.masks[maskKey] = bm
		a.maskBytes += len(bm.pix)
	}
	if bm.w == 0 || bm.h == 0 {
		return Sprite{Advance: bm.advance}
	}

	var rect image.Rectangle
	if a.page != nil {
		rect, _ = a.page.reserve(bm.w, bm.h)
	}
	if rect.Empty() {
		a.startPage(max(pageSide, bm.w, bm.h))
		rect, _ = a.page.reserve(bm.w, bm.h)
	}
	page := a.page
	alpha := uint32(key.Alpha)
	if alpha == 0 {
		alpha = 255
	}
	red := &glyphColors[key.RGB>>16&255]
	green := &glyphColors[key.RGB>>8&255]
	blue := &glyphColors[key.RGB&255]
	for row := 0; row < bm.h; row++ {
		src := bm.pix[row*bm.w : (row+1)*bm.w]
		dst := page.data[((rect.Min.Y+row)*page.side+rect.Min.X)*4:]
		for i, coverage := range src {
			opacity := (uint32(coverage)*alpha + 127) / 255
			pixel := dst[i*4 : i*4+4]
			pixel[0], pixel[1], pixel[2] = red[opacity], green[opacity], blue[opacity]
			pixel[3] = byte(opacity)
		}
	}
	sprite := Sprite{R: rect, Left: bm.left, Top: bm.top, Advance: bm.advance, OK: true, page: page}
	page.keys = append(page.keys, key)
	page.dirty = true
	a.entries[key] = sprite
	a.generation++
	return sprite
}

type bitmap struct {
	pix     []byte
	w, h    int
	left    int
	top     int
	advance int
}

// rasterize renders one glyph in coverage form.
func rasterize(face *fontset.Face, key Key) *bitmap {
	if face == nil {
		return nil
	}
	upem := face.Upem
	if upem <= 0 || key.PPEM <= 0 {
		return nil
	}
	scale := float32(key.PPEM) / upem
	adv := int(math.Round(float64(face.HorizontalAdvance(key.GID) * scale)))

	outline, ext := face.Outline(key.GID)
	if len(outline) == 0 {
		return &bitmap{advance: adv}
	}

	stroke := float32(0)
	if key.Bold {
		stroke = float32(key.PPEM) / 20
		if stroke < 0.55 {
			stroke = 0.55
		}
	}
	// Some fonts report a negative height (y-down convention); the rasterizer
	// only needs the magnitude.
	wUnits := float32(math.Abs(float64(ext.Width))) * scale
	hUnits := float32(math.Abs(float64(ext.Height))) * scale
	if wUnits <= 0 && stroke == 0 {
		return &bitmap{advance: adv}
	}

	pad := 1 + int(math.Ceil(float64(stroke)))
	left := int(math.Floor(float64(ext.XBearing*scale))) - pad
	top := int(math.Floor(float64(-ext.YBearing*scale))) - pad
	ww := int(math.Ceil(float64(wUnits))) + pad*2 + 1 + int(math.Ceil(float64(stroke)))
	hh := int(math.Ceil(float64(hUnits))) + pad*2 + 1
	if ww > 512 || hh > 512 || ww < 1 || hh < 1 {
		return &bitmap{advance: adv}
	}

	alpha := image.NewAlpha(image.Rect(0, 0, ww, hh))
	opaque := image.NewUniform(color.Alpha{A: 0xff})
	ox, oy := float32(-left), float32(-top)

	passes := 1
	if stroke > 0 {
		passes = 5
	}
	// Synthetic bold repeats the outline at five offsets. Reset reuses the
	// coverage work buffer while clearing it between independent passes.
	var ras vector.Rasterizer
	for i := 0; i < passes; i++ {
		dx := float32(0)
		if stroke > 0 {
			dx = float32(i) / float32(passes-1) * stroke
		}
		ras.Reset(ww, hh)
		ras.DrawOp = drawOver
		for _, seg := range outline {
			p := seg.Args
			fp := func(i int) (float32, float32) {
				return p[i].X*scale + ox + dx, -p[i].Y*scale + oy
			}
			switch seg.Op {
			case opentype.SegmentOpMoveTo:
				x, y := fp(0)
				ras.MoveTo(x, y)
			case opentype.SegmentOpLineTo:
				x, y := fp(0)
				ras.LineTo(x, y)
			case opentype.SegmentOpQuadTo:
				cx, cy := fp(0)
				x, y := fp(1)
				ras.QuadTo(cx, cy, x, y)
			case opentype.SegmentOpCubeTo:
				c1x, c1y := fp(0)
				c2x, c2y := fp(1)
				x, y := fp(2)
				ras.CubeTo(c1x, c1y, c2x, c2y, x, y)
			}
		}
		ras.ClosePath()
		ras.Draw(alpha, alpha.Bounds(), opaque, image.Point{})
	}

	pix, stride := alpha.Pix, alpha.Stride
	minX, minY, maxX, maxY := ww, hh, -1, -1
	for y := 0; y < hh; y++ {
		row := pix[y*stride : y*stride+ww]
		for x, v := range row {
			if v != 0 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return &bitmap{advance: adv}
	}
	tw, th := maxX-minX+1, maxY-minY+1
	out := make([]byte, tw*th)
	for y := 0; y < th; y++ {
		copy(out[y*tw:(y+1)*tw], pix[(minY+y)*stride+minX:(minY+y)*stride+minX+tw])
	}
	return &bitmap{pix: out, w: tw, h: th, left: left + minX, top: top + minY, advance: adv}
}
