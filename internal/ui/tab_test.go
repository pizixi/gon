package ui

import (
	"image"
	"testing"
)

func assertTiled(t *testing.T, tab *Tab, area image.Rectangle) {
	t.Helper()
	pixels := 0
	for i, pane := range tab.Panes {
		r := pane.Rect()
		if r.Empty() || !r.In(area) {
			t.Fatalf("pane %d rect = %v outside %v", i, r, area)
		}
		for _, other := range tab.Panes[:i] {
			if r.Overlaps(other.Rect()) {
				t.Fatalf("pane %v overlaps %v", r, other.Rect())
			}
		}
		pixels += r.Dx() * r.Dy()
	}
	if want := area.Dx() * area.Dy(); pixels != want {
		t.Fatalf("panes cover %d of %d pixels", pixels, want)
	}
}

func TestRelayoutUsesSharedBoundaries(t *testing.T) {
	tb := new(Tab)
	left, top, bottom := new(Pane), new(Pane), new(Pane)
	tb.addPane(left, SplitRight)
	tb.addPane(top, SplitRight)
	tb.addPane(bottom, SplitDown)
	area := image.Rect(100, 50, 501, 451)
	tb.Relayout(area, image.Pt(88, 76))
	assertTiled(t, tb, area)
	for pane, want := range map[*Pane]image.Rectangle{
		left:   image.Rect(100, 50, 301, 451),
		top:    image.Rect(301, 50, 501, 251),
		bottom: image.Rect(301, 251, 501, 451),
	} {
		if got := pane.Rect(); got != want {
			t.Errorf("pane = %v, want %v", got, want)
		}
	}
}

func TestRemovePaneExpandsSurvivingBranch(t *testing.T) {
	tb := new(Tab)
	left, top, bottom := new(Pane), new(Pane), new(Pane)
	tb.addPane(left, SplitRight)
	tb.addPane(top, SplitRight)
	tb.addPane(bottom, SplitDown)
	area, minimum := image.Rect(210, 38, 1013, 641), image.Pt(88, 76)

	tb.removePane(left)
	tb.Relayout(area, minimum)
	assertTiled(t, tb, area)
	if top.Rect().Dx() != area.Dx() || bottom.Rect().Dx() != area.Dx() || tb.Focused() != bottom {
		t.Fatalf("surviving branch = %v and %v, focus = %p", top.Rect(), bottom.Rect(), tb.Focused())
	}
	tb.removePane(bottom)
	tb.Relayout(area, minimum)
	if top.Rect() != area || tb.Focused() != top {
		t.Fatalf("remaining pane = %v, focus = %p", top.Rect(), tb.Focused())
	}
}

func TestResizePreservesMinimumsAndAdaptsToWindow(t *testing.T) {
	tb := new(Tab)
	left, middle, right := new(Pane), new(Pane), new(Pane)
	tb.addPane(left, SplitRight)
	tb.addPane(middle, SplitRight)
	tb.addPane(right, SplitRight)
	area, minimum := image.Rect(210, 38, 1013, 641), image.Pt(88, 76)
	tb.Relayout(area, minimum)
	tb.root.resize(area.Dx() + 100)
	tb.Relayout(area, minimum)
	assertTiled(t, tb, area)
	if middle.Rect().Dx() != minimum.X || right.Rect().Dx() != minimum.X {
		t.Fatalf("nested panes shrank too far: %v and %v", middle.Rect(), right.Rect())
	}
	before := left.Rect()

	smaller := image.Rect(140, 38, 345, 481)
	tb.Relayout(smaller, minimum)
	assertTiled(t, tb, smaller)
	tb.Relayout(area, minimum)
	assertTiled(t, tb, area)
	if left.Rect() != before {
		t.Fatalf("restored pane = %v, want %v", left.Rect(), before)
	}
}

func TestFocusAtAndNeighbor(t *testing.T) {
	tb := new(Tab)
	left, right := new(Pane), new(Pane)
	tb.addPane(left, SplitRight)
	tb.addPane(right, SplitRight)
	tb.focusByPane(left)
	tb.Relayout(image.Rect(0, 0, 400, 100), image.Pt(88, 76))
	if !tb.focusByPane(tb.paneAt(image.Pt(200, 50))) || tb.Focused() != right {
		t.Fatal("focus did not move to the right pane")
	}
	if !tb.FocusNeighbor(-1, 0) || tb.Focused() != left {
		t.Fatal("focus did not move back to the left pane")
	}
}

func TestSplitPreviewMakesRoomInEarlierPanes(t *testing.T) {
	tb := new(Tab)
	minimum := image.Pt(88, 76)
	area := image.Rect(0, 0, 6*minimum.X, 300)
	tb.addPane(new(Pane), SplitRight)
	for count := 2; count <= 6; count++ {
		tb.Relayout(area, minimum)
		tile := tb.root.find(tb.Focused())
		preview := tb.root.splitPreview(area, tile, minimum, SplitRight, false)
		if preview.Empty() {
			t.Fatalf("layout could not preview pane %d", count)
		}
		newPane := new(Pane)
		tb.addPane(newPane, SplitRight)
		tb.Relayout(area, minimum)
		assertTiled(t, tb, area)
		if newPane.Rect() != preview || preview.Dx() < minimum.X {
			t.Fatalf("pane %d got %v, with preview %v", count, newPane.Rect(), preview)
		}
	}
	if got := tb.root.splitPreview(area, tb.root.find(tb.Focused()), minimum, SplitRight, false); !got.Empty() {
		t.Fatalf("preview outside window limits: %v", got)
	}
}

func TestCycleFocus(t *testing.T) {
	tb := &Tab{Panes: []*Pane{{}, {}, {}}, Focus: 0}
	if !tb.CycleFocus(1) || tb.Focus != 1 {
		t.Fatalf("focus = %d, want 1", tb.Focus)
	}
	tb.CycleFocus(-2)
	if tb.Focus != 2 {
		t.Fatalf("focus = %d, want 2", tb.Focus)
	}
}
