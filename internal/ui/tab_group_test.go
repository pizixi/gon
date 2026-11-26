package ui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"gon/internal/config"
)

func TestExistingTabsSplitAndDetach(t *testing.T) {
	a := New(config.Default(), nil, "")
	t.Cleanup(a.shutdown)
	a.started = true
	first, second, third := &Tab{Title: "First"}, &Tab{Title: "Second"}, &Tab{Title: "Third"}
	for _, tab := range []*Tab{first, second, third} {
		tab.addPane(newInputPane(), SplitRight)
	}
	a.tabs = []*Tab{first, second, third}
	a.activateTab(0)
	h := &layoutTest{App: a, size: image.Pt(1000, 640)}
	h.frame()
	tabCenter := func(label string) image.Point {
		row := h.controlBounds(t, label, 0)
		return row.Min.Add(row.Size().Div(2))
	}
	rightClick := func(at image.Point) {
		h.mouse(pointer.Press, at, pointer.ButtonSecondary)
		h.mouse(pointer.Release, at, 0)
	}
	drag := func(from, to image.Point) {
		h.mouse(pointer.Press, from, pointer.ButtonPrimary)
		h.mouse(pointer.Move, to, pointer.ButtonPrimary)
		h.mouse(pointer.Release, to, 0)
	}

	rightClick(tabCenter("Second"))
	h.clickControl(t, "在当前标签右侧显示", 0)
	h.frame()
	left, right := first.Focused(), second.Focused()
	if a.activeTab() != second || !left.visible.Load() || !right.visible.Load() || third.Focused().visible.Load() ||
		left.Rect().Max.X != right.Rect().Min.X || left.Rect().Union(right.Rect()) != a.paneArea() {
		t.Fatalf("first two tabs did not share the view: %v, %v", left.Rect(), right.Rect())
	}

	from := tabCenter("Third")
	to := image.Pt(right.Rect().Min.X+right.Rect().Dx()/2, right.Rect().Max.Y-8)
	h.mouse(pointer.Press, from, pointer.ButtonPrimary)
	h.mouse(pointer.Move, to, pointer.ButtonPrimary)
	if drop := a.tabDrop(third, a.tabDrag.at); !a.tabDrag.moving || drop.target != second ||
		drop.mode != SplitDown || drop.before || drop.bounds.Empty() || third.group != nil {
		t.Fatalf("incorrect drop preview: %+v", drop)
	}
	h.mouse(pointer.Release, to, 0)
	bottom := third.Focused()
	if first.group == nil || first.group != third.group || a.currentPane() != bottom || !bottom.visible.Load() ||
		right.Rect().Max.Y != bottom.Rect().Min.Y || right.Rect().Min.X != bottom.Rect().Min.X {
		t.Fatalf("third tab was not added below the second: %v, %v", right.Rect(), bottom.Rect())
	}

	h.router.Queue(key.Event{Name: key.NameLeftArrow, State: key.Press, Modifiers: key.ModAlt})
	h.frame()
	if a.currentPane() != left {
		t.Fatal("Alt+Left did not focus the neighboring tab")
	}
	h.click(right.presented.grid.Bounds.Min.Add(image.Pt(8, 8)))
	h.router.Queue(key.EditEvent{Text: "input for second"})
	h.frame()
	if a.activeTab() != second || takeWrite(t, right) != "input for second" || len(left.writeQ) != 0 || len(bottom.writeQ) != 0 {
		t.Fatal("input was not isolated to the clicked terminal")
	}

	cut := image.Pt(left.Rect().Max.X, left.Rect().Min.Y+80)
	drag(cut, cut.Sub(image.Pt(64, 0)))
	if left.Rect().Max.X != cut.X-64 || right.Rect().Min.X != cut.X-64 || bottom.Rect().Min.X != cut.X-64 {
		t.Fatal("dragging the main divider did not move all adjacent panes")
	}

	rightClick(tabCenter("Third"))
	h.clickControl(t, "单独显示此标签", 0)
	h.frame()
	if a.currentPane() != bottom || third.group != nil || bottom.Rect() != a.paneArea() ||
		left.visible.Load() || right.visible.Load() || len(a.tabs) != 3 {
		t.Fatal("showing one tab alone changed terminal ownership or left other tabs visible")
	}

	h.click(tabCenter("Second"))
	if a.currentPane() != right || !left.visible.Load() || !right.visible.Load() || bottom.visible.Load() {
		t.Fatal("returning to the group did not show its other tab")
	}
	h.click(image.Pt(left.Rect().Max.X-16, left.Rect().Min.Y+a.paneHeaderH/2))
	if a.currentPane() != right || second.group != nil || len(a.tabs) != 2 || left.visible.Load() ||
		right.Rect() != a.paneArea() {
		t.Fatal("closing a visible inactive pane closed or hid the wrong terminal")
	}
}

func TestTabDragShowsSplitHint(t *testing.T) {
	a := New(config.Default(), nil, "")
	t.Cleanup(a.shutdown)
	a.started = true
	first, second := &Tab{Title: "A"}, &Tab{Title: "B"}
	first.addPane(newInputPane(), SplitRight)
	second.addPane(newInputPane(), SplitRight)
	a.tabs = []*Tab{first, second}
	a.activateTab(0)
	h := &layoutTest{App: a, size: image.Pt(880, 580)}
	h.frame()

	withoutHint := func() {
		t.Helper()
		for _, node := range h.router.AppendSemantics(nil) {
			if strings.HasPrefix(node.Desc.Label, "松开后在") {
				t.Fatalf("unexpected split hint %q", node.Desc.Label)
			}
		}
	}
	row := h.controlBounds(t, "B", 0)
	from := row.Min.Add(row.Size().Div(2))
	h.mouse(pointer.Press, from, pointer.ButtonPrimary)
	withoutHint()

	area := a.paneArea()
	center := area.Min.Add(area.Size().Div(2))
	right := image.Pt(area.Max.X-12, center.Y)
	for _, edge := range []struct {
		at   image.Point
		side string
	}{
		{right, "右侧"},
		{image.Pt(center.X, area.Max.Y-12), "下方"},
		{image.Pt(area.Min.X+12, center.Y), "左侧"},
		{image.Pt(center.X, area.Min.Y+12), "上方"},
	} {
		h.mouse(pointer.Move, edge.at, pointer.ButtonPrimary)
		hint := h.controlBounds(t, "松开后在"+edge.side+"分屏显示「B」", 0)
		drop := a.tabDrop(second, edge.at)
		if hint.Empty() || !hint.In(drop.bounds) || a.activeTab() != first || second.group != nil {
			t.Fatalf("hint %v does not preview the intended split %v", hint, drop.bounds)
		}
	}
	h.mouse(pointer.Move, image.Pt(center.X, area.Min.Y-5), pointer.ButtonPrimary)
	withoutHint()

	h.mouse(pointer.Move, right, pointer.ButtonPrimary)
	hint := h.controlBounds(t, "松开后在右侧分屏显示「B」", 0)
	h.mouse(pointer.Release, hint.Min.Add(hint.Size().Div(2)), 0)
	withoutHint()
	if first.group == nil || first.group != second.group || a.activeTab() != second ||
		first.Focused().Rect().Max.X != second.Focused().Rect().Min.X {
		t.Fatal("releasing over the hint did not split the two sessions")
	}
}

func TestGroupCanKeepAddingTabsUntilTheWindowIsFull(t *testing.T) {
	minimum := image.Pt(88, 76)
	a := App{sidebarHidden: true, lastSize: image.Pt(6*minimum.X, 320), minPaneSize: minimum}
	for range 7 {
		tab := new(Tab)
		tab.addPane(newInputPane(), SplitRight)
		a.tabs = append(a.tabs, tab)
	}
	for i, tab := range a.tabs[1:6] {
		a.splitTab(a.activeTab(), tab, SplitRight, false)
		if a.activeTab() != tab || tab.group == nil {
			t.Fatalf("tab %d could not join with enough room: %s", i+2, a.toast)
		}
	}
	area := a.paneArea()
	a.activeTab().viewportRoot().layout(area, minimum)
	for i, tab := range a.tabs[:6] {
		want := image.Rect(i*minimum.X, area.Min.Y, (i+1)*minimum.X, area.Max.Y)
		if r := tab.Focused().Rect(); r != want {
			t.Fatalf("tab %d is at %v, want %v", i, r, want)
		}
	}
	last := a.tabs[6]
	a.splitTab(a.activeTab(), last, SplitRight, false)
	if last.group != nil || a.active != 5 {
		t.Fatal("adding a tab beyond the available width changed the current group")
	}
}

func TestTabGroupKeepsEachTabLayout(t *testing.T) {
	a := App{sidebarHidden: true, lastSize: image.Pt(720, 480), minPaneSize: image.Pt(88, 76)}
	first, second, third, fourth := new(Tab), new(Tab), new(Tab), new(Tab)
	for _, tab := range []*Tab{first, second, third, fourth} {
		tab.addPane(newInputPane(), SplitRight)
	}
	first.addPane(newInputPane(), SplitDown)
	first.root.ratio = 0.4
	a.tabs = []*Tab{first, second, third, fourth}
	a.splitTab(first, second, SplitRight, false)
	a.activateTab(2)
	a.splitTab(third, fourth, SplitDown, false)
	a.activateTab(0)
	a.splitTab(first, third, SplitDown, false)
	if fourth.group != nil || fourth.Focused().visible.Load() || third.group != first.group || third.group != second.group {
		t.Fatal("moving a tab between groups left stale membership")
	}
	first.viewportRoot().layout(a.paneArea(), a.minPaneSize)
	left := first.Panes[0].Rect()
	right := first.Panes[1].Rect()
	if first.root.ratio != 0.4 || left.Max.Y != right.Min.Y || right.Max.Y != third.Focused().Rect().Min.Y {
		t.Fatal("cross-tab splitting changed an inner split")
	}
	a.lastSize = image.Pt(140, 120)
	a.splitTab(first, fourth, SplitRight, false)
	if fourth.group != nil || a.activeTab() != third {
		t.Fatal("a split too small to fit changed the active view")
	}
}
