package ui

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"gon/internal/term"
)

func TestFindPreservesCellColumns(t *testing.T) {
	p := newInputPane()
	term.NewParser(p.scr).Write([]byte("A中e\u0301👩🏻‍💻Z\r\nALPHA alpha\r\nababababac"))
	for _, tc := range []struct {
		query    string
		from     int
		forward  bool
		line, c0 int
		c1       int
	}{
		{"中", -1, true, 0, 1, 2},
		{"e\u0301", -1, true, 0, 3, 3},
		{"\u0301👩", -1, true, 0, 3, 5},
		{"🏻‍💻z", -1, true, 0, 4, 6},
		{"alpha", -1, true, 1, 0, 4},
		{"alpha", 1, true, 1, 0, 4},
		{"alpha", -1, false, 1, 0, 4},
		{"ababac", -1, true, 2, 4, 9},
		{" ", -1, true, 0, 7, 7},
	} {
		line, c0, c1, ok := p.Find(tc.query, tc.from, tc.forward)
		if !ok || line != tc.line || c0 != tc.c0 || c1 != tc.c1 {
			t.Errorf("Find(%q, %d, %t) = %d, %d..%d, %t; want %d, %d..%d", tc.query, tc.from, tc.forward,
				line, c0, c1, ok, tc.line, tc.c0, tc.c1)
		}
	}
}

// Compare search order, wrapping and Unicode positions with a simple rune scan.
func TestFindMatchesReference(t *testing.T) {
	p := &Pane{scr: term.NewScreen(24, 4, 12), cols: 24, rows: 4}
	rng := rand.New(rand.NewSource(1))
	clusters := []string{"a", "A", "b", " ", "中", "e\u0301", "👩🏻‍💻", "İ", "ẞ", "K", "ſ"}
	var data strings.Builder
	for i := 0; i < 24; i++ {
		for j := 0; j < 8; j++ {
			data.WriteString(clusters[rng.Intn(len(clusters))])
		}
		data.WriteString("\r\n")
	}
	term.NewParser(p.scr).Write([]byte(data.String()))
	queries := append([]string{"", "missing", "aaab", "中e", "\u0301", "🏻‍", "i", "k", "s", "ss", "�"}, clusters...)
	for _, query := range queries {
		for from := -2; from <= p.scr.TotalLines()+1; from++ {
			for _, forward := range []bool{false, true} {
				wantLine, wantC0, wantC1, wantOK := referenceFind(p.scr, query, from, forward)
				line, c0, c1, ok := p.Find(query, from, forward)
				if line != wantLine || c0 != wantC0 || c1 != wantC1 || ok != wantOK {
					t.Fatalf("Find(%q, %d, %t) = %d, %d..%d, %t; want %d, %d..%d, %t", query, from, forward,
						line, c0, c1, ok, wantLine, wantC0, wantC1, wantOK)
				}
			}
		}
	}
}

func referenceFind(scr *term.Screen, query string, from int, forward bool) (int, int, int, bool) {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 0, 0, 0, false
	}
	total := scr.TotalLines()
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < total; i++ {
			line := from - 1 - i
			if forward {
				line = from + 1 + i
			}
			if forward && line >= total || !forward && line < 0 {
				break
			}
			cells := scr.AbsLine(line)
			var text []rune
			var cols []int
			for col, cell := range cells {
				for _, r := range cell.Text() {
					text, cols = append(text, unicode.ToLower(r)), append(cols, col)
				}
			}
			for at := 0; at+len(q) <= len(text); at++ {
				if string(text[at:at+len(q)]) == string(q) {
					return line, cols[at], trailingColumn(cells, cols[at+len(q)-1]), true
				}
			}
		}
		from = total
		if forward {
			from = -1
		}
	}
	return 0, 0, 0, false
}

func BenchmarkFindHistory(b *testing.B) {
	for _, workload := range []struct{ name, line, query string }{
		{"ASCII", "INFO request completed successfully in 12ms", "missing"},
		{"Unicode", "处理完成：中文日志 e\u0301 👩🏻‍💻 请求耗时 12ms", "不存在"},
		{"RepeatedPrefix", strings.Repeat("a", 110), strings.Repeat("a", 48) + "b"},
	} {
		b.Run(workload.name, func(b *testing.B) {
			p := &Pane{scr: term.NewScreen(120, 24, 8000), cols: 120, rows: 24}
			term.NewParser(p.scr).Write([]byte(strings.Repeat(workload.line+"\r\n", 8024)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, _, ok := p.Find(workload.query, -1, true); ok {
					b.Fatal("unexpected match")
				}
			}
		})
	}
}
