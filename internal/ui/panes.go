package ui

import (
	"image"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"gon/internal/render"
	"gon/internal/term"
)

// paneTag identifies one pane's terminal surface. It is keyed by the pane
// alone so that reordering the pane list does not drop keyboard focus.
type paneTag struct{ p *Pane }

func (paneTag) ImplementsEventTag() {}

// paneDragTag tags a pane header (move).
type paneDragTag struct{ p *Pane }

func (paneDragTag) ImplementsEventTag() {}

type paneScrollTag struct{ p *Pane }

type scrollbarDrag struct {
	startY, lastY float32
	startOffset   int
	pointerID     pointer.ID
	active        bool
}

const (
	paneHeaderHeight    = 22
	paneHorizontalInset = 4
	minPaneWidth        = 88
	minPaneHeight       = 76
)

// drawPanes lays out and draws every pane of the active tab.
func (a *App) drawPanes(gtx layout.Context, area image.Rectangle) {
	t := a.activeTab()
	if t == nil {
		a.drawEmptyState(gtx, area)
		return
	}
	if area.Empty() {
		return
	}
	if a.settings.open {
		gtx = gtx.Disabled()
	}
	t.Relayout(area, a.minPaneSize)
	if len(t.Panes) > 1 && !a.settings.open {
		a.updatePaneControls(gtx, t)
		t.Relayout(area, a.minPaneSize)
	}

	// Keyboard shortcuts can change the tab while we draw its terminals.
	var framePanes [MaxPanes]*Pane
	paneCount := copy(framePanes[:], t.Panes)
	multi := paneCount > 1
	for _, p := range framePanes[:paneCount] {
		a.drawPane(gtx, p, p == t.Focused(), multi)
	}
	showDividers := multi && paneCount == len(t.Panes) && a.activeTab() == t
	if showDividers {
		a.drawDividers(gtx, t.root)
	}
	// Register scrollbar input after the divider handles so the track gets the press.
	for _, p := range framePanes[:paneCount] {
		a.drawScrollbar(gtx, p, area.Max.X)
	}
	if showDividers {
		a.drawPaneDragPreview(gtx, area)
	}
}

func paneBody(r image.Rectangle, headerHeight int) image.Rectangle {
	r.Min.Y += min(headerHeight, max(0, r.Dy()-1))
	return r
}

func (a *App) terminalArea(body image.Rectangle) image.Rectangle {
	width, cellWidth := body.Dx(), a.view.Metrics().CellW
	if cellWidth <= 0 || width < cellWidth {
		return body
	}
	inset := min(a.paneInsetPx, (width-cellWidth)/2)
	// Share the pixels left over after fitting whole columns.
	remainder := (width - 2*inset) % cellWidth
	body.Min.X += inset + remainder/2
	body.Max.X -= inset + (remainder+1)/2
	return body
}

func (a *App) drawPane(gtx layout.Context, p *Pane, focused, multi bool) {
	r := p.Rect()
	if r.Empty() {
		return
	}
	body := r
	if multi {
		body = paneBody(r, a.paneHeaderH)
		header := r
		header.Max.Y = body.Min.Y
		a.drawPaneHeader(gtx, p, header, focused)
	}

	if !a.settings.open {
		a.paneInput(gtx, p, body, focused)
	}
	grid := a.view.Grid(a.terminalArea(body))
	p.Resize(grid.Cols, grid.Rows, image.Pt(grid.Metrics.CellW, grid.Metrics.CellH))

	func() {
		defer clip.Rect(grid.Bounds).Push(gtx.Ops).Pop()
		defer op.Offset(grid.Bounds.Min).Push(gtx.Ops).Pop()

		a.view.ShowCursor = a.currentPane() == p && !a.settings.open
		a.view.CursorBlinkMS = 0
		if a.view.ShowCursor && (a.win == nil || a.windowFocused) {
			if exited, _ := p.Exited(); !exited {
				a.view.CursorBlinkMS = a.cfg.CursorBlink
			}
		}
		p.drawTerminal(gtx, a.view, grid, a.theme.Selection)
	}()
}

// drawScrollbar paints an overlay scrollbar and forwards drags to the pane.
func (a *App) drawScrollbar(gtx layout.Context, p *Pane, viewportRight int) {
	drag := &p.scrollbarDrag
	pane := p.Rect()
	if pane.Empty() {
		drag.active = false
		return
	}
	grid := p.presented.grid
	track := pane
	track.Min = image.Pt(grid.Bounds.Max.X, grid.Bounds.Min.Y)
	if track.Max.X < viewportRight {
		track.Max.X-- // Keep the visible divider available for resizing.
	}
	availableWidth := track.Max.X - pane.Min.X
	if availableWidth <= 0 || track.Dy() <= 0 {
		drag.active = false
		return
	}
	total := p.ScrollbackLen()
	if total <= 0 {
		p.scrollbarHover = false
		drag.active = false
		return
	}
	if !gtx.Enabled() {
		drag.active = false
	}
	off := min(p.ScrollOffset(), total)
	barH := min(track.Dy(), max(gtx.Dp(24), track.Dy()*grid.Rows/(total+grid.Rows)))
	travel := track.Dy() - barH
	tag := paneScrollTag{p: p}
	if !track.Empty() {
		func() {
			defer clip.Rect(track).Push(gtx.Ops).Pop()
			event.Op(gtx.Ops, tag)
		}()
	}

	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: tag, Kinds: pointer.Enter | pointer.Leave | pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll | pointer.Cancel,
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch pe.Kind {
		case pointer.Enter:
			p.scrollbarHover = true
		case pointer.Leave:
			p.scrollbarHover = false
		case pointer.Cancel:
			p.scrollbarHover = false
			drag.active = false
		case pointer.Scroll:
			if pe.Scroll.Y != 0 {
				p.ScrollBy(wheelLines(pe.Scroll.Y, a.view.Metrics().CellH))
				a.invalidate()
			}
		case pointer.Press:
			if isPrimaryPress(pe) && travel > 0 {
				drag.startY, drag.lastY = pe.Position.Y, pe.Position.Y
				drag.startOffset = min(p.ScrollOffset(), total)
				drag.pointerID = pe.PointerID
				drag.active = true
				gtx.Execute(pointer.GrabCmd{Tag: tag, ID: pe.PointerID})
			}
		case pointer.Drag:
			if drag.active && drag.pointerID == pe.PointerID && pe.Position.Y != drag.lastY && travel > 0 {
				drag.lastY = pe.Position.Y
				moved := float64(pe.Position.Y - drag.startY)
				lines := int(math.Round(moved * float64(total) / float64(travel)))
				p.SetScrollOffset(drag.startOffset - lines)
				a.invalidate()
			}
		case pointer.Release:
			if drag.pointerID == pe.PointerID {
				drag.active = false
			}
		}
	}

	y := track.Min.Y + travel*(total-off)/total
	hover := gtx.Enabled() && (p.scrollbarHover || drag.active)
	wide := min(availableWidth, max(1, gtx.Dp(7)))
	w := min(wide, max(1, gtx.Dp(4)))
	if hover {
		w = wide
	}
	// Keep the right edge fixed as the thumb expands inward.
	right := track.Max.X - max(0, (track.Dx()-wide+1)/2)
	bar := image.Rect(right-w, y, right, y+barH)
	al := uint8(0x40)
	if off > 0 {
		al = 0xbb
	}
	if hover {
		al = 0xff
	}
	defer clip.UniformRRect(bar, w/2).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
	paint.Fill(gtx.Ops, NRGBA(a.theme.Dim, al))
}

func (a *App) updatePaneControls(gtx layout.Context, t *Tab) {
	for _, p := range t.Panes {
		if p.closeButton.Clicked(gtx) {
			a.closePane(p)
			return
		}
		a.updatePaneDrag(gtx, t, p)
	}
	a.updateDividerDrag(gtx, t.root)
}

func (a *App) drawPaneHeader(gtx layout.Context, p *Pane, r image.Rectangle, focused bool) {
	if r.Empty() {
		return
	}
	defer clip.Rect(r).Push(gtx.Ops).Pop()
	target := p == a.dropPane
	tint := uint8(0x02)
	if focused {
		tint = 0x08
	}
	if target {
		tint = 0x12
	}
	paint.Fill(gtx.Ops, NRGBA(a.theme.Fg, tint))

	// Keep the close target inside the header, clear of the divider handles.
	band := dividerBand(gtx)
	button := image.Rectangle{
		Min: image.Pt(max(r.Min.X, r.Max.X-gtx.Dp(29)), r.Min.Y+band),
		Max: image.Pt(r.Max.X-band, r.Max.Y-gtx.Dp(1)),
	}
	dragRect := r
	if !button.Empty() {
		dragRect.Max.X = button.Min.X
	}
	if !a.settings.open && !dragRect.Empty() {
		deferDrag := clip.Rect(dragRect).Push(gtx.Ops)
		cursor := pointer.CursorGrab
		if a.dragPane == p {
			cursor = pointer.CursorGrabbing
		}
		cursor.Add(gtx.Ops)
		event.Op(gtx.Ops, paneDragTag{p: p})
		deferDrag.Pop()
	}

	px := a.uiPx(gtx, 11)
	name := p.ShellName()
	if exited, _ := p.Exited(); exited {
		name += " (已退出)"
	}
	x := r.Min.X + gtx.Dp(8)
	a.drawText(gtx, a.textFit(name, px, dragRect.Max.X-x), x,
		r.Min.Y+(r.Dy()+a.textAscent(px))/2, px, condColor(focused || target, a.theme.Fg, a.theme.Dim), 0xff)
	if !button.Empty() {
		a.drawIconButton(gtx, button, &p.closeButton, "关闭窗格", drawCloseIcon)
	}
}

func (a *App) drawPaneDragPreview(gtx layout.Context, area image.Rectangle) {
	if !a.paneDragging || !a.dragPoint.In(area) {
		return
	}
	defer clip.Rect(area).Push(gtx.Ops).Pop()
	pointer.CursorGrabbing.Add(gtx.Ops)
	if a.dropPane != nil {
		bounds := a.dropPane.Rect().Inset(gtx.Dp(3))
		shape := clip.UniformRRect(bounds, gtx.Dp(6))
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x07), shape.Op(gtx.Ops))
		outline := clip.Stroke{Path: shape.Path(gtx.Ops), Width: float32(max(1, gtx.Dp(1)))}
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x60), outline.Op())
	}

	px, pad := a.uiPx(gtx, 12), gtx.Dp(12)
	title := a.dragPane.ShellName()
	if title == "" {
		title = "终端"
	}
	w := min(area.Dx(), gtx.Dp(208), max(gtx.Dp(68), a.textMeasure(title, px)+2*pad))
	h := min(area.Dy(), gtx.Dp(30))
	x := min(a.dragPoint.X+pad, area.Max.X-w)
	y := min(a.dragPoint.Y+pad, area.Max.Y-h)
	card := image.Rect(x, y, x+w, y+h)
	shadow := clip.UniformRRect(card.Add(image.Pt(0, gtx.Dp(3))), gtx.Dp(7))
	paint.FillShape(gtx.Ops, NRGBA(0, 0x54), shadow.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(mix(a.theme.Panel, a.theme.Fg, 0.13), 0xff), clip.UniformRRect(card, gtx.Dp(7)).Op(gtx.Ops))
	a.drawText(gtx, a.textFit(title, px, w-2*pad), x+pad, y+(h+a.textAscent(px))/2, px, a.theme.Fg, 0xff)
}

func (a *App) updatePaneDrag(gtx layout.Context, t *Tab, p *Pane) {
	tag := paneDragTag{p: p}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			return
		}
		pe := ev.(pointer.Event)
		pos := image.Pt(int(pe.Position.X), int(pe.Position.Y))
		if pe.Kind == pointer.Press && a.dragPane == nil && a.resizeTile == nil && isPrimaryPress(pe) {
			if t.focusByPane(p) {
				a.invalidate()
			}
			a.dragPane, a.dragStart, a.dragID = p, pos, pe.PointerID
			gtx.Execute(pointer.GrabCmd{Tag: tag, ID: pe.PointerID})
		}
		if a.dragPane != p {
			continue
		}
		if pe.Kind == pointer.Cancel {
			a.resetPaneGesture()
			continue
		}
		if pe.PointerID != a.dragID {
			continue
		}
		switch pe.Kind {
		case pointer.Drag, pointer.Release:
			a.dragPoint = pos
			a.dropPane = nil
			delta := pos.Sub(a.dragStart)
			a.paneDragging = a.paneDragging || max(iabs(delta.X), iabs(delta.Y)) >= gtx.Dp(4)
			if a.paneDragging {
				if target := t.paneAt(pos); target != p {
					a.dropPane = target
				}
			}
			if pe.Kind == pointer.Release {
				t.swapPanes(p, a.dropPane)
				a.resetPaneGesture()
			}
		}
	}
}

func (a *App) resetPaneGesture() {
	a.dragPane, a.dropPane, a.resizeTile = nil, nil, nil
	a.paneDragging = false
}

func isPrimaryPress(e pointer.Event) bool {
	return e.Source == pointer.Touch || e.Buttons.Contain(pointer.ButtonPrimary)
}

func (a *App) updateDividerDrag(gtx layout.Context, n *paneTile) {
	if n.pane != nil {
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: n, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe := ev.(pointer.Event)
		pos := image.Pt(int(pe.Position.X), int(pe.Position.Y))
		offset := n.mode.axis(pos.Sub(n.area.Min))
		if pe.Kind == pointer.Press && a.dragPane == nil && a.resizeTile == nil && isPrimaryPress(pe) {
			a.resizeTile, a.dragID = n, pe.PointerID
			a.resizeOffset = offset - n.splitOffset(a.minPaneSize)
			gtx.Execute(pointer.GrabCmd{Tag: n, ID: pe.PointerID})
		}
		if a.resizeTile != n {
			continue
		}
		if pe.Kind == pointer.Cancel {
			a.resetPaneGesture()
			continue
		}
		if pe.PointerID != a.dragID {
			continue
		}
		switch pe.Kind {
		case pointer.Drag, pointer.Release:
			n.resize(offset-a.resizeOffset, a.minPaneSize)
			if pe.Kind == pointer.Release {
				a.resetPaneGesture()
			}
		}
	}
	a.updateDividerDrag(gtx, n.first)
	a.updateDividerDrag(gtx, n.second)
}

func dividerBand(gtx layout.Context) int {
	return max(1, gtx.Dp(4))
}

func (a *App) drawDividers(gtx layout.Context, n *paneTile) {
	if n.pane != nil || n.area.Empty() {
		return
	}
	lo, hi := n.splitBounds(a.minPaneSize)
	cut := n.splitOffset(a.minPaneSize)
	band := dividerBand(gtx)
	hit := n.area
	cursor := pointer.CursorEastWestResize
	if n.mode == SplitRight {
		x := n.area.Min.X + cut
		hit.Min.X, hit.Max.X = x-band, x+band
		line := image.Rect(x-1, n.area.Min.Y, x, n.area.Max.Y)
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, uint8(condColor(a.resizeTile == n, 0x15, 0x06))),
			clip.Rect(line.Intersect(n.area)).Op())
	} else {
		y := n.area.Min.Y + cut
		hit.Min.Y, hit.Max.Y = y-band, y+band
		cursor = pointer.CursorNorthSouthResize
	}
	if !a.settings.open && (lo < hi || a.resizeTile == n) {
		stack := clip.Rect(hit.Intersect(n.area)).Push(gtx.Ops)
		cursor.Add(gtx.Ops)
		event.Op(gtx.Ops, n)
		stack.Pop()
	}
	a.drawDividers(gtx, n.first)
	a.drawDividers(gtx, n.second)
}

// paneArea returns the complete content area to the right of the sidebar.
func (a *App) paneArea() image.Rectangle {
	size := a.lastSize
	return image.Rectangle{
		Min: image.Pt(a.sidebarWidth(size.X), min(size.Y, a.hdrH)),
		Max: size,
	}
}

// paneInput processes pointer, wheel, keyboard and paste input for one pane.
func (a *App) paneInput(gtx layout.Context, p *Pane, body image.Rectangle, focused bool) {
	tag := paneTag{p: p}
	// Register the region after processing shortcuts that can change the cell width.
	defer func() {
		area := a.terminalArea(body)
		defer op.Offset(area.Min).Push(gtx.Ops).Pop()
		defer clip.Rect(image.Rectangle{Max: area.Size()}).Push(gtx.Ops).Pop()
		cursor := pointer.CursorText
		if mode, _ := p.MouseMode(); mode > 0 && p.ScrollOffset() == 0 {
			cursor = pointer.CursorDefault
		}
		cursor.Add(gtx.Ops)
		event.Op(gtx.Ops, tag)
	}()

	grid := p.presented.grid
	if grid.Bounds.Empty() {
		grid = a.view.Grid(a.terminalArea(body))
	}

	// Consecutive mouse drags describe one caret. Forward only the last one
	// instead of making a TUI redraw every stale position in the frame queue.
	var drag pointer.Event
	flushDrag := func(latest bool) {
		if drag.Kind == 0 {
			return
		}
		if latest && a.selectionCursor != nil && (a.selecting == p || p.mouseButton == 1) {
			if at, ok := a.selectionCursor(); ok {
				local := at.Sub(grid.Bounds.Min)
				drag.Position = f32.Pt(float32(local.X), float32(local.Y))
			}
		}
		a.handlePanePointer(gtx, p, drag, grid)
		drag = pointer.Event{}
	}
	for {
		ev, ok := gtx.Event(
			pointer.Filter{
				Target:  tag,
				Kinds:   pointer.Press | pointer.Release | pointer.Drag | pointer.Move | pointer.Scroll | pointer.Cancel,
				ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
				ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			},
			key.FocusFilter{Target: tag},
			key.Filter{Focus: tag, Name: ""},
			// The catch-all above only matches unmodified keys; this one lets
			// Ctrl/Alt/Shift chords reach the pane as well.
			key.Filter{Focus: tag, Name: "", Optional: key.ModCtrl | key.ModShift | key.ModAlt | key.ModSuper},
			key.Filter{Focus: tag, Name: key.NameTab, Optional: key.ModShift | key.ModCtrl | key.ModAlt},
			key.Filter{Focus: tag, Name: key.NameEnter},
			key.Filter{Focus: tag, Name: key.NameReturn},
			key.Filter{Focus: tag, Name: key.NameDeleteBackward, Optional: key.ModCtrl | key.ModAlt},
			key.Filter{Focus: tag, Name: key.NameSpace, Optional: key.ModCtrl | key.ModShift | key.ModAlt},
			transfer.TargetFilter{Target: tag, Type: "application/text"},
		)
		if !ok {
			break
		}
		if next, ok := ev.(pointer.Event); ok && next.Kind == pointer.Drag && next.Source == pointer.Mouse {
			if drag.Kind != 0 && (drag.PointerID != next.PointerID || drag.Buttons != next.Buttons || drag.Modifiers != next.Modifiers) {
				flushDrag(false)
			}
			drag = next
			continue
		}
		// A release, cancel, modifier change or other input is an ordering
		// boundary. Never sample a future cursor position across that boundary.
		flushDrag(false)
		switch ev := ev.(type) {
		case pointer.Event:
			a.handlePanePointer(gtx, p, ev, grid)
		case key.Event:
			if !focused || a.searchOpen {
				continue
			}
			if a.handleShortcut(ev) {
				if a.currentPane() != p || a.searchOpen {
					return
				}
				continue
			}
			if ev.State != key.Press {
				continue
			}
			// Printable characters arrive as key.EditEvent from WM_CHAR; sending
			// them here as well would type every character twice on Windows.
			name := string(ev.Name)
			mod := ev.Modifiers.Contain(key.ModCtrl) || ev.Modifiers.Contain(key.ModAlt) ||
				ev.Modifiers.Contain(key.ModSuper)
			if !mod && !isSpecialKey(name) {
				continue
			}
			if p.SendKey(name, toMods(ev.Modifiers)) {
				a.view.ResetCursorBlink()
				a.invalidate()
			}
		case key.EditEvent:
			if !focused || a.searchOpen {
				continue
			}
			p.SendTextInput(ev.Text)
			a.view.ResetCursorBlink()
			a.invalidate()
		case transfer.DataEvent:
			if !focused {
				continue
			}
			data, err := io.ReadAll(ev.Open())
			if err == nil && len(data) > 0 {
				p.Paste(string(data))
			}
			a.invalidate()
		}
	}

	flushDrag(true)

	if focused && !a.searchOpen && !gtx.Focused(tag) {
		gtx.Execute(key.FocusCmd{Tag: tag})
	}
}

func (a *App) handlePanePointer(gtx layout.Context, p *Pane, ev pointer.Event, grid render.Grid) {
	mouseMode, encoding := p.MouseMode()
	reportMouse := mouseMode != 0 && p.ScrollOffset() == 0 && !ev.Modifiers.Contain(key.ModShift)
	cell, pixel, hit := grid.Hit(ev.Position, ev.Kind == pointer.Drag || ev.Kind == pointer.Release || ev.Kind == pointer.Scroll)
	cx, cy := cell.X, cell.Y
	pos := cell
	if encoding == term.MousePixels {
		pos = pixel
	}

	switch ev.Kind {
	case pointer.Press:
		if !hit {
			return
		}
		a.view.ResetCursorBlink()
		if t := a.activeTab(); t != nil && t.focusByPane(p) {
			gtx.Execute(key.FocusCmd{Tag: paneTag{p: p}})
			a.invalidate()
		}
		if ev.Buttons.Contain(pointer.ButtonSecondary) {
			if _, _, _, _, selected := p.SelectionRange(); selected {
				if a.copyPaneSelection(p) {
					p.SelectionClear()
				}
				return
			}
		}
		if reportMouse {
			btn := 0
			switch {
			case ev.Buttons.Contain(pointer.ButtonTertiary):
				btn = 1
			case ev.Buttons.Contain(pointer.ButtonSecondary):
				btn = 2
			}
			p.mouseButton = btn + 1
			p.mousePosition = pos
			mods := toMods(ev.Modifiers)
			if mouseMode == term.MousePressOnly {
				mods = 0
			}
			gtx.Execute(pointer.GrabCmd{Tag: paneTag{p: p}, ID: ev.PointerID})
			p.SendMouse(btn, pos, true, mods, encoding)
			a.invalidate()
			return
		}
		if ev.Buttons.Contain(pointer.ButtonSecondary) {
			a.requestPaste()
			return
		}
		if ev.Buttons.Contain(pointer.ButtonPrimary) && ev.Modifiers.Contain(key.ModCtrl) {
			if url, ok := p.URLAt(cy, cx); ok {
				a.openURL(url)
				return
			}
		}
		if ev.Buttons.Contain(pointer.ButtonPrimary) {
			now := time.Now()
			pt := ev.Position.Round()
			if a.clickPane == p && now.Sub(a.clickAt) < 450*time.Millisecond &&
				iabs(pt.X-a.clickPos.X) <= 4 && iabs(pt.Y-a.clickPos.Y) <= 4 {
				a.clickCount++
			} else {
				a.clickCount = 1
			}
			a.clickAt, a.clickPos, a.clickPane = now, pt, p
			switch a.clickCount {
			case 2: // select the word under the pointer
				if s, e, ok := p.WordRange(cy, cx); ok {
					p.selectPresentedRange(cy, s, e)
				}
			case 3: // select the whole line
				if e, ok := p.LineRange(cy); ok {
					p.selectPresentedRange(cy, 0, e)
				}
				a.clickCount = 0
			default:
				p.SelectionStart(cx, cy)
			}
			a.selecting = p
		}
	case pointer.Drag:
		if !hit {
			return
		}
		if reportMouse && (mouseMode == term.MouseDrag || mouseMode == term.MouseMotion) && p.mouseButton > 0 {
			p.mousePosition = pos
			p.SendMouse(32+p.mouseButton-1, pos, true, toMods(ev.Modifiers), encoding)
			a.invalidate()
			return
		}
		if a.selecting == p {
			p.SelectionExtend(cx, cy)
		}
	case pointer.Move:
		if hit && reportMouse && mouseMode == term.MouseMotion && p.mouseButton == 0 && a.selecting != p {
			p.SendMouse(35, pos, true, toMods(ev.Modifiers), encoding)
		}
	case pointer.Cancel, pointer.Release:
		if p.mouseButton > 0 {
			btn := p.mouseButton - 1
			p.mouseButton = 0
			if mouseMode != 0 && mouseMode != term.MousePressOnly {
				if !hit || ev.Kind == pointer.Cancel {
					pos = p.mousePosition
				}
				p.SendMouse(btn, pos, false, toMods(ev.Modifiers), encoding)
			}
			a.invalidate()
			return
		}
		if a.selecting == p {
			if hit && ev.Kind == pointer.Release && a.clickCount == 1 {
				p.SelectionExtend(cx, cy)
			}
			a.selecting = nil
		}
	case pointer.Scroll:
		dy := ev.Scroll.Y
		if dy == 0 {
			dy = ev.Scroll.X
		}
		if dy == 0 {
			return
		}
		if reportMouse && mouseMode != term.MousePressOnly && hit {
			btn := 65
			if dy < 0 {
				btn = 64
			}
			if ev.Scroll.Y == 0 {
				btn = 67
				if dy < 0 {
					btn = 66
				}
			}
			report := term.EncodeMouse(btn, pos.X, pos.Y, true, toMods(ev.Modifiers), encoding)
			ticks := max(1, min(16, iabs(int(dy))/120))
			p.noteMouseInput()
			p.SendText(strings.Repeat(report, ticks))
			a.invalidate()
			return
		}
		p.scrollWheel(wheelLines(dy, grid.Metrics.CellH))
		a.invalidate()
	}
}

func wheelLines(delta float32, cellHeight int) int {
	lines := -int(delta) / max(1, cellHeight)
	if delta < 0 {
		return max(1, lines)
	}
	return min(-1, lines)
}

func toMods(m key.Modifiers) term.Mods {
	var out term.Mods
	if m.Contain(key.ModCtrl) {
		out |= term.ModCtrl
	}
	if m.Contain(key.ModShift) {
		out |= term.ModShift
	}
	if m.Contain(key.ModAlt) {
		out |= term.ModAlt
	}
	if m.Contain(key.ModSuper) {
		out |= term.ModSuper
	}
	return out
}

// handleShortcut applies app-level shortcuts; it reports whether the key was
// consumed.
func (a *App) handleShortcut(ev key.Event) bool {
	if ev.State != key.Press {
		return false
	}
	ctrl := ev.Modifiers.Contain(key.ModCtrl)
	shift := ev.Modifiers.Contain(key.ModShift)
	alt := ev.Modifiers.Contain(key.ModAlt)
	name := string(ev.Name)

	switch {
	case ev.Modifiers == key.ModAlt && ev.Name == key.NameF4:
		// Windows handles this shortcut through the window close event.
		return true
	case ctrl && shift && name == "T":
		if _, err := a.NewTab(a.cfg.DefaultShell); err != nil {
			a.notify("启动失败：" + err.Error())
		}
		a.invalidate()
		return true
	case ctrl && shift && name == "W":
		a.CloseFocusedPane()
		a.invalidate()
		return true
	case ctrl && shift && name == string(key.NameTab):
		a.cycleTab(-1)
		a.invalidate()
		return true
	case ctrl && name == string(key.NameTab):
		a.cycleTab(1)
		a.invalidate()
		return true
	case ctrl && shift && name == "C":
		a.copySelection()
		return true
	case ctrl && shift && name == "V":
		a.requestPaste()
		return true
	case ctrl && shift && name == "F":
		a.openSearch()
		return true
	case ctrl && !shift && name == "C":
		// Copy when there is a selection, otherwise send ^C to the shell.
		if p := a.currentPane(); p != nil && p.SelectedText() != "" {
			a.copySelection()
			return true
		}
		return false
	case ctrl && !shift && name == "V":
		a.requestPaste()
		return true
	case alt && shift && (name == "+" || name == "="):
		a.SplitActive(SplitRight)
		return true
	case alt && shift && name == "-":
		a.SplitActive(SplitDown)
		return true
	case alt && shift && name == "W":
		a.CloseFocusedPane()
		a.invalidate()
		return true
	case ctrl && (name == "=" || name == "+"):
		a.setZoom(1)
		return true
	case ctrl && name == "-":
		a.setZoom(-1)
		return true
	case ctrl && name == "0":
		a.setZoom(0)
		return true
	case name == string(key.NameF11):
		a.fullscreen = !a.fullscreen
		if a.fullscreen {
			a.perform(system.ActionFullscreen)
		} else {
			a.perform(system.ActionUnmaximize)
		}
		return true
	case alt && (name == string(key.NameLeftArrow) || name == string(key.NameRightArrow) ||
		name == string(key.NameUpArrow) || name == string(key.NameDownArrow)):
		t := a.activeTab()
		if t != nil && len(t.Panes) > 1 {
			dx, dy := 0, 0
			switch name {
			case string(key.NameLeftArrow):
				dx = -1
			case string(key.NameRightArrow):
				dx = 1
			case string(key.NameUpArrow):
				dy = -1
			case string(key.NameDownArrow):
				dy = 1
			}
			t.FocusNeighbor(dx, dy)
			a.invalidate()
			return true
		}
	}
	return false
}

func (a *App) cycleTab(delta int) {
	if len(a.tabs) < 2 {
		return
	}
	i := (a.active + delta) % len(a.tabs)
	if i < 0 {
		i += len(a.tabs)
	}
	a.activateTab(i)
}

// sidebarWidth computes the sidebar width in pixels for a window width.
func (a *App) sidebarWidth(winW int) int {
	if a.sidebarHidden {
		return 0
	}
	w := a.sidePx
	if w <= 0 {
		w = a.cfg.Window.Sidebar
	}
	if w < 140 {
		w = 140
	}
	if w > winW/2 {
		w = winW / 2
	}
	return w
}

// setZoom changes the terminal font size. 0 resets to the configured size.
func (a *App) setZoom(delta int) {
	cur := a.view.FontSize()
	switch {
	case delta > 0:
		a.view.SetFontSize(cur + 1)
	case delta < 0:
		a.view.SetFontSize(cur - 1)
	default:
		a.view.SetFontSize(a.fontBase)
	}
	a.invalidate()
}

// drawEmptyState renders a hint when no shell could be started.
func (a *App) drawEmptyState(gtx layout.Context, area image.Rectangle) {
	px := a.uiPx(gtx, 14)
	msg := "暂无终端"
	w := a.textMeasure(msg, px)
	x := area.Min.X + (area.Dx()-w)/2
	if x < area.Min.X {
		x = area.Min.X
	}
	y := area.Min.Y + area.Dy()/2
	a.drawText(gtx, msg, x, y, px, a.theme.Dim, 0xff)
}

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// openURL opens an http(s) link in the default browser.
func (a *App) openURL(url string) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		a.notify("无法打开链接：" + err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()
}

// isSpecialKey reports whether a Gio key name is a named key (rather than text)
// that must be encoded from the key event.
func isSpecialKey(name string) bool {
	switch name {
	case "Tab", "⏎", "⌤", "⎋", "⌫", "⌦", "⇱", "⇲", "⇞", "⇟",
		"←", "→", "↑", "↓", "Insert",
		"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		return true
	}
	return false
}
