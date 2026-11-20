package fontset

import (
	"testing"
)

func TestLoadFamilyFindsAMonospaceFace(t *testing.T) {
	f := LoadFamily("Consolas", nil)
	if f == nil || f.Nominal() == nil {
		t.Skip("Consolas not installed")
	}
	if f.Nominal().Upem <= 0 {
		t.Fatalf("bad upem %v", f.Nominal().Upem)
	}
	if gid, ok := f.Nominal().NominalGlyph('M'); !ok {
		t.Fatal("no glyph for M")
	} else if adv := f.Nominal().HorizontalAdvance(gid); adv <= 0 {
		t.Fatalf("bad advance %v", adv)
	}
}

func TestCJKFallback(t *testing.T) {
	f := LoadFamily("Consolas", []string{"msyh.ttc", "simhei.ttf", "simsun.ttc", "Deng.ttf"})
	if f == nil || f.Nominal() == nil {
		t.Skip("no base font")
	}
	face := f.Pick('中', false, false)
	if face == nil {
		t.Fatal("no face selected for CJK")
	}
	if _, ok := face.NominalGlyph('中'); !ok {
		t.Skip("no CJK fallback font installed")
	}
	if !IsWide('中') {
		t.Fatal("中 should be a wide rune")
	}
	if IsWide('a') {
		t.Fatal("'a' should not be wide")
	}
}

func TestPickFallsBackToRegular(t *testing.T) {
	f := LoadFamily("Consolas", nil)
	if f == nil || f.Nominal() == nil {
		t.Skip("Consolas not installed")
	}
	if got := f.Pick('x', true, true); got == nil {
		t.Fatal("bold-italic pick returned nil")
	}
}
