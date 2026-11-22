package ui

import (
	"image"
	"math"
	"slices"

	"gioui.org/widget"

	"gon/internal/launch"
)

// Tab holds the terminal sessions and their split layout.
type Tab struct {
	Title string
	// Panes keeps creation order for focus cycling.
	Panes       []*Pane
	Focus       int
	root        *paneTile
	launch      launch.Context
	closeButton widget.Clickable
	closeShown  bool
}

const MaxPanes = 4

func (t *Tab) CanSplit() bool { return len(t.Panes) < MaxPanes }

func (t *Tab) Focused() *Pane {
	if t.Focus < 0 || t.Focus >= len(t.Panes) {
		return nil
	}
	return t.Panes[t.Focus]
}

func (t *Tab) paneAt(pt image.Point) *Pane {
	for _, p := range t.Panes {
		if pt.In(p.Rect()) {
			return p
		}
	}
	return nil
}

func (t *Tab) focusByPane(p *Pane) bool {
	return t.focusIndex(slices.Index(t.Panes, p))
}

func (t *Tab) focusIndex(i int) bool {
	if i < 0 || i >= len(t.Panes) || i == t.Focus {
		return false
	}
	t.Focus = i
	return true
}

// SplitMode selects the axis along which two adjacent panes are arranged.
type SplitMode int

const (
	SplitRight SplitMode = iota
	SplitDown
)

func (t *Tab) addPane(p *Pane, mode SplitMode) {
	newTile := &paneTile{pane: p}
	if t.root == nil {
		t.root = newTile
	} else {
		target := t.root.find(t.Focused())
		*target = paneTile{
			mode:   mode,
			ratio:  0.5,
			first:  &paneTile{pane: target.pane},
			second: newTile,
		}
	}
	t.Panes = append(t.Panes, p)
	t.Focus = len(t.Panes) - 1
}

// removePane replaces a split with its surviving branch and closes the session.
func (t *Tab) removePane(p *Pane) {
	i := slices.Index(t.Panes, p)
	if i < 0 {
		return
	}
	focused := t.Focused()
	root, focusedNeighbor := t.root.without(p)
	t.root = root
	t.Panes = slices.Delete(t.Panes, i, i+1)
	if focused == p || focused == nil {
		focused = focusedNeighbor
	}
	t.Focus = slices.Index(t.Panes, focused)
	p.Close()
}

func (t *Tab) swapPanes(src, dst *Pane) {
	if src == nil || dst == nil || src == dst {
		return
	}
	a, b := t.root.find(src), t.root.find(dst)
	if a != nil && b != nil {
		a.pane, b.pane = b.pane, a.pane
	}
}

func (t *Tab) Relayout(area image.Rectangle, minimum image.Point) {
	if t.root != nil {
		t.root.layout(area, minimum)
	}
}

// FocusNeighbor moves focus to the nearest pane in the given direction.
func (t *Tab) FocusNeighbor(dx, dy int) bool {
	cur := t.Focused()
	if cur == nil || len(t.Panes) < 2 {
		return false
	}
	center := cur.Rect().Min.Add(cur.Rect().Size().Div(2))
	best, bestScore := -1, math.MaxInt
	for i, p := range t.Panes {
		rect := p.Rect()
		delta := rect.Min.Add(rect.Size().Div(2)).Sub(center)
		if (dx != 0 && dx*delta.X <= 0) || (dy != 0 && dy*delta.Y <= 0) {
			continue
		}
		primary, secondary := iabs(delta.X), iabs(delta.Y)
		if dy != 0 {
			primary, secondary = secondary, primary
		}
		if score := 5*primary + 3*secondary; score < bestScore {
			best, bestScore = i, score
		}
	}
	return t.focusIndex(best)
}

func (t *Tab) CycleFocus(delta int) bool {
	if len(t.Panes) < 2 {
		return false
	}
	i := (t.Focus + delta) % len(t.Panes)
	if i < 0 {
		i += len(t.Panes)
	}
	return t.focusIndex(i)
}

// A tile is either one pane or a split between two tiles. The ratio belongs to
// the divider, so both sides use the same pixel boundary as the window changes.
type paneTile struct {
	pane   *Pane
	mode   SplitMode
	ratio  float64
	first  *paneTile
	second *paneTile
	area   image.Rectangle
}

func (n *paneTile) find(p *Pane) *paneTile {
	if n == nil {
		return nil
	}
	if n.pane != nil {
		if n.pane == p {
			return n
		}
		return nil
	}
	if found := n.first.find(p); found != nil {
		return found
	}
	return n.second.find(p)
}

func (n *paneTile) firstPane() *Pane {
	for n.pane == nil {
		n = n.first
	}
	return n.pane
}

func (n *paneTile) without(p *Pane) (*paneTile, *Pane) {
	if n.pane != nil {
		if n.pane == p {
			return nil, nil
		}
		return n, nil
	}
	first, neighbor := n.first.without(p)
	if first == nil {
		return n.second, n.second.firstPane()
	}
	if neighbor != nil {
		n.first = first
		return n, neighbor
	}
	second, neighbor := n.second.without(p)
	if second == nil {
		return first, first.firstPane()
	}
	n.second = second
	return n, neighbor
}

func (n *paneTile) layout(area image.Rectangle, minimum image.Point) {
	if n.pane != nil {
		n.pane.SetRect(area)
		return
	}
	n.area = area
	first, second := splitPaneArea(area, n.mode, n.splitOffset(minimum))
	n.first.layout(first, minimum)
	n.second.layout(second, minimum)
}

func (n *paneTile) minimumSize(leaf image.Point) image.Point {
	if n.pane != nil {
		return leaf
	}
	first, second := n.first.minimumSize(leaf), n.second.minimumSize(leaf)
	if n.mode == SplitRight {
		return image.Pt(first.X+second.X, max(first.Y, second.Y))
	}
	return image.Pt(max(first.X, second.X), first.Y+second.Y)
}

func (n *paneTile) splitBounds(minimum image.Point) (int, int) {
	span := n.mode.axis(n.area.Size())
	first := n.mode.axis(n.first.minimumSize(minimum))
	second := n.mode.axis(n.second.minimumSize(minimum))
	if span < first+second {
		// A small window shares the available space proportionally among
		// branches, including branches with several panes along this axis.
		offset := int(math.Round(float64(span) * float64(first) / float64(first+second)))
		return offset, offset
	}
	return first, span - second
}

func (n *paneTile) splitOffset(minimum image.Point) int {
	lo, hi := n.splitBounds(minimum)
	offset := int(math.Round(float64(n.mode.axis(n.area.Size())) * n.ratio))
	return max(lo, min(hi, offset))
}

func (n *paneTile) resize(offset int, minimum image.Point) {
	lo, hi := n.splitBounds(minimum)
	if lo < hi {
		offset = max(lo, min(hi, offset))
		if offset != n.splitOffset(minimum) {
			n.ratio = float64(offset) / float64(n.mode.axis(n.area.Size()))
		}
	}
}

func (m SplitMode) axis(pt image.Point) int {
	if m == SplitRight {
		return pt.X
	}
	return pt.Y
}

func splitPaneArea(area image.Rectangle, mode SplitMode, offset int) (image.Rectangle, image.Rectangle) {
	first, second := area, area
	if mode == SplitRight {
		first.Max.X = area.Min.X + offset
		second.Min.X = first.Max.X
	} else {
		first.Max.Y = area.Min.Y + offset
		second.Min.Y = first.Max.Y
	}
	return first, second
}
