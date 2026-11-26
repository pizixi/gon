package ui

import (
	"image"
	"math"
	"slices"
)

// A tab group has its own split tree. Each leaf keeps its tab's pane tree.
type tabGroup struct {
	root *paneTile
}

func (t *Tab) shownWith(active *Tab) bool {
	return t != nil && active != nil && (t == active || t.group != nil && t.group == active.group)
}

func (t *Tab) viewportRoot() *paneTile {
	if t == nil {
		return nil
	}
	if t.group != nil {
		return t.group.root
	}
	return t.root
}

func (n *paneTile) findTab(t *Tab) *paneTile {
	if n.first == nil {
		if n.tab == t {
			return n
		}
		return nil
	}
	if leaf := n.first.findTab(t); leaf != nil {
		return leaf
	}
	return n.second.findTab(t)
}

func (n *paneTile) tabAt(at image.Point) *paneTile {
	if !at.In(n.area) {
		return nil
	}
	if n.tab != nil {
		return n
	}
	if leaf := n.first.tabAt(at); leaf != nil {
		return leaf
	}
	return n.second.tabAt(at)
}

// leaveGroup detaches this tab and returns a tab next to the vacated tile.
func (t *Tab) leaveGroup() *Tab {
	g := t.group
	if g == nil {
		return nil
	}
	root, neighbor := g.root.without(g.root.findTab(t))
	g.root = root
	t.group = nil
	if root != nil && root.tab != nil {
		root.tab.group = nil
	}
	if neighbor != nil {
		return neighbor.tab
	}
	return nil
}

type tabPlacement struct {
	target *Tab
	mode   SplitMode
	before bool
	bounds image.Rectangle
}

func (d tabPlacement) side() string {
	if d.mode == SplitRight {
		if d.before {
			return "左侧"
		}
		return "右侧"
	}
	if d.before {
		return "上方"
	}
	return "下方"
}

func (a *App) tabDrop(other *Tab, at image.Point) tabPlacement {
	active, area := a.activeTab(), a.paneArea()
	if active == nil || !at.In(area) {
		return tabPlacement{}
	}
	target := active
	if active.group != nil {
		root := active.group.root
		root.layout(area, a.minPaneSize)
		leaf := root.tabAt(at)
		if leaf == nil {
			return tabPlacement{}
		}
		target, area = leaf.tab, leaf.area
	}
	dx, dy := 2*(at.X-area.Min.X)-area.Dx(), 2*(at.Y-area.Min.Y)-area.Dy()
	mode, before := SplitDown, dy < 0
	if int64(iabs(dx))*int64(area.Dy()) >= int64(iabs(dy))*int64(area.Dx()) {
		mode, before = SplitRight, dx < 0
	}
	bounds := a.tabSplitBounds(target, other, mode, before)
	return tabPlacement{target: target, mode: mode, before: before, bounds: bounds}
}

func (a *App) tabSplitBounds(target, other *Tab, mode SplitMode, before bool) image.Rectangle {
	active := a.activeTab()
	if !target.shownWith(active) || other == nil || target.root == nil || other.root == nil || target.shownWith(other) {
		return image.Rectangle{}
	}
	root := active.viewportRoot()
	root.measure(a.minPaneSize)
	tile := target.root
	if target.group != nil {
		tile = root.findTab(target)
	}
	return root.splitPreview(a.paneArea(), tile, other.root.measure(a.minPaneSize), mode, before)
}

func (a *App) splitTab(target, other *Tab, mode SplitMode, before bool) {
	index := slices.Index(a.tabs, other)
	active := a.activeTab()
	if index < 0 || !target.shownWith(active) {
		return
	}
	if a.tabSplitBounds(target, other, mode, before).Empty() {
		a.notify("空间不足，无法分屏显示此标签")
		return
	}
	other.leaveGroup()
	group := target.group
	if group == nil {
		group = &tabGroup{root: &paneTile{tab: target}}
		target.group = group
	}
	tile := group.root.findTab(target)
	first, second := &paneTile{tab: target}, &paneTile{tab: other}
	if before {
		first, second = second, first
	}
	*tile = paneTile{first: first, second: second, mode: mode, ratio: 0.5}
	other.group = group
	a.activateTab(index)
	a.invalidate()
}

func (a *App) showTabAlone(tab *Tab) {
	index := slices.Index(a.tabs, tab)
	if index < 0 || tab.group == nil {
		return
	}
	tab.leaveGroup()
	a.resetPaneGesture()
	a.activateTab(index)
	a.invalidate()
}

func (a *App) focusPane(p *Pane) bool {
	t := p.tab
	active := a.activeTab()
	if !t.shownWith(active) {
		return false
	}
	changed := t.focusByPane(p)
	if t != active {
		a.activateTab(slices.Index(a.tabs, t))
		return true
	}
	if changed {
		a.closeSearch()
	}
	return changed
}

func (a *App) openTabMenu(tab *Tab, at image.Point) {
	a.menuOpen, a.menuTab, a.menuAt = true, tab, at
	a.menuScroll = 0
	a.tabDrag = tabDrag{}
	a.invalidate()
}

func (a *App) closeMenu() {
	a.menuOpen, a.menuTab = false, nil
}

type menuItem struct {
	label string
	run   func()
}

func (a *App) menuItems() []menuItem {
	if tab := a.menuTab; tab != nil {
		active := a.activeTab()
		var items []menuItem
		if active != nil && !tab.shownWith(active) {
			items = append(items,
				menuItem{"在当前标签右侧显示", func() { a.splitTab(active, tab, SplitRight, false) }},
				menuItem{"在当前标签下方显示", func() { a.splitTab(active, tab, SplitDown, false) }},
			)
		}
		if tab.group != nil {
			items = append(items, menuItem{"单独显示此标签", func() { a.showTabAlone(tab) }})
		}
		return append(items, menuItem{"关闭标签页", func() { a.CloseTab(slices.Index(a.tabs, tab)) }})
	}

	items := []menuItem{{"设置", a.openSettings}}
	for i, sh := range a.cfg.Shells {
		items = append(items, menuItem{
			label: "新建 " + shellDisplayName(sh.Name, sh.Command),
			run: func() {
				if _, err := a.NewTab(i); err != nil {
					a.notify("启动失败：" + err.Error())
				}
			},
		})
	}
	return append(items,
		menuItem{"向右分屏", func() { a.SplitActive(SplitRight) }},
		menuItem{"向下分屏", func() { a.SplitActive(SplitDown) }},
		menuItem{"关闭当前窗格", a.CloseFocusedPane},
		menuItem{"复制选中文本", a.copySelection},
		menuItem{"粘贴", a.requestPaste},
	)
}

func paneDragTarget(p *Pane, pos image.Point, fromFooter bool) *Pane {
	t := p.tab
	if target := t.paneAt(pos); target != nil {
		if target != p {
			return target
		}
		return nil
	}
	if !fromFooter || t.group == nil {
		return nil
	}
	root := t.group.root
	leaf := root.tabAt(pos)
	if leaf == nil || !root.canSwapTabs(root.findTab(t), leaf) {
		return nil
	}
	return leaf.tab.paneAt(pos)
}

func (n *paneTile) canSwapTabs(first, second *paneTile) bool {
	if first == nil || second == nil || first == second {
		return false
	}
	if first.minimum == second.minimum {
		return true
	}
	need := n.minimumAfterSwap(first, second)
	return need.X <= max(n.area.Dx(), n.minimum.X) && need.Y <= max(n.area.Dy(), n.minimum.Y)
}

func (n *paneTile) minimumAfterSwap(first, second *paneTile) image.Point {
	switch {
	case n == first:
		return second.minimum
	case n == second:
		return first.minimum
	case n.first == nil:
		return n.minimum
	default:
		return splitMinimum(n.mode, n.first.minimumAfterSwap(first, second), n.second.minimumAfterSwap(first, second))
	}
}

func swapDraggedPanes(src, dst *Pane) bool {
	if dst == nil {
		return false
	}
	if src.tab == dst.tab {
		return src.tab.swapPanes(src, dst)
	}
	root := src.tab.group.root
	first, second := root.findTab(src.tab), root.findTab(dst.tab)
	// Moving across tabs swaps entire tiles and keeps their panes together.
	first.tab, second.tab = second.tab, first.tab
	return true
}

func (a *App) focusAdjacentPane(dx, dy int) {
	active := a.activeTab()
	if active == nil {
		return
	}
	if active.group == nil {
		if active.FocusNeighbor(dx, dy) {
			a.closeSearch()
			a.invalidate()
		}
		return
	}
	cur := active.Focused()
	if cur == nil {
		return
	}
	center := cur.Rect().Min.Add(cur.Rect().Size().Div(2))
	var best *Pane
	bestScore := math.MaxInt
	for _, t := range a.tabs {
		if !t.shownWith(active) {
			continue
		}
		for _, p := range t.Panes {
			if score := paneFocusDistance(center, p.Rect(), dx, dy); score < bestScore {
				best, bestScore = p, score
			}
		}
	}
	if best != nil && a.focusPane(best) {
		a.invalidate()
	}
}
