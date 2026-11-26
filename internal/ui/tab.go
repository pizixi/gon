package ui

import (
	"image"
	"math"
	"slices"

	"gioui.org/widget"

	"gon/internal/pty"
)

// Tab holds the terminal sessions and their split layout.
type Tab struct {
	Title string
	// Panes keeps creation order for focus cycling.
	Panes       []*Pane
	Focus       int
	root        *paneTile
	group       *tabGroup
	launch      pty.Launch
	closeButton widget.Clickable
	closeShown  bool
}

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
	p.tab = t
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
	root, focusedNeighbor := t.root.without(t.root.find(p))
	t.root = root
	t.Panes = slices.Delete(t.Panes, i, i+1)
	if focused == p || focused == nil {
		focused = nil
		if focusedNeighbor != nil {
			focused = focusedNeighbor.pane
		}
	}
	t.Focus = slices.Index(t.Panes, focused)
	p.tab = nil
	p.Close()
}

func (t *Tab) swapPanes(src, dst *Pane) bool {
	if src == nil || dst == nil || src == dst {
		return false
	}
	a, b := t.root.find(src), t.root.find(dst)
	if a == nil || b == nil {
		return false
	}
	a.pane, b.pane = b.pane, a.pane
	return true
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
		if score := paneFocusDistance(center, p.Rect(), dx, dy); score < bestScore {
			best, bestScore = i, score
		}
	}
	return t.focusIndex(best)
}

func paneFocusDistance(center image.Point, r image.Rectangle, dx, dy int) int {
	delta := r.Min.Add(r.Size().Div(2)).Sub(center)
	if r.Empty() || (dx != 0 && dx*delta.X <= 0) || (dy != 0 && dy*delta.Y <= 0) {
		return math.MaxInt
	}
	primary, secondary := iabs(delta.X), iabs(delta.Y)
	if dy != 0 {
		primary, secondary = secondary, primary
	}
	return 5*primary + 3*secondary
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

// A tile holds a pane, a whole tab, or a split between two tiles. Both sides
// of a split share the same pixel boundary as the window changes.
type paneTile struct {
	pane    *Pane
	tab     *Tab
	mode    SplitMode
	ratio   float64
	first   *paneTile
	second  *paneTile
	area    image.Rectangle
	minimum image.Point
}

func (n *paneTile) find(p *Pane) *paneTile {
	if n == nil {
		return nil
	}
	if n.first == nil {
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

func (n *paneTile) firstLeaf() *paneTile {
	for n.first != nil {
		n = n.first
	}
	return n
}

func (n *paneTile) without(leaf *paneTile) (*paneTile, *paneTile) {
	if n == nil || n == leaf {
		return nil, nil
	}
	if n.first == nil {
		return n, nil
	}
	first, neighbor := n.first.without(leaf)
	if first == nil {
		return n.second, n.second.firstLeaf()
	}
	if neighbor != nil {
		n.first = first
		return n, neighbor
	}
	second, neighbor := n.second.without(leaf)
	if second == nil {
		return first, first.firstLeaf()
	}
	n.second = second
	return n, neighbor
}

func (n *paneTile) layout(area image.Rectangle, minimum image.Point) {
	n.measure(minimum)
	n.place(area)
}

func (n *paneTile) place(area image.Rectangle) {
	n.area = area
	if n.pane != nil {
		n.pane.SetRect(area)
		return
	}
	if n.tab != nil {
		n.tab.root.place(area)
		return
	}
	first, second := splitPaneArea(area, n.mode, n.splitOffset())
	n.first.place(first)
	n.second.place(second)
}

func (n *paneTile) measure(leaf image.Point) image.Point {
	if n == nil {
		return image.Point{}
	}
	switch {
	case n.tab != nil:
		n.minimum = n.tab.root.measure(leaf)
	case n.first == nil:
		n.minimum = leaf
	default:
		n.minimum = splitMinimum(n.mode, n.first.measure(leaf), n.second.measure(leaf))
	}
	return n.minimum
}

func splitMinimum(mode SplitMode, first, second image.Point) image.Point {
	if mode == SplitRight {
		return image.Pt(first.X+second.X, max(first.Y, second.Y))
	}
	return image.Pt(max(first.X, second.X), first.Y+second.Y)
}

func (n *paneTile) splitBounds() (int, int) {
	span := n.mode.axis(n.area.Size())
	return splitBounds(span, n.mode.axis(n.first.minimum), n.mode.axis(n.second.minimum))
}

func splitBounds(span, first, second int) (int, int) {
	if span < first+second {
		// A small window shares the available space proportionally among
		// branches, including branches with several panes along this axis.
		offset := int(math.Round(float64(span) * float64(first) / float64(first+second)))
		return offset, offset
	}
	return first, span - second
}

func (n *paneTile) splitOffset() int {
	lo, hi := n.splitBounds()
	return splitOffset(n.mode.axis(n.area.Size()), n.ratio, lo, hi)
}

func splitOffset(span int, ratio float64, lo, hi int) int {
	offset := int(math.Round(float64(span) * ratio))
	return max(lo, min(hi, offset))
}

func (n *paneTile) resize(offset int) bool {
	lo, hi := n.splitBounds()
	if lo >= hi {
		return false
	}
	offset = max(lo, min(hi, offset))
	if offset == n.splitOffset() {
		return false
	}
	n.ratio = float64(offset) / float64(n.mode.axis(n.area.Size()))
	return true
}

type splitStep struct {
	mode          SplitMode
	ratio         float64
	first, second image.Point
	followSecond  bool
}

// splitPreview projects the new tile through the measured layout so ancestors
// can give its branch more space before a split changes the live tree.
func (n *paneTile) splitPreview(area image.Rectangle, target *paneTile, added image.Point, mode SplitMode, before bool) image.Rectangle {
	if target == nil {
		return image.Rectangle{}
	}
	first, second := target.minimum, added
	if before {
		first, second = second, first
	}
	var scratch [12]splitStep
	need, path, ok := n.splitPath(target, splitMinimum(mode, first, second), scratch[:0])
	if !ok || area.Dx() < need.X || area.Dy() < need.Y {
		return image.Rectangle{}
	}
	for i := len(path) - 1; i >= 0; i-- {
		step := path[i]
		left, right := step.areas(area)
		area = left
		if step.followSecond {
			area = right
		}
	}
	left, right := (splitStep{mode: mode, ratio: 0.5, first: first, second: second}).areas(area)
	if before {
		return left
	}
	return right
}

func (n *paneTile) splitPath(target *paneTile, replacement image.Point, path []splitStep) (image.Point, []splitStep, bool) {
	if n == target {
		return replacement, path, true
	}
	if n.tab != nil {
		return n.tab.root.splitPath(target, replacement, path)
	}
	if n.first == nil {
		return image.Point{}, path, false
	}
	if size, steps, ok := n.first.splitPath(target, replacement, path); ok {
		step := splitStep{mode: n.mode, ratio: n.ratio, first: size, second: n.second.minimum}
		return splitMinimum(n.mode, step.first, step.second), append(steps, step), true
	}
	if size, steps, ok := n.second.splitPath(target, replacement, path); ok {
		step := splitStep{mode: n.mode, ratio: n.ratio, first: n.first.minimum, second: size, followSecond: true}
		return splitMinimum(n.mode, step.first, step.second), append(steps, step), true
	}
	return image.Point{}, path, false
}

func (s splitStep) areas(area image.Rectangle) (image.Rectangle, image.Rectangle) {
	span := s.mode.axis(area.Size())
	lo, hi := splitBounds(span, s.mode.axis(s.first), s.mode.axis(s.second))
	return splitPaneArea(area, s.mode, splitOffset(span, s.ratio, lo, hi))
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
