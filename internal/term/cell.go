package term

// ColorMode selects how a Color is interpreted.
type ColorMode uint8

const (
	// ColorDefault means "use the theme default".
	ColorDefault ColorMode = iota
	// ColorIndexed refers to the 256 color palette.
	ColorIndexed
	// ColorRGB is a direct 24 bit color.
	ColorRGB
)

// Color is a terminal color.
type Color struct {
	Mode    ColorMode
	Idx     uint8
	R, G, B uint8
}

// DefaultColor is the theme default foreground/background.
var DefaultColor = Color{}

// Indexed returns a palette color.
func Indexed(i int) Color { return Color{Mode: ColorIndexed, Idx: uint8(i)} }

// RGB returns a direct color.
func RGB(r, g, b int) Color {
	return Color{Mode: ColorRGB, R: uint8(clamp(r)), G: uint8(clamp(g)), B: uint8(clamp(b))}
}

// RGBOr resolves a truecolor override against its configured default.
func (c Color) RGBOr(fallback uint32) uint32 {
	if c.Mode != ColorRGB {
		return fallback
	}
	return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// Text attribute flags.
const (
	AttrBold uint16 = 1 << iota
	AttrDim
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrInvisible
	AttrStrike
)

// Attr is the per-cell style.
type Attr struct {
	FG, BG Color
	Flags  uint16
}

// Has reports whether a flag is set.
func (a Attr) Has(f uint16) bool { return a.Flags&f != 0 }

// Cell is one character cell. W is 0 for the right half of a wide rune,
// 1 for a normal rune and 2 for the left half of a wide rune.
type Cell struct {
	R         rune   // the rune value
	W         uint8  // cell width in pixels (1 or 2 for wide chars)
	A         Attr   // attributes (colors, bold, etc.)
	Cont      bool   // continues across cell boundary (used for wide chars)
	Combining string // remaining codepoints in the same grapheme
}

// Text is the grapheme represented by the leading cell.
func (c Cell) Text() string {
	if c.Cont {
		return ""
	}
	if c.R == 0 {
		return " "
	}
	return string(c.R) + c.Combining
}

// Blank is an empty, unstyled cell.
var Blank = Cell{R: ' ', W: 1}

// IsBlank reports whether the cell contains nothing visible.
func (c Cell) IsBlank() bool { return (c.R == 0 || c.R == ' ') && c.Combining == "" && c.A == Attr{} }

// decGraphics is the DEC Special Graphics (charset "0") mapping.
var decGraphics = [32]rune{
	' ', '◆', '▒', '␉', '␌', '␍', '␊',
	'°', '±', '␋', '┘', '┐', '┌', '└', '┼', '⎺',
	'⎻', '─', '⎼', '⎽', '├', '┤', '┴', '┬',
	'│', '≤', '≥', 'π', '≠', '£', '·', '⎷',
}

func decGraphic(r rune) rune {
	if r >= 0x5f && r <= 0x7e {
		return decGraphics[r-0x5f]
	}
	return r
}
