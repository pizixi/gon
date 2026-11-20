package render

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/go-text/typesetting/font"

	"gon/internal/fontset"
	"gon/internal/term"
)

// View draws terminal cells with a glyph atlas and reports grid metrics.
//
// A View shares its glyph atlas across terminal panes and interface text.
type View struct {
	Family          *fontset.Family
	InterfaceFamily *fontset.Family
	Atlas           *Atlas
	Pal             *term.Palette

	metrics    Metrics
	glyphs     []placedGlyph
	lineHeight float64

	// BgAlpha is the terminal background opacity (0-255).
	BgAlpha uint8
	// TransparentBackground lets the window paint one shared backdrop.
	TransparentBackground bool
	// CursorColor is the 0xRRGGBB cursor colour.
	CursorColor uint32
	// CursorBlinkMS is the blink half-period; 0 disables blinking.
	CursorBlinkMS int
	// ShowCursor hides the cursor for unfocused panes.
	ShowCursor bool

	cursorEpoch time.Time
}

// Metrics are the resolved cell and font metrics in pixels.
type Metrics struct {
	CellW    int
	CellH    int
	Baseline int // distance from cell top to the text baseline
	FontSize int
}

// NewView creates a View with the given family and nominal pixel size.
func NewView(fam *fontset.Family, fontSize int) *View {
	v := &View{
		Family:      fam,
		Atlas:       NewAtlas(),
		Pal:         term.NewPalette(0xd4d4d4, 0x1e1e1e),
		BgAlpha:     0xff,
		CursorColor: 0xd4d4d4,
		ShowCursor:  true,
		lineHeight:  1.05,
		cursorEpoch: time.Now(),
	}
	v.SetFontSize(fontSize)
	return v
}

// SetFontSize changes the nominal pixel size and invalidates cached glyphs.
func (v *View) SetFontSize(px int) {
	if px < 6 {
		px = 6
	}
	if px > 96 {
		px = 96
	}
	v.metrics.FontSize = px
	v.recompute()
}

func (v *View) SetFamily(fam *fontset.Family) {
	v.Family = fam
	v.recompute()
}

func (v *View) SetLineHeight(height float64) {
	if height > 0 && height != v.lineHeight {
		v.lineHeight = height
		v.recompute()
	}
}

// Metrics returns the resolved metrics.
func (v *View) Metrics() Metrics { return v.metrics }

// FontSize returns the nominal pixel size.
func (v *View) FontSize() int { return v.metrics.FontSize }

// FontSizeFor estimates a pixel size that fits cols x rows into w x h pixels.
func (v *View) FontSizeFor(cols, rows, w, h int) int {
	fs := v.metrics.FontSize
	if cols <= 0 || rows <= 0 || w <= 0 || h <= 0 {
		return fs
	}
	nom := v.Family.Nominal()
	if nom == nil || nom.Upem <= 0 {
		return fs
	}
	gid, ok := nom.NominalGlyph('M')
	if !ok {
		return fs
	}
	adv := nom.HorizontalAdvance(gid)
	lineUnits := nom.Ascent - nom.Descent
	if adv <= 0 || lineUnits <= 0 {
		return fs
	}
	byW := float32(w) / float32(cols) / adv * nom.Upem
	byH := float32(h) / float32(rows) / lineUnits * nom.Upem
	size := int(byW)
	if int(byH) < size {
		size = int(byH)
	}
	if size < 6 {
		size = 6
	}
	if size > 96 {
		size = 96
	}
	return size
}

func (v *View) recompute() {
	nom := v.Family.Nominal()
	if nom == nil {
		v.metrics.CellW = 8
		v.metrics.CellH = v.metrics.FontSize + 4
		v.metrics.Baseline = v.metrics.FontSize
		return
	}
	upem := float32(nom.Upem)
	if upem <= 0 {
		upem = 2048
	}
	scale := float32(v.metrics.FontSize) / upem
	asc := nom.Ascent
	desc := nom.Descent
	if asc <= 0 {
		asc = upem * 0.8
	}
	if desc >= 0 {
		desc = -upem * 0.2
	}
	lineH := int((asc-desc)*scale*float32(1.2*v.lineHeight/1.05) + 0.5)
	if lineH < v.metrics.FontSize+2 {
		lineH = v.metrics.FontSize + 2
	}
	baseline := int(asc*scale + (float32(lineH)-(asc-desc)*scale)/2 + 0.5)
	if baseline < 1 {
		baseline = 1
	}
	if baseline >= lineH {
		baseline = lineH - 1
	}
	cellW := 8
	if gid, ok := nom.NominalGlyph('M'); ok {
		if adv := nom.HorizontalAdvance(gid) * scale; adv > 0 {
			cellW = int(adv + 0.5)
		}
	}
	if cellW < 4 {
		cellW = 4
	}
	v.metrics.CellW = cellW
	v.metrics.CellH = lineH
	v.metrics.Baseline = baseline
	v.Atlas.Reset()
}

// layoutParams resolves viewport/scrolling parameters for one draw.
type layoutParams struct {
	cols, rows int
	w, h       int
	m          Metrics
	firstLine  int
	useHistory bool
}

// GridFrom converts a pixel rectangle into a column/row count.
func (v *View) GridFrom(w, h int) (cols, rows int) {
	m := v.metrics
	if m.CellW <= 0 || m.CellH <= 0 {
		return 1, 1
	}
	cols = w / m.CellW
	rows = h / m.CellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// Draw renders the screen into the current clip area at the current origin.
func (v *View) Draw(gtx layout.Context, scr *term.Screen, scrollOffset int) (cols, rows int) {
	w, h := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	return v.DrawRect(gtx, scr, w, h, scrollOffset, Selection{}, 0)
}

// Selection describes a text selection in absolute line/column coordinates.
type Selection struct {
	L0, C0, L1, C1 int
	Active         bool
}

// DrawRect renders the screen into a w x h pixel area at the current origin
// and returns the grid size in cells. sel, when active, is highlighted.
func (v *View) DrawRect(gtx layout.Context, scr *term.Screen, w, h, scrollOffset int, sel Selection, selColor uint32) (cols, rows int) {
	cols, rows = v.GridFrom(w, h)
	p := layoutParams{cols: cols, rows: rows, w: w, h: h, m: v.metrics}
	if scr != nil {
		// The top visible line is needed for selection math even when the view
		// is live at the bottom of the buffer.
		p.firstLine = scr.ViewTop(scrollOffset)
		p.useHistory = scrollOffset > 0
		if !p.useHistory {
			frame, until := scr.DisplayFrame(gtx.Now)
			_, _, generation := scr.ScrollbackState()
			top, frameGeneration := scr.FramePosition(frame)
			p.firstLine = top
			sel.Active = sel.Active && generation == frameGeneration
			scr = frame
			if !until.IsZero() {
				gtx.Execute(op.InvalidateCmd{At: until})
			}
		}
	}
	v.draw(gtx, scr, p, sel, selColor)
	return cols, rows
}

func (v *View) draw(gtx layout.Context, scr *term.Screen, p layoutParams, sel Selection, selColor uint32) {
	m := p.m
	palette := v.Pal
	if scr != nil && (scr.FgColor.Mode == term.ColorRGB || scr.BgColor.Mode == term.ColorRGB) {
		colors := *palette
		colors.Fg = scr.FgColor.RGBOr(colors.Fg)
		colors.Bg = scr.BgColor.RGBOr(colors.Bg)
		palette = &colors
	}
	if !v.TransparentBackground || palette.Bg != v.Pal.Bg {
		bg := rgb(palette.Bg)
		bg.A = v.BgAlpha
		paint.FillShape(gtx.Ops, bg, clip.Rect(image.Rect(0, 0, p.w, p.h)).Op())
	}
	if scr == nil {
		return
	}

	// Background fills, merged across runs of the same color.
	for row := 0; row < p.rows; row++ {
		line := v.lineAt(scr, p, row)
		if line == nil {
			continue
		}
		x := 0
		for x < len(line) && x < p.cols {
			c := line[x]
			w := cellWidth(c)
			if c.Cont {
				x += w
				continue
			}
			st := palette.StyleOf(c.A)
			if st.BG != palette.Bg {
				run := w
				for x+run < len(line) && x+run < p.cols {
					nc := line[x+run]
					if nc.Cont {
						run += cellWidth(nc)
						continue
					}
					if palette.StyleOf(nc.A).BG != st.BG {
						break
					}
					run += cellWidth(nc)
				}
				rect := image.Rect(x*m.CellW, row*m.CellH, min(p.cols, x+run)*m.CellW, (row+1)*m.CellH)
				paint.FillShape(gtx.Ops, rgb(st.BG), clip.Rect(rect).Op())
				x += run
				continue
			}
			x += w
		}
	}
	if sel.Active {
		v.drawSelection(gtx, p, sel, selColor)
	}

	// Glyphs and decorations.
	glyphs := v.glyphs[:0]
	for row := 0; row < p.rows; row++ {
		line := v.lineAt(scr, p, row)
		if line == nil {
			continue
		}
		for x := 0; x < len(line) && x < p.cols; {
			c := line[x]
			w := cellWidth(c)
			if c.Cont {
				x += w
				continue
			}
			hasGlyph := c.R != 0 && (c.R != ' ' || c.Combining != "")
			if !hasGlyph && !c.A.Has(term.AttrUnderline|term.AttrStrike) {
				x += w
				continue
			}
			st := palette.StyleOf(c.A)
			px0, px1 := x*m.CellW, (x+w)*m.CellW
			py := row * m.CellH
			if hasGlyph && !c.A.Has(term.AttrInvisible) && x+w <= p.cols {
				cell := image.Rect(px0, py, px1, py+m.CellH)
				if !drawGridGlyph(gtx, c.R, cell, st.FG) {
					glyphs = v.placeCellGlyphs(glyphs, c, st.FG, cell, m)
				}
			}
			if c.A.Has(term.AttrUnderline) {
				y := py + m.Baseline + 1
				y = min(y, py+m.CellH-1)
				paint.FillShape(gtx.Ops, rgb(st.FG), clip.Rect(image.Rect(px0, y, min(px1, p.cols*m.CellW), y+1)).Op())
			}
			if c.A.Has(term.AttrStrike) {
				y := py + m.Baseline - m.FontSize/3
				paint.FillShape(gtx.Ops, rgb(st.FG), clip.Rect(image.Rect(px0, y, px1, y+1)).Op())
			}
			x += w
		}
	}
	for _, glyph := range glyphs {
		glyph.draw(gtx)
	}
	clear(glyphs)
	v.glyphs = glyphs[:0]

	if !p.useHistory {
		v.drawCursor(gtx, scr, p, palette)
	}
}

func (v *View) drawSelection(gtx layout.Context, p layoutParams, sel Selection, col uint32) {
	m := p.m
	// Gio blends in linear space, so the alpha is raised to stay visible.
	c := color.NRGBA{R: uint8(col >> 16), G: uint8(col >> 8), B: uint8(col), A: 0xba}
	for row := 0; row < p.rows; row++ {
		abs := p.firstLine + row
		if abs < sel.L0 || abs > sel.L1 {
			continue
		}
		x0, x1 := 0, p.cols
		if abs == sel.L0 {
			x0 = sel.C0
		}
		if abs == sel.L1 {
			x1 = sel.C1 + 1
		}
		if x0 < 0 {
			x0 = 0
		}
		if x1 > p.cols {
			x1 = p.cols
		}
		if x0 >= x1 {
			continue
		}
		rect := image.Rect(x0*m.CellW, row*m.CellH, x1*m.CellW, (row+1)*m.CellH)
		paint.FillShape(gtx.Ops, c, clip.Rect(rect).Op())
	}
}

// lineAt returns the cells for a viewport row.
func (v *View) lineAt(scr *term.Screen, p layoutParams, row int) []term.Cell {
	abs := p.firstLine + row
	if p.useHistory {
		return scr.AbsLine(abs)
	}
	if row >= scr.Rows {
		return nil
	}
	return scr.AbsLine(scr.ScrollbackLen() + row)
}

func (v *View) terminalGlyph(c term.Cell, color uint32, size int) Sprite {
	face := v.Family.Pick(c.R, c.A.Has(term.AttrBold), c.A.Has(term.AttrItalic))
	gid, ok := face.NominalGlyph(c.R)
	if !ok {
		return Sprite{}
	}
	return v.glyphSprite(face, gid, c.A, color, size)
}

func (v *View) glyphSprite(face *fontset.Face, gid font.GID, attr term.Attr, color uint32, size int) Sprite {
	key := Key{
		Face: face.Path, FaceIndex: face.Index, GID: gid, PPEM: size,
		Bold: attr.Has(term.AttrBold) && !face.Bold, RGB: color,
	}
	return v.Atlas.Lookup(key, face)
}

func (v *View) placeGlyphLayers(dst []placedGlyph, face *fontset.Face, gid font.GID, color uint32, size int, baseline image.Point) []placedGlyph {
	for _, layer := range face.ColorLayers(gid) {
		fg, alpha := color, uint8(255)
		if !layer.Foreground {
			fg = uint32(layer.Color.R)<<16 | uint32(layer.Color.G)<<8 | uint32(layer.Color.B)
			alpha = layer.Color.A
		}
		sprite := v.Atlas.Lookup(Key{Face: face.Path, FaceIndex: face.Index, GID: layer.ID, PPEM: size, RGB: fg, Alpha: alpha}, face)
		if sprite.OK {
			dst = append(dst, placedGlyph{sprite: sprite, baseline: baseline})
		}
	}
	return dst
}

func (v *View) placeCellGlyphs(dst []placedGlyph, c term.Cell, color uint32, cell image.Rectangle, m Metrics) []placedGlyph {
	baseline := cell.Min.Add(image.Pt(0, m.Baseline))
	start := len(dst)
	advance := 0
	if c.Combining != "" || c.R >= 0x1f000 {
		face, cluster := v.Family.ShapeCluster(c.Text(), c.A.Has(term.AttrBold), c.A.Has(term.AttrItalic))
		if len(cluster.Glyphs) > 0 {
			scale := float32(m.FontSize) / face.Upem
			advance = int(math.Round(float64(float32(cluster.Advance) * scale)))
			for _, glyph := range cluster.Glyphs {
				offset := image.Pt(int(math.Round(float64(float32(glyph.X)*scale))), int(math.Round(float64(-float32(glyph.Y)*scale))))
				at := baseline.Add(offset)
				before := len(dst)
				dst = v.placeGlyphLayers(dst, face, glyph.ID, color, m.FontSize, at)
				if before == len(dst) {
					if sp := v.glyphSprite(face, glyph.ID, c.A, color, m.FontSize); sp.OK {
						dst = append(dst, placedGlyph{sprite: sp, baseline: at})
					}
				}
			}
		}
	}
	if start == len(dst) {
		sp := v.terminalGlyph(c, color, m.FontSize)
		if !sp.OK {
			return dst
		}
		advance = sp.Advance
		dst = append(dst, placedGlyph{sprite: sp, baseline: baseline})
	}
	var ink image.Rectangle
	for _, glyph := range dst[start:] {
		r := image.Rectangle{Max: glyph.sprite.R.Size()}.Add(glyph.baseline.Add(image.Pt(glyph.sprite.Left, glyph.sprite.Top)))
		ink = ink.Union(r)
	}
	dx, dy := (cell.Dx()-advance)/2, 0
	if ink.Dx() <= cell.Dx() {
		dx = max(cell.Min.X-ink.Min.X, min(dx, cell.Max.X-ink.Max.X))
	} else {
		dx = cell.Min.X - ink.Min.X
	}
	if ink.Dy() <= cell.Dy() {
		dy = max(cell.Min.Y-ink.Min.Y, min(0, cell.Max.Y-ink.Max.Y))
	} else {
		dy = cell.Min.Y - ink.Min.Y
	}
	sx, sy := min(1, float32(cell.Dx())/float32(ink.Dx())), min(1, float32(cell.Dy())/float32(ink.Dy()))
	for i := start; i < len(dst); i++ {
		dst[i].baseline = dst[i].baseline.Add(image.Pt(dx, dy))
		dst[i].cell = cell
		dst[i].scale = f32.Pt(sx, sy)
	}
	return dst
}

type placedGlyph struct {
	sprite   Sprite
	baseline image.Point
	cell     image.Rectangle
	scale    f32.Point
}

func (g placedGlyph) draw(gtx layout.Context) {
	if g.scale != (f32.Point{}) && g.scale != f32.Pt(1, 1) {
		defer clip.Rect(g.cell).Push(gtx.Ops).Pop()
		origin := f32.Pt(float32(g.cell.Min.X), float32(g.cell.Min.Y))
		defer op.Affine(f32.Affine2D{}.Scale(origin, g.scale)).Push(gtx.Ops).Pop()
	}
	sp := g.sprite
	off := g.baseline.Add(image.Pt(sp.Left, sp.Top)).Sub(sp.R.Min)
	defer op.Offset(off).Push(gtx.Ops).Pop()
	defer clip.Rect(sp.R).Push(gtx.Ops).Pop()
	sp.page.imageOp().Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func (v *View) drawCursor(gtx layout.Context, scr *term.Screen, p layoutParams, palette *term.Palette) {
	if scr.CursorHidden() || !v.ShowCursor {
		return
	}
	// DECSCUSR: 0/1 blinking block, 2 steady block, 3/4 underline, 5/6 bar.
	style := scr.CursorStyle
	if style < 0 || style > 6 {
		style = 1
	}
	if (style == 0 || style%2 == 1) && v.CursorBlinkMS > 0 {
		period := time.Duration(v.CursorBlinkMS) * time.Millisecond
		const idleTimeout = 5 * time.Second
		now := gtx.Now
		if now.IsZero() {
			now = time.Now()
		}
		elapsed := max(0, now.Sub(v.cursorEpoch))
		if period > 0 && elapsed < idleTimeout {
			cycle := elapsed / period
			next := min(idleTimeout, cycle*period+min(period, idleTimeout))
			gtx.Execute(op.InvalidateCmd{At: v.cursorEpoch.Add(next)})
			if cycle%2 == 1 {
				return
			}
		}
	}
	cx, cy := scr.Cursor()
	if cy < 0 || cy >= p.rows || cx < 0 || cx >= p.cols {
		return
	}
	w := 1
	c := scr.CellAt(cx, cy)
	if c.Cont && cx > 0 {
		cx--
		c = scr.CellAt(cx, cy)
	}
	if c.W > 1 {
		w = int(c.W)
	}
	m := p.m
	cellRect := image.Rect(cx*m.CellW, cy*m.CellH, (cx+w)*m.CellW, (cy+1)*m.CellH)
	cur := rgb(scr.CursorColor.RGBOr(v.CursorColor))
	thick := m.CellW / 8
	if thick < 2 {
		thick = 2
	}

	switch style {
	case 3, 4: // underline
		rect := image.Rect(cellRect.Min.X, cellRect.Max.Y-thick, cellRect.Max.X, cellRect.Max.Y)
		paint.FillShape(gtx.Ops, cur, clip.Rect(rect).Op())
		return
	case 5, 6: // vertical bar
		rect := image.Rect(cellRect.Min.X, cellRect.Min.Y, cellRect.Min.X+thick, cellRect.Max.Y)
		paint.FillShape(gtx.Ops, cur, clip.Rect(rect).Op())
		return
	}
	rect := cellRect
	paint.FillShape(gtx.Ops, cur, clip.Rect(rect).Op())

	// Reverse video: redraw the covered glyph in the background colour.
	if c.R != 0 && c.R != ' ' && !c.A.Has(term.AttrInvisible) {
		fg := palette.StyleOf(c.A).BG
		if !drawGridGlyph(gtx, c.R, cellRect, fg) {
			var buf [4]placedGlyph
			for _, glyph := range v.placeCellGlyphs(buf[:0], c, fg, cellRect, m) {
				glyph.draw(gtx)
			}
		}
	}
}

// ResetCursorBlink restarts the blink phase so the cursor is immediately
// visible after input.
func (v *View) ResetCursorBlink() { v.cursorEpoch = time.Now() }

func cellWidth(c term.Cell) int {
	if c.W < 1 {
		return 1
	}
	return int(c.W)
}

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

// RGBA converts a 0xRRGGBB color to an NRGBA with the given alpha.
func RGBA(c uint32, a uint8) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: a}
}

// Ascent returns the baseline offset for UI text at px.
func (v *View) Ascent(px int) int { return v.TextRenderer().Ascent(px) }

// Measure returns the advance width of s at px.
func (v *View) Measure(s string, px int) int { return v.TextRenderer().Measure(s, px) }

// Fit shortens s so it fits maxW pixels at px.
func (v *View) Fit(s string, px, maxW int) string { return v.TextRenderer().Fit(s, px, maxW) }

// DrawUI draws s with its baseline at (x,y) and returns its width.
func (v *View) DrawUI(gtx layout.Context, s string, x, y, px int, col uint32, alpha uint8) int {
	return v.TextRenderer().Draw(gtx, s, x, y, px, col, alpha)
}
