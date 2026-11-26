package term

import "testing"

var logLine = []byte("INFO job finished successfully in 42 ms\r\n")

func BenchmarkScrollOutput(b *testing.B) {
	const history = 8000
	s := NewScreen(120, 48, history)
	p := NewParser(s)
	for range history + s.Rows {
		p.Write(logLine)
	}
	b.ReportAllocs()
	for b.Loop() {
		p.Write(logLine)
	}
}

func BenchmarkFillScrollback(b *testing.B) {
	const history = 8000
	b.ReportAllocs()
	for b.Loop() {
		s := NewScreen(120, 48, history)
		p := NewParser(s)
		for range history + s.Rows {
			p.Write(logLine)
		}
	}
}
