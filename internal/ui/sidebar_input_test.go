package ui

import (
	"fmt"
	"image"
	"slices"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"

	"gon/internal/config"
)

func sidebarInputTest(t *testing.T, scale float32, count int) *layoutTest {
	t.Helper()
	a := New(config.Default(), nil, "")
	a.started = true
	t.Cleanup(a.shutdown)
	for i := 0; i < count; i++ {
		tab := &Tab{Title: fmt.Sprintf("Tab %d", i+1)}
		tab.addPane(newInputPane(), SplitRight)
		a.tabs = append(a.tabs, tab)
	}
	a.activateTab(count - 1)
	h := &layoutTest{
		App: a, size: image.Pt(int(900*scale), int(340*scale)),
		metric: unit.Metric{PxPerDp: scale, PxPerSp: scale},
	}
	h.frame()
	return h
}

func TestSidebarStationaryPressDoesNotStartDrag(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		for _, count := range []int{6, 18} {
			t.Run(fmt.Sprintf("scale=%g/tabs=%d", scale, count), func(t *testing.T) {
				h := sidebarInputTest(t, scale, count)
				before := slices.Clone(h.tabs)
				scroll := h.sideScroll
				r := h.controlBounds(t, fmt.Sprintf("Tab %d", count), 1)
				at := r.Min.Add(r.Size().Div(2))
				h.mouse(pointer.Move, at, 0)
				h.mouse(pointer.Press, at, pointer.ButtonPrimary)
				for range 5 {
					h.mouse(pointer.Move, at, pointer.ButtonPrimary)
					h.frame()
					if h.tabDrag.tab != before[count-1] || h.tabDrag.moving || h.tabDrag.start != at || h.tabDrag.at != at || h.sideScroll != scroll {
						t.Fatalf("stationary press changed drag or scroll: drag=%+v scroll=%d, want position=%v scroll=%d", h.tabDrag, h.sideScroll, at, scroll)
					}
				}
				h.mouse(pointer.Release, at, 0)
				if h.tabDrag.tab != nil || !slices.Equal(h.tabs, before) {
					t.Fatal("stationary release reordered tabs or kept a drag active")
				}
			})
		}
	}
}

func TestSidebarSingleClickActivatesAndClosesTab(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		for _, count := range []int{6, 18} {
			t.Run(fmt.Sprintf("scale=%g/tabs=%d", scale, count), func(t *testing.T) {
				h := sidebarInputTest(t, scale, count)
				target := h.tabs[count-2]
				r := h.controlBounds(t, fmt.Sprintf("Tab %d", count-1), 0)
				at := r.Min.Add(r.Size().Div(2))
				h.click(at)
				if h.activeTab() != target {
					t.Fatal("one click did not activate the tab")
				}
				gtx := layout.Context{Metric: h.metric}
				close := tabCloseBounds(gtx, r)
				at = close.Min.Add(close.Size().Div(2))
				h.mouse(pointer.Move, at, 0)
				h.mouse(pointer.Press, at, pointer.ButtonPrimary)
				if h.tabDrag.tab != nil {
					t.Fatal("close button press was captured by tab drag")
				}
				h.mouse(pointer.Release, at, 0)
				if slices.Contains(h.tabs, target) || len(h.tabs) != count-1 {
					t.Fatal("one click did not close the tab")
				}
			})
		}
	}
}

func TestSidebarQuickClickWithoutHoverActivatesTab(t *testing.T) {
	h := sidebarInputTest(t, 2, 6)
	target := h.tabs[3]
	r := h.controlBounds(t, "Tab 4", 0)
	at := r.Min.Add(r.Size().Div(2))
	position := f32.Pt(float32(at.X), float32(at.Y))
	h.router.Queue(
		pointer.Event{Kind: pointer.Move, Position: position},
		pointer.Event{Kind: pointer.Press, Position: position, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Position: position},
	)
	h.frame()
	h.frame()
	if h.activeTab() != target || h.tabDrag.tab != nil {
		t.Fatalf("quick click did not activate the tab: active=%p drag=%+v", h.activeTab(), h.tabDrag)
	}
}

func TestSidebarSingleClickWithoutHoverClosesTab(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, count := range []int{6, 18} {
			t.Run(fmt.Sprintf("scale=%g/tabs=%d", scale, count), func(t *testing.T) {
				h := sidebarInputTest(t, scale, count)
				target := h.tabs[count-2]
				row := h.controlBounds(t, fmt.Sprintf("Tab %d", count-1), 0)
				close := tabCloseBounds(layout.Context{Metric: h.metric}, row)
				h.click(close.Min.Add(close.Size().Div(2)))
				if slices.Contains(h.tabs, target) || len(h.tabs) != count-1 {
					t.Fatal("the close target needed a hover frame or a second click")
				}
			})
		}
	}
}

func TestSidebarHoverThenImmediateClick(t *testing.T) {
	for _, action := range []string{"switch", "close"} {
		for _, scale := range []float32{1, 2} {
			for _, count := range []int{6, 18} {
				t.Run(fmt.Sprintf("%s/scale=%g/tabs=%d", action, scale, count), func(t *testing.T) {
					h := sidebarInputTest(t, scale, count)
					target := h.tabs[count-2]
					row := h.controlBounds(t, fmt.Sprintf("Tab %d", count-1), 0)
					at := row.Min.Add(row.Size().Div(2))
					if action == "close" {
						close := tabCloseBounds(layout.Context{Metric: h.metric}, row)
						at = close.Min.Add(close.Size().Div(2))
					}
					h.mouse(pointer.Move, at, 0)
					if h.hoverTab != target || !target.closeShown {
						t.Fatal("hover feedback was not drawn")
					}
					position := f32.Pt(float32(at.X), float32(at.Y))
					h.router.Queue(
						pointer.Event{Kind: pointer.Press, Position: position, Buttons: pointer.ButtonPrimary},
						pointer.Event{Kind: pointer.Release, Position: position},
					)
					h.frame()
					if action == "switch" && h.activeTab() != target || action == "close" && slices.Contains(h.tabs, target) {
						t.Fatalf("immediate %s click was lost: active=%p drag=%+v", action, h.activeTab(), h.tabDrag)
					}
				})
			}
		}
	}
}

func TestSidebarClickReleaseDriftDoesNotStartDrag(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, count := range []int{6, 18} {
			t.Run(fmt.Sprintf("scale=%g/tabs=%d", scale, count), func(t *testing.T) {
				h := sidebarInputTest(t, scale, count)
				before := slices.Clone(h.tabs)
				target := h.tabs[count-2]
				row := h.controlBounds(t, fmt.Sprintf("Tab %d", count-1), 0)
				at := row.Min.Add(row.Size().Div(2))
				h.mouse(pointer.Move, at, 0)
				h.mouse(pointer.Press, at, pointer.ButtonPrimary)
				// A release can have a slightly different coordinate without
				// any intervening motion event, especially with remote input.
				h.mouse(pointer.Release, at.Add(image.Pt(int(6*scale), 0)), 0)
				if h.activeTab() != target || !slices.Equal(h.tabs, before) || h.tabDrag.tab != nil {
					t.Fatal("a click with release coordinate drift was treated as a drag")
				}
			})
		}
	}
}
