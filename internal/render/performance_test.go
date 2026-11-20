package render

import (
	"testing"

	"gon/internal/fontset"
)

func benchmarkFace(b *testing.B) *fontset.Face {
	b.Helper()
	family := fontset.LoadFamily("Consolas", nil)
	if family == nil || family.Nominal() == nil {
		b.Skip("no usable font available")
	}
	b.Cleanup(family.Close)
	return family.Nominal()
}

func BenchmarkRasterize(b *testing.B) {
	for _, bold := range []bool{false, true} {
		name := "Regular"
		if bold {
			name = "SyntheticBold"
		}
		b.Run(name, func(b *testing.B) {
			face := benchmarkFace(b)
			gid, ok := face.NominalGlyph('M')
			if !ok {
				b.Skip("no glyph for M")
			}
			key := Key{GID: gid, PPEM: 32, Bold: bold}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mask := rasterize(face, key); mask == nil || len(mask.pix) == 0 {
					b.Fatal("missing glyph coverage")
				}
			}
		})
	}
}
