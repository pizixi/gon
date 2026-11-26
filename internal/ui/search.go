package ui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

const searchLabel = "搜索终端历史"

type searchPanel struct {
	pane                  *Pane
	query                 string
	miss, focus           bool
	editor                widget.Editor
	previous, next, close widget.Clickable
}

type searchBarTag struct{}

func (a *App) openSearch() {
	p := a.currentPane()
	if p == nil {
		return
	}
	s := &a.search
	if s.pane != p {
		a.closeSearch()
		*s = searchPanel{pane: p, editor: widget.Editor{SingleLine: true, Submit: true}}
	}
	s.focus = true
	a.invalidate()
}

func (a *App) updateSearch(gtx layout.Context) {
	s := &a.search
	if s.pane == nil || !gtx.Enabled() {
		return
	}
	if s.close.Clicked(gtx) {
		a.closeSearch()
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ev.(key.Event).State == key.Press {
			a.closeSearch()
			return
		}
	}
	// Gio's editor submits unmodified Enter. Route Shift+Enter before it can
	// edit the field, then apply the pending text before finding a result.
	previous := 0
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &s.editor, Name: key.NameReturn, Required: key.ModShift},
			key.Filter{Focus: &s.editor, Name: key.NameEnter, Required: key.ModShift},
		)
		if !ok {
			break
		}
		if ev.(key.Event).State == key.Press {
			previous++
		}
	}
	changed := false
	for {
		ev, ok := s.editor.Update(gtx)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case widget.ChangeEvent:
			changed = true
		case widget.SubmitEvent:
			a.setSearchQuery(ev.Text)
			a.runSearch(true, false)
			changed = false
		}
	}
	if changed {
		a.setSearchQuery(s.editor.Text())
	}
	if s.previous.Clicked(gtx) {
		previous++
		s.focus = true
	}
	for range previous {
		a.runSearch(false, false)
	}
	if s.next.Clicked(gtx) {
		a.runSearch(true, false)
		s.focus = true
	}
}

func (a *App) drawSearch(gtx layout.Context, area image.Rectangle) {
	s := &a.search
	if s.pane == nil {
		return
	}
	if s.pane != a.currentPane() {
		a.closeSearch()
		return
	}
	bar := searchBounds(area, func(dp float32) int { return gtx.Dp(unit.Dp(dp)) })
	pad := min(gtx.Dp(4), min(bar.Dx(), bar.Dy())/8)
	content := bar.Inset(pad)
	if content.Empty() {
		return
	}
	shape := clip.UniformRRect(bar, gtx.Dp(8))
	defer shape.Push(gtx.Ops).Pop()
	pointer.CursorDefault.Add(gtx.Ops)
	event.Op(gtx.Ops, searchBarTag{})
	paint.Fill(gtx.Ops, NRGBA(a.theme.Panel, 0xff))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Border, 0xff), clip.Stroke{
		Path: shape.Path(gtx.Ops), Width: 1,
	}.Op())

	button := min(gtx.Dp(28), content.Dx()/6)
	field := content
	field.Max.X -= 3 * button
	if s.miss {
		status := field
		status.Min.X = max(status.Min.X, status.Max.X-gtx.Dp(56))
		status.Min.X = max(status.Min.X, field.Min.X+field.Dx()/2)
		field.Max.X = status.Min.X
		px := a.uiPx(gtx, 12)
		label := a.textFit("无匹配", px, status.Dx()-gtx.Dp(6))
		func() {
			defer clip.Rect(status).Push(gtx.Ops).Pop()
			semantic.LabelOp("无匹配").Add(gtx.Ops)
			a.drawText(gtx, label, status.Min.X+gtx.Dp(6), (status.Min.Y+status.Max.Y+a.textAscent(px))/2, px, 0xff7b72, 0xff)
		}()
	}
	a.drawSettingEditor(gtx, field, searchLabel, "搜索历史…", &s.editor, a.textEditorShaper(), true)
	if s.focus && gtx.Enabled() {
		gtx.Execute(key.FocusCmd{Tag: &s.editor})
		s.focus = false
	}
	close := image.Rect(content.Max.X-button, content.Min.Y, content.Max.X, content.Max.Y)
	next := close.Sub(image.Pt(button, 0))
	previous := next.Sub(image.Pt(button, 0))
	a.drawIconButton(gtx, previous, &s.previous, "上一个匹配", drawSearchPrevious)
	a.drawIconButton(gtx, next, &s.next, "下一个匹配", drawSearchNext)
	a.drawIconButton(gtx, close, &s.close, "关闭搜索", drawCloseIcon)
}

func drawSearchPrevious(gtx layout.Context, size image.Point, col uint32) {
	drawSearchChevron(gtx, size, col, -1)
}

func drawSearchNext(gtx layout.Context, size image.Point, col uint32) {
	drawSearchChevron(gtx, size, col, 1)
}

func drawSearchChevron(gtx layout.Context, size image.Point, col uint32, direction float32) {
	x, y := float32(size.X)/2, float32(size.Y)/2
	dx, dy := float32(gtx.Dp(4)), direction*float32(gtx.Dp(2))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x-dx, y-dy))
	path.LineTo(f32.Pt(x, y+dy))
	path.LineTo(f32.Pt(x+dx, y-dy))
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(1))}.Op())
}

func searchBounds(area image.Rectangle, dp func(float32) int) image.Rectangle {
	margin := min(dp(12), min(area.Dx(), area.Dy())/8)
	w := min(dp(384), area.Dx()-2*margin)
	h := min(dp(40), area.Dy()-2*margin)
	right, top := area.Max.X-margin, area.Min.Y+margin
	return image.Rect(right-w, top, right, top+h)
}

func (a *App) closeSearch() {
	s := &a.search
	if s.pane == nil {
		return
	}
	s.pane.clearSearchSelection()
	s.pane = nil
	s.query, s.miss, s.focus = "", false, false
	a.invalidate()
}

func (a *App) setSearchQuery(query string) {
	if a.search.pane == nil || query == a.search.query {
		return
	}
	a.search.query = query
	a.runSearch(false, true)
}

func (a *App) runSearch(forward, includeCurrent bool) {
	s := &a.search
	if s.pane == nil {
		return
	}
	found := s.pane.findAndSelect(s.query, forward, includeCurrent)
	s.miss = s.query != "" && !found
	a.invalidate()
}
