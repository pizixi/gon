package fontset

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
	"github.com/go-text/typesetting/harfbuzz"
	imagefont "golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Face loads outlines from disk on demand. It uses the full OpenType loader
// for fonts that the smaller SFNT reader cannot parse.
type Face struct {
	Name      string
	Path      string
	Index     uint16
	Upem      float32
	Ascent    float32
	Descent   float32
	CapHeight float32
	Bold      bool

	mu         sync.Mutex
	file       *os.File
	sf         *sfnt.Font
	full       *font.Face
	buffer     sfnt.Buffer
	glyphs     map[rune]font.GID
	advances   map[font.GID]float32
	shaped     map[string]Cluster
	shapeFont  *harfbuzz.Font
	loadedFull bool
}

// LoadFace loads face zero from a font file.
func LoadFace(path string) (*Face, error) {
	return loadFace(font.FontID{File: path})
}

func loadFace(id font.FontID) (*Face, error) {
	file, err := os.Open(id.File)
	if err != nil {
		return nil, err
	}
	face := &Face{Path: id.File, Index: id.Index, Name: filepath.Base(id.File)}
	if collection, err := sfnt.ParseCollectionReaderAt(file); err == nil {
		if sf, err := collection.Font(int(id.Index)); err == nil {
			face.file, face.sf = file, sf
			face.Upem = float32(sf.UnitsPerEm())
			if name, err := sf.Name(&face.buffer, sfnt.NameIDFamily); err == nil && name != "" {
				face.Name = name
			}
			if style, err := sf.Name(&face.buffer, sfnt.NameIDSubfamily); err == nil {
				style = strings.ToLower(style)
				face.Bold = strings.Contains(style, "bold") || strings.Contains(style, "black") || strings.Contains(style, "heavy")
			}
			metrics, err := sf.Metrics(&face.buffer, fixed.I(int(face.Upem)), imagefont.HintingNone)
			if err == nil {
				face.Ascent = float32(metrics.Ascent) / 64
				face.Descent = -float32(metrics.Descent) / 64
				face.CapHeight = float32(metrics.CapHeight) / 64
			}
			face.normalizeMetrics()
			return face, nil
		}
	}
	defer file.Close()
	loaders, err := opentype.NewLoaders(file)
	if err != nil {
		return nil, err
	}
	if int(id.Index) >= len(loaders) {
		return nil, fmt.Errorf("font index %d is missing from %s", id.Index, id.File)
	}
	ft, err := font.NewFont(loaders[id.Index])
	if err != nil {
		return nil, err
	}
	face.full = font.NewFace(ft)
	face.Upem = float32(face.full.Upem())
	description := ft.Describe()
	face.Bold = description.Aspect.Weight >= font.WeightSemibold
	if name := description.Family; name != "" {
		face.Name = name
	}
	if ext, ok := face.full.FontHExtents(); ok {
		face.Ascent, face.Descent = ext.Ascender, ext.Descender
	}
	face.CapHeight = face.full.LineMetric(font.CapHeight)
	face.normalizeMetrics()
	return face, nil
}

// GlyphPosition holds a shaped glyph and its baseline offset in font units.
type GlyphPosition struct {
	ID   font.GID
	X, Y int32
}

type Cluster struct {
	Glyphs  []GlyphPosition
	Advance int32
}

// ShapeCluster shapes a single terminal cell without joining adjacent cells.
func (f *Face) ShapeCluster(text string) Cluster {
	if f == nil {
		return Cluster{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if shaped, ok := f.shaped[text]; ok {
		return shaped
	}
	if f.shapeFont == nil {
		if !f.ensureFull() {
			return Cluster{}
		}
		f.shapeFont = harfbuzz.NewFont(f.full)
		f.shaped = make(map[string]Cluster)
	}
	buf := harfbuzz.NewBuffer()
	buf.AddRunes([]rune(text), 0, -1)
	buf.GuessSegmentProperties()
	buf.Shape(f.shapeFont, nil)
	cluster := Cluster{Glyphs: make([]GlyphPosition, 0, len(buf.Info))}
	var x, y int32
	for i, info := range buf.Info {
		if info.Glyph == 0 {
			cluster = Cluster{}
			break
		}
		pos := buf.Pos[i]
		cluster.Glyphs = append(cluster.Glyphs, GlyphPosition{info.Glyph, x + pos.XOffset, y + pos.YOffset})
		x, y = x+pos.XAdvance, y+pos.YAdvance
		cluster.Advance = x
	}
	if len(f.shaped) >= 512 {
		clear(f.shaped)
	}
	f.shaped[text] = cluster
	return cluster
}

func (f *Face) ensureFull() bool {
	if f.full == nil && f.file != nil && !f.loadedFull {
		f.loadedFull = true
		if loaders, err := opentype.NewLoaders(f.file); err == nil && int(f.Index) < len(loaders) {
			if parsed, err := font.NewFont(loaders[f.Index]); err == nil {
				f.full = font.NewFace(parsed)
			}
		}
	}
	return f.full != nil
}

// ColorLayer is a solid COLR glyph layer from the font's default palette.
type ColorLayer struct {
	ID         font.GID
	Color      color.NRGBA
	Foreground bool
}

func (f *Face) ColorLayers(gid font.GID) []ColorLayer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.ensureFull() || len(f.full.CPAL) == 0 {
		return nil
	}
	glyph, ok := f.full.GlyphDataColor(gid)
	if !ok {
		return nil
	}
	layers, ok := glyph.Paint.(tables.PaintColrLayersResolved)
	if !ok {
		return nil
	}
	pal := f.full.CPAL[0]
	result := make([]ColorLayer, 0, len(layers))
	for _, layer := range layers {
		if layer.PaletteIndex == 0xffff {
			result = append(result, ColorLayer{ID: font.GID(layer.GlyphID), Foreground: true})
		} else if int(layer.PaletteIndex) < len(pal) {
			c := pal[layer.PaletteIndex]
			if c.Alpha != 0 {
				result = append(result, ColorLayer{ID: font.GID(layer.GlyphID), Color: color.NRGBA{R: c.Red, G: c.Green, B: c.Blue, A: c.Alpha}})
			}
		}
	}
	return result
}

func (f *Face) normalizeMetrics() {
	if f.Ascent <= 0 {
		f.Ascent = f.Upem * 0.8
	}
	if f.Descent >= 0 {
		f.Descent = -f.Upem * 0.2
	}
	if f.CapHeight <= 0 {
		f.CapHeight = f.Upem * 0.72
	}
}

func (f *Face) close() {
	if f != nil && f.file != nil {
		f.file.Close()
	}
}

func (f *Face) NominalGlyph(r rune) (font.GID, bool) {
	if f == nil {
		return 0, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sf == nil {
		return f.full.NominalGlyph(r)
	}
	if gid, ok := f.glyphs[r]; ok {
		return gid, gid != 0
	}
	idx, _ := f.sf.GlyphIndex(&f.buffer, r)
	if f.glyphs == nil {
		f.glyphs = make(map[rune]font.GID)
	} else if len(f.glyphs) >= 4096 {
		clear(f.glyphs)
	}
	gid := font.GID(idx)
	f.glyphs[r] = gid
	return gid, gid != 0
}

func (f *Face) HorizontalAdvance(gid font.GID) float32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sf == nil {
		return f.full.HorizontalAdvance(gid)
	}
	if width, ok := f.advances[gid]; ok {
		return width
	}
	width, _ := f.sf.GlyphAdvance(&f.buffer, sfnt.GlyphIndex(gid), fixed.I(int(f.Upem)), imagefont.HintingNone)
	if f.advances == nil {
		f.advances = make(map[font.GID]float32)
	} else if len(f.advances) >= 4096 {
		clear(f.advances)
	}
	advance := float32(width) / 64
	f.advances[gid] = advance
	return advance
}

// Outline returns segments and bounds in font units, with an upward Y axis.
func (f *Face) Outline(gid font.GID) ([]opentype.Segment, opentype.GlyphExtents) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sf == nil {
		ext, _ := f.full.GlyphExtents(gid)
		switch g := f.full.GlyphData(gid).(type) {
		case font.GlyphOutline:
			return g.Segments, ext
		case font.GlyphSVG:
			return g.Outline.Segments, ext
		}
		return nil, ext
	}
	// A fixed ppem keeps the reader's intermediate arithmetic in range
	// even for faces with large units-per-em values.
	const ppem = 256
	path, err := f.sf.LoadGlyph(&f.buffer, sfnt.GlyphIndex(gid), fixed.I(ppem), nil)
	if err != nil || len(path) == 0 {
		return nil, opentype.GlyphExtents{}
	}
	scale := f.Upem / (64 * ppem)
	segments := make([]opentype.Segment, len(path))
	for i, seg := range path {
		segments[i].Op = opentype.SegmentOp(seg.Op)
		for j, pt := range seg.Args {
			segments[i].Args[j] = opentype.SegmentPoint{X: float32(pt.X) * scale, Y: -float32(pt.Y) * scale}
		}
	}
	b := path.Bounds()
	return segments, opentype.GlyphExtents{
		XBearing: float32(b.Min.X) * scale,
		YBearing: -float32(b.Min.Y) * scale,
		Width:    float32(b.Max.X-b.Min.X) * scale,
		Height:   float32(b.Min.Y-b.Max.Y) * scale,
	}
}
