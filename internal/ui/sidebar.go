package ui

import (
	"image"
	"slices"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

type sidebarScrollTag struct{}
type tabTag struct{ tab *Tab }

type tabDrag struct {
	tab       *Tab
	id        pointer.ID
	start, at image.Point
	offset    int
	moving    bool
}

type sidebarList struct {
	area           image.Rectangle
	rowHeight, gap int
}

func (l sidebarList) row(index, scroll int) image.Rectangle {
	y := l.area.Min.Y + index*(l.rowHeight+l.gap) - scroll
	return image.Rect(l.area.Min.X, y, l.area.Max.X, y+l.rowHeight)
}

func (l sidebarList) insertion(y, scroll, count int) int {
	stride := l.rowHeight + l.gap
	index := (y - l.area.Min.Y + scroll + stride - l.rowHeight/2) / stride
	return max(0, min(count, index))
}

func (l sidebarList) clampScroll(scroll, count int) int {
	contentHeight := count*(l.rowHeight+l.gap) - l.gap
	return max(0, min(scroll, contentHeight-l.area.Dy()))
}

func sidebarButtonBounds(gtx layout.Context, height int) image.Rectangle {
	size := min(height, gtx.Dp(32))
	x, y := gtx.Dp(6), (height-size)/2
	return image.Rect(x, y, x+size, y+size)
}

func (a *App) updateSidebarButtons(gtx layout.Context) {
	if a.settings.open {
		return
	}
	if a.sidebarToggle.Clicked(gtx) {
		a.sidebarHidden = !a.sidebarHidden
		a.closeMenu()
		a.hoverTab = nil
		a.tabDrag = tabDrag{}
		a.revealTab = !a.sidebarHidden
		a.resetPaneGesture()
	}
	if !a.sidebarHidden && a.newTabButton.Clicked(gtx) {
		if _, err := a.NewTab(a.cfg.DefaultShell); err != nil {
			a.notify("启动失败：" + err.Error())
		}
	}
}

func (a *App) drawSidebarButtons(gtx layout.Context, height int) int {
	if a.settings.open {
		gtx = gtx.Disabled()
	}
	r := sidebarButtonBounds(gtx, height)
	label := "隐藏侧栏"
	if a.sidebarHidden {
		label = "展开侧栏"
	}
	a.drawIconButton(gtx, r, &a.sidebarToggle, label, drawSidebarIcon)
	if !a.sidebarHidden {
		r = r.Add(image.Pt(r.Dx()+gtx.Dp(3), 0))
		a.drawIconButton(gtx, r, &a.newTabButton, "新建标签页", drawPlusIcon)
	}
	return r.Max.X
}

func (a *App) sidebarTabs(gtx layout.Context, area image.Rectangle) sidebarList {
	rowH, gap := gtx.Dp(34), gtx.Dp(3)
	inset := min(a.pad/2, area.Dx()/4)
	top := min(area.Max.Y, area.Min.Y+a.hdrH+a.pad)
	bottom := max(top, area.Max.Y-a.pad-rowH-gap)
	return sidebarList{area: image.Rect(area.Min.X+inset, top, area.Max.X-inset, bottom), rowHeight: rowH, gap: gap}
}

func (a *App) drawSidebar(gtx layout.Context, area image.Rectangle, list sidebarList, active *Tab, title terminalTitle) {
	line := image.Rect(area.Max.X-1, min(area.Max.Y, a.hdrH), area.Max.X, area.Max.Y)
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x06), clip.Rect(line).Op())
	if a.settings.open {
		gtx = gtx.Disabled()
	}
	if !list.area.Empty() {
		px := a.uiPx(gtx, 13)
		var hoverRow image.Rectangle
		var hoverLabel tabLabel
		stack := clip.Rect(list.area).Push(gtx.Ops)
		event.Op(gtx.Ops, sidebarScrollTag{})
		for i, tab := range a.tabs {
			row := list.row(i, a.sideScroll)
			if row.Min.Y >= list.area.Max.Y {
				break
			}
			if row.Max.Y > list.area.Min.Y {
				tabTitle := title
				if tab != active {
					tabTitle = tab.terminalTitle()
				}
				label := tab.sidebarLabel(tabTitle)
				a.drawTabRow(gtx, row, tab, tabTitle, label, tab == active, tab.group != nil && tab.shownWith(active), px)
				if tab == a.hoverTab {
					hoverRow, hoverLabel = row.Intersect(list.area), label
				}
			}
		}
		a.drawTabDrag(gtx, list, px)
		stack.Pop()
		a.drawTabTooltip(gtx, hoverRow, area.Max.X, hoverLabel)
	}
}

func (a *App) updateTabInput(gtx layout.Context, list sidebarList) {
	for i, tab := range a.tabs {
		if tab.closeButton.Clicked(gtx) {
			a.CloseTab(i)
			return
		}
		tag := tabTag{tab: tab}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Enter | pointer.Leave | pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
			if !ok {
				break
			}
			e := ev.(pointer.Event)
			pos := image.Pt(int(e.Position.X), int(e.Position.Y))
			switch e.Kind {
			case pointer.Enter:
				if e.Source == pointer.Mouse {
					a.hoverTab = tab
					a.tabTooltipAt = gtx.Now.Add(tabTooltipDelay)
				}
			case pointer.Leave:
				if e.Source == pointer.Mouse && a.hoverTab == tab {
					a.hoverTab = nil
				}
			case pointer.Press:
				if e.Buttons.Contain(pointer.ButtonTertiary) {
					a.CloseTab(i)
					return
				}
				if e.Buttons.Contain(pointer.ButtonSecondary) {
					a.openTabMenu(tab, pos)
					return
				}
				if a.tabDrag.tab == nil && isPrimaryPress(e) {
					row := list.row(i, a.sideScroll)
					if pos.In(tabCloseBounds(gtx, row)) {
						continue
					}
					a.tabDrag = tabDrag{tab: tab, id: e.PointerID, start: pos, at: pos, offset: pos.Y - row.Min.Y}
				}
			case pointer.Cancel:
				if a.tabDrag.tab == tab {
					a.tabDrag = tabDrag{}
				}
			case pointer.Drag, pointer.Release:
				drag := &a.tabDrag
				if drag.tab != tab || drag.id != e.PointerID {
					continue
				}
				drag.at = pos
				if e.Kind == pointer.Drag && !drag.moving && len(a.tabs) > 1 {
					travel := pos.Sub(drag.start)
					if max(iabs(travel.X), iabs(travel.Y)) >= gtx.Dp(4) {
						drag.moving = true
						gtx.Execute(pointer.GrabCmd{Tag: tag, ID: e.PointerID})
					}
				}
				if e.Kind != pointer.Release {
					continue
				}
				a.tabTooltipAt = gtx.Now.Add(tabTooltipDelay)
				moving := drag.moving
				a.tabDrag = tabDrag{}
				if moving {
					if pos.In(list.area) {
						a.moveTab(i, list.insertion(pos.Y, a.sideScroll, len(a.tabs)))
					} else if drop := a.tabDrop(tab, pos); !drop.bounds.Empty() {
						a.splitTab(drop.target, tab, drop.mode, drop.before)
					}
					return
				}
				if !moving && pos.In(list.row(i, a.sideScroll).Intersect(list.area)) {
					a.activateTab(i)
				}
			}
		}
	}
}

// moveTab inserts the dragged tab at a gap in the sidebar's current order.
func (a *App) moveTab(from, gap int) {
	to := gap
	if to > from {
		to--
	}
	if to == from {
		return
	}
	active, tab := a.activeTab(), a.tabs[from]
	if to < from {
		copy(a.tabs[to+1:from+1], a.tabs[to:from])
	} else {
		copy(a.tabs[from:to], a.tabs[from+1:to+1])
	}
	a.tabs[to] = tab
	a.active = slices.Index(a.tabs, active)
}

func (a *App) updateTabScroll(gtx layout.Context, list sidebarList) {
	filter := pointer.Filter{Target: sidebarScrollTag{}, Kinds: pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}}
	for {
		ev, ok := gtx.Event(filter)
		if !ok {
			break
		}
		a.sideScroll += int(ev.(pointer.Event).Scroll.Y)
	}
	if a.revealTab && a.activeTab() != nil {
		row := list.row(a.active, a.sideScroll)
		if row.Min.Y < list.area.Min.Y {
			a.sideScroll += row.Min.Y - list.area.Min.Y
		} else if row.Max.Y > list.area.Max.Y {
			a.sideScroll += row.Max.Y - list.area.Max.Y
		}
	}
	a.revealTab = false
	a.sideScroll = list.clampScroll(a.sideScroll, len(a.tabs))
	drag := a.tabDrag
	if drag.moving && drag.at.In(list.area) {
		step := 0
		if drag.at.Y < list.area.Min.Y+gtx.Dp(20) {
			step = -gtx.Dp(4)
		} else if drag.at.Y >= list.area.Max.Y-gtx.Dp(20) {
			step = gtx.Dp(4)
		}
		if next := list.clampScroll(a.sideScroll+step, len(a.tabs)); next != a.sideScroll {
			a.sideScroll = next
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
		}
	}
}

func tabCloseBounds(gtx layout.Context, row image.Rectangle) image.Rectangle {
	return image.Rect(row.Max.X-gtx.Dp(26), row.Min.Y+gtx.Dp(5), row.Max.X-gtx.Dp(3), row.Max.Y-gtx.Dp(5))
}

func (a *App) drawTabRow(gtx layout.Context, row image.Rectangle, tab *Tab, title terminalTitle, label tabLabel, active, grouped bool, px int) {
	defer clip.Rect(row).Push(gtx.Ops).Pop()
	hover := a.hoverTab == tab
	dragged := a.tabDrag.moving && a.tabDrag.tab == tab
	if active || hover {
		alpha := uint8(condColor(active && !dragged, 0x08, 0x06))
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, alpha), clip.UniformRRect(row, gtx.Dp(6)).Op(gtx.Ops))
	}
	if gtx.Enabled() {
		cursor := pointer.CursorPointer
		if dragged {
			cursor = pointer.CursorGrabbing
		} else if a.tabDrag.tab == tab && len(a.tabs) > 1 {
			cursor = pointer.CursorGrab
		}
		cursor.Add(gtx.Ops)
		semantic.LabelOp(label.full()).Add(gtx.Ops)
		semantic.SelectedOp(active).Add(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		event.Op(gtx.Ops, tabTag{tab: tab})
	}

	close := tabCloseBounds(gtx, row)
	col := condColor((active || hover) && !dragged, a.theme.Fg, a.theme.Dim)
	labelArea := row
	labelArea.Min.X += gtx.Dp(7)
	if grouped && !dragged {
		x, y := row.Min.X+gtx.Dp(2), row.Min.Y+gtx.Dp(8)
		mark := image.Rect(x, y, x+max(1, gtx.Dp(2)), row.Max.Y-gtx.Dp(8))
		ink := NRGBA(a.theme.Accent, uint8(condColor(active, 0xff, 0x78)))
		paint.FillShape(gtx.Ops, ink, clip.UniformRRect(mark, gtx.Dp(1)).Op(gtx.Ops))
		labelArea.Min.X += gtx.Dp(3)
	}
	labelArea.Max.X = close.Min.X - gtx.Dp(3)
	a.drawTabLabel(gtx, labelArea, label, title, px, col)
	tab.closeShown = hover && !dragged && gtx.Enabled()
	closeContext := gtx
	if dragged {
		closeContext = closeContext.Disabled()
	}
	// Register the close target even before a hover frame paints its icon.
	// Otherwise a quick click enters the row's drag/click gesture first.
	a.layoutIconButton(closeContext, close, &tab.closeButton, "关闭标签页", drawCloseIcon, tab.closeShown)
}

func (a *App) drawTabDropPreview(gtx layout.Context) {
	drag := a.tabDrag
	if !drag.moving || !gtx.Enabled() {
		return
	}
	drop := a.tabDrop(drag.tab, drag.at)
	bounds := drop.bounds
	if bounds.Empty() {
		return
	}
	bounds = bounds.Inset(min(gtx.Dp(4), min(bounds.Dx(), bounds.Dy())/4))
	defer clip.Rect(bounds).Push(gtx.Ops).Pop()
	pointer.CursorGrabbing.Add(gtx.Ops)
	shape := clip.UniformRRect(bounds, gtx.Dp(6))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Accent, 0x2a), shape.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Accent, 0xb0), clip.Stroke{Path: shape.Path(gtx.Ops), Width: 1}.Op())

	px := a.uiPx(gtx, 13)
	padX, padY := min(gtx.Dp(12), bounds.Dx()/10), gtx.Dp(6)
	lineH := max(gtx.Dp(18), px+gtx.Dp(4))
	height := 2 * (lineH + padY)
	width := min(bounds.Dx()-gtx.Dp(8), gtx.Dp(320)) - 2*padX
	if width <= 0 || height > bounds.Dy() {
		return
	}

	label := drag.tab.sidebarLabel(drag.tab.terminalTitle())
	title := a.fitTabLabel(label, px, width)
	side := drop.side()
	instruction := "松开后在" + side + "分屏显示"
	prompt, promptPx := instruction, px
	promptWidth := a.textMeasure(prompt, promptPx)
	if promptWidth > width {
		promptPx = min(px, max(1, width/4))
		prompt = a.textFit(side+"分屏", promptPx, width)
		promptWidth = a.textMeasure(prompt, promptPx)
	}
	titleWidth := a.textMeasure(title, px)
	size := image.Pt(max(promptWidth, titleWidth)+2*padX, height)
	card := image.Rectangle{Max: size}.Add(bounds.Min.Add(bounds.Size().Sub(size).Div(2)))
	cardShape := clip.UniformRRect(card, gtx.Dp(8))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.PanelHi, 0xff), cardShape.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Accent, 0x9a), clip.Stroke{Path: cardShape.Path(gtx.Ops), Width: 1}.Op())
	defer clip.Rect(card).Push(gtx.Ops).Pop()
	semantic.LabelOp(instruction + "「" + label.full() + "」").Add(gtx.Ops)
	a.drawText(gtx, prompt, card.Min.X+(card.Dx()-promptWidth)/2,
		card.Min.Y+padY+(lineH+a.textAscent(promptPx))/2, promptPx, a.theme.Dim, 0xff)
	a.drawText(gtx, title, card.Min.X+(card.Dx()-titleWidth)/2,
		card.Min.Y+padY+lineH+(lineH+a.textAscent(px))/2, px, a.theme.Fg, 0xff)
}

func (a *App) drawTabDrag(gtx layout.Context, list sidebarList, px int) {
	drag := a.tabDrag
	if !drag.moving || !drag.at.In(list.area) {
		return
	}
	defer clip.Rect(list.area).Push(gtx.Ops).Pop()
	pointer.CursorGrabbing.Add(gtx.Ops)
	top := max(list.area.Min.Y, min(list.area.Max.Y-list.rowHeight, drag.at.Y-drag.offset))
	card := image.Rect(list.area.Min.X+gtx.Dp(5), top, list.area.Max.X-gtx.Dp(4), top+list.rowHeight)
	shape := clip.UniformRRect(card, gtx.Dp(7))
	shadow := clip.UniformRRect(card.Add(image.Pt(0, gtx.Dp(3))), gtx.Dp(7))
	paint.FillShape(gtx.Ops, NRGBA(0, 0x54), shadow.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(mix(a.theme.Panel, a.theme.Fg, 0.13), 0xff), shape.Op(gtx.Ops))
	pad := gtx.Dp(7)
	title := drag.tab.terminalTitle()
	labelArea := card
	labelArea.Min.X += pad
	labelArea.Max.X -= pad
	a.drawTabLabel(gtx, labelArea, drag.tab.sidebarLabel(title), title, px, a.theme.Fg)

	y := list.row(list.insertion(drag.at.Y, a.sideScroll, len(a.tabs)), a.sideScroll).Min.Y - list.gap/2
	y = max(list.area.Min.Y, min(list.area.Max.Y-1, y))
	marker := image.Rect(list.area.Min.X+gtx.Dp(6), y, list.area.Max.X-gtx.Dp(6), y+max(1, gtx.Dp(2)))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x60), clip.UniformRRect(marker, gtx.Dp(1)).Op(gtx.Ops))
}

func (a *App) drawTabLabel(gtx layout.Context, area image.Rectangle, label tabLabel, title terminalTitle, px int, col uint32) {
	if area.Empty() {
		return
	}
	fitted := a.fitTabLabel(label, px, area.Dx())
	baseline := area.Min.Y + (area.Dy()+a.view.TextRenderer().CapHeight(px))/2
	a.drawText(gtx, fitted, area.Min.X, baseline, px, a.titleColor(gtx, title, col), 0xff)
}

func (a *App) drawIconButton(gtx layout.Context, r image.Rectangle, button *widget.Clickable, label string, icon func(layout.Context, image.Point, uint32)) {
	a.layoutIconButton(gtx, r, button, label, icon, true)
}

func (a *App) layoutIconButton(gtx layout.Context, r image.Rectangle, button *widget.Clickable, label string, icon func(layout.Context, image.Point, uint32), shown bool) {
	if r.Empty() {
		return
	}
	defer op.Offset(r.Min).Push(gtx.Ops).Pop()
	button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !shown {
			return layout.Dimensions{Size: r.Size()}
		}
		pointer.CursorPointer.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		col := a.theme.Dim
		if button.Hovered() || button.Pressed() || gtx.Focused(button) {
			shape := clip.UniformRRect(image.Rectangle{Max: r.Size()}, gtx.Dp(5))
			paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x0a), shape.Op(gtx.Ops))
			col = a.theme.Fg
		}
		icon(gtx, r.Size(), col)
		return layout.Dimensions{Size: r.Size()}
	})
}

func drawSidebarIcon(gtx layout.Context, size image.Point, col uint32) {
	x, y := float32(size.X)/2, float32(size.Y)/2
	w, h := float32(gtx.Dp(7)), float32(gtx.Dp(5))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x-w, y-h))
	path.LineTo(f32.Pt(x+w, y-h))
	path.LineTo(f32.Pt(x+w, y+h))
	path.LineTo(f32.Pt(x-w, y+h))
	path.Close()
	divider := x - float32(gtx.Dp(2))
	path.MoveTo(f32.Pt(divider, y-h))
	path.LineTo(f32.Pt(divider, y+h))
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(1))}.Op())
}

func drawPlusIcon(gtx layout.Context, size image.Point, col uint32) {
	x, y := float32(size.X)/2, float32(size.Y)/2
	d := float32(gtx.Dp(5))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x-d, y))
	path.LineTo(f32.Pt(x+d, y))
	path.MoveTo(f32.Pt(x, y-d))
	path.LineTo(f32.Pt(x, y+d))
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(1))}.Op())
}

func drawCloseIcon(gtx layout.Context, size image.Point, col uint32) {
	x, y := float32(size.X)/2, float32(size.Y)/2
	d := float32(gtx.Dp(3.5))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x-d, y-d))
	path.LineTo(f32.Pt(x+d, y+d))
	path.MoveTo(f32.Pt(x-d, y+d))
	path.LineTo(f32.Pt(x+d, y-d))
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(1))}.Op())
}
