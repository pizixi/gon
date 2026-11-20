package ui

import (
	"math"
	"image/color"
	"strconv"
	"strings"
)

// Theme holds resolved colors for the chrome around the terminal.
type Theme struct {
	Bg        uint32 // window background
	Fg        uint32 // primary text
	Dim       uint32 // secondary text
	Accent    uint32 // active highlight
	Panel     uint32 // panel background
	PanelHi   uint32 // hovered panel
	Border    uint32
	Selection uint32
	Cursor    uint32
	TermBg    uint32
	TermFg    uint32
	Palette   [16]uint32
}

// ParseColor parses "#rrggbb" or "#rgb" into 0xRRGGBB.
func ParseColor(s string, fallback uint32) uint32 {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	switch len(s) {
	case 3:
		r, err1 := strconv.ParseUint(s[0:1], 16, 8)
		g, err2 := strconv.ParseUint(s[1:2], 16, 8)
		b, err3 := strconv.ParseUint(s[2:3], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil {
			return fallback
		}
		return uint32(r*17)<<16 | uint32(g*17)<<8 | uint32(b*17)
	case 6:
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return fallback
		}
		return uint32(v)
	}
	return fallback
}

// NRGBA converts 0xRRGGBB + alpha into a color.
func NRGBA(c uint32, a uint8) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: a}
}

// mix blends two 0xRRGGBB colors; t is the weight of b.
func mix(a, b uint32, t float32) uint32 {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	ch := func(shift uint) uint32 {
		x := float32((a >> shift) & 0xff)
		y := float32((b >> shift) & 0xff)
		return uint32(x*(1-t) + y*t + 0.5)
	}
	return ch(16)<<16 | ch(8)<<8 | ch(0)
}

// lighten moves c toward white by t.
func lighten(c uint32, t float32) uint32 { return mix(c, 0xffffff, t) }

// darken moves c toward black by t.
func darken(c uint32, t float32) uint32 { return mix(c, 0x000000, t) }

// withAlpha returns c with the given alpha.
func withAlpha(c uint32, a uint8) color.NRGBA { return NRGBA(c, a) }

// contrast picks black or white text for a background color.
func contrast(bg uint32) uint32 {
	r := float32((bg >> 16) & 0xff)
	g := float32((bg >> 8) & 0xff)
	b := float32(bg & 0xff)
	// Rec. 709 luma.
	if 0.2126*r+0.7152*g+0.0722*b > 140 {
		return 0x101010
	}
	return 0xf2f6fa
}

// perfAlpha converts a perceptual opacity (0-1) to the alpha Gio needs.
// Gio blends colours in linear space, so a raw alpha looks far more
// transparent than the same alpha in sRGB; this compensates.
func perfAlpha(p float64) uint8 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 0xff
	}
	return uint8(255*(1-math.Pow(1-p, 2.2)) + 0.5)
}
