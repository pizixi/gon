package term

// Style is a resolved cell style: concrete colors after reverse/dim handling.
type Style struct {
	FG, BG uint32 // 0xRRGGBB
	Flags  uint16
}

// Palette maps terminal colors onto RGB values.
type Palette struct {
	Fg, Bg uint32
	// Indexed maps 0..255 to 0xRRGGBB.
	Indexed [256]uint32
}

// NewPalette builds the default xterm palette with the given defaults.
func NewPalette(fg, bg uint32) *Palette {
	p := &Palette{Fg: fg, Bg: bg}
	base := [16]uint32{
		0x0c0c0c, 0xc50f1f, 0x13a10e, 0xc19c00,
		0x0037da, 0x881798, 0x3a96dd, 0xcccccc,
		0x767676, 0xe74856, 0x16c60c, 0xf9f1a5,
		0x3b78ff, 0xb4009e, 0x61d6d6, 0xf2f2f2,
	}
	copy(p.Indexed[:16], base[:])
	for i := 0; i < 216; i++ {
		r := i / 36
		g := (i / 6) % 6
		b := i % 6
		p.Indexed[16+i] = uint32(step(r))<<16 | uint32(step(g))<<8 | uint32(step(b))
	}
	for i := 0; i < 24; i++ {
		v := uint32(8 + i*10)
		p.Indexed[232+i] = v<<16 | v<<8 | v
	}
	return p
}

func step(v int) int {
	if v == 0 {
		return 0
	}
	return 55 + v*40
}

// SetIndexed overrides a palette entry.
func (p *Palette) SetIndexed(i int, rgb uint32) {
	if i >= 0 && i < 256 {
		p.Indexed[i] = rgb
	}
}

func (p *Palette) resolve(c Color) uint32 {
	switch c.Mode {
	case ColorIndexed:
		return p.Indexed[c.Idx]
	case ColorRGB:
		return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	}
	return 0
}

// StyleOf resolves a cell attribute into concrete colors.
func (p *Palette) StyleOf(a Attr) Style {
	st := Style{Flags: a.Flags}
	if a.FG.Mode == ColorDefault {
		st.FG = p.Fg
	} else {
		st.FG = p.resolve(a.FG)
	}
	if a.BG.Mode == ColorDefault {
		st.BG = p.Bg
	} else {
		st.BG = p.resolve(a.BG)
	}
	if a.Has(AttrBold) && a.FG.Mode == ColorIndexed && a.FG.Idx < 8 {
		st.FG = p.Indexed[a.FG.Idx+8]
	}
	if a.Has(AttrReverse) {
		st.FG, st.BG = st.BG, st.FG
	}
	if a.Has(AttrInvisible) {
		st.FG = st.BG
	}
	if a.Has(AttrDim) {
		st.FG = dim(st.FG, 0x66)
	}
	return st
}

func dim(c uint32, k uint32) uint32 {
	r := (c >> 16) & 0xff
	g := (c >> 8) & 0xff
	b := c & 0xff
	r = r * k / 255
	g = g * k / 255
	b = b * k / 255
	return r<<16 | g<<8 | b
}

// Mix blends two colors, a in [0,1].
func Mix(c1, c2 uint32, a float32) uint32 {
	r := blend((c1>>16)&0xff, (c2>>16)&0xff, a)
	g := blend((c1>>8)&0xff, (c2>>8)&0xff, a)
	b := blend(c1&0xff, c2&0xff, a)
	return r<<16 | g<<8 | b
}

func blend(a, b uint32, t float32) uint32 {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return uint32(float32(a)*(1-t) + float32(b)*t + 0.5)
}
