package render

import (
	"gioui.org/layout"
	"gioui.org/op"
	"image"
	"strings"
	"testing"

	"gon/internal/fontset"
	"gon/internal/term"
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

func BenchmarkDrawTerminal(b *testing.B) {
	for _, workload := range []struct{ name, line string }{
		{"Logs", "2026-11-26 12:00:00 INFO  worker 42 completed job 6803 with status=ok in 12ms (retry=0)"},
		{"Color", "\x1b[44;97m  ◎ build  \x1b[0m│ \x1b[32mtests passed\x1b[0m ├──── " + strings.Repeat("\x1b[7;1m A1 \x1b[0m ", 12)},
		{"Unicode", "终端渲染 中文 日本語 한국어 e\u0301 👩🏻‍💻 ✓ 处理完成。 " + strings.Repeat("输出正常 ", 5)},
	} {
		b.Run(workload.name, func(b *testing.B) {
			family := fontset.LoadFamily("Consolas", []string{"msyh.ttc", "seguiemj.ttf"})
			if family == nil || family.Nominal() == nil {
				b.Skip("no usable font available")
			}
			b.Cleanup(family.Close)
			v := NewView(family, 16)
			v.ShowCursor = false
			const cols, rows = 120, 42
			s := term.NewScreen(cols, rows, 0)
			term.NewParser(s).Write([]byte(strings.Repeat(workload.line+"\r\n", rows-1) + workload.line))
			m := v.Metrics()
			size := image.Pt(cols*m.CellW, rows*m.CellH)
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Constraints: layout.Exact(size)}
			draw := func() {
				ops.Reset()
				v.Draw(gtx, s, 0)
			}
			draw()
			b.ReportAllocs()
			for b.Loop() {
				draw()
			}
		})
	}
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
