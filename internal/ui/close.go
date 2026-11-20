package ui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

type closeDialog struct {
	open, approved  bool
	cancel, confirm widget.Clickable
}

func (a *App) handleWindowClosing(e *app.ClosingEvent) {
	d := &a.closeDialog
	if len(a.tabs) == 0 || d.approved {
		return
	}
	e.Abort()
	if !d.open {
		*d = closeDialog{open: true}
		a.menuOpen = false
		a.hoverTab = nil
		a.tabDrag = tabDrag{}
		a.resetPaneGesture()
		a.selecting = nil
	}
	if !a.windowFocused || a.minimized.Load() {
		a.perform(system.ActionRaise)
	}
	a.invalidate()
}

type closeBackdropTag struct{}

func (a *App) drawCloseDialog(gtx layout.Context, size image.Point) {
	d := &a.closeDialog
	if !d.open {
		return
	}
	cancel := d.cancel.Clicked(gtx)
	focus := &d.cancel
	if gtx.Focused(&d.confirm) {
		focus = &d.confirm
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameTab, Optional: key.ModShift})
		if !ok {
			break
		}
		k := ev.(key.Event)
		if k.State != key.Press {
			continue
		}
		switch {
		case k.Name == key.NameEscape:
			cancel = true
		case focus == &d.cancel:
			focus = &d.confirm
		default:
			focus = &d.cancel
		}
	}
	if cancel {
		d.open = false
		a.settings.focusNew = a.settings.open
		a.invalidate()
		return
	}
	if d.confirm.Clicked(gtx) {
		d.approved = true
		a.perform(system.ActionClose)
	}

	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{A: 0x68})
	pointer.CursorDefault.Add(gtx.Ops)
	event.Op(gtx.Ops, closeBackdropTag{})

	w, h := min(gtx.Dp(416), size.X-2*gtx.Dp(12)), gtx.Dp(174)
	if w <= 0 {
		return
	}
	pos := image.Pt((size.X-w)/2, max(0, (size.Y-h)/2))
	r := image.Rectangle{Min: pos, Max: pos.Add(image.Pt(w, h))}
	shadow := clip.UniformRRect(r.Add(image.Pt(0, gtx.Dp(5))).Inset(-gtx.Dp(2)), gtx.Dp(15))
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0x4c}, shadow.Op(gtx.Ops))
	shape := clip.UniformRRect(r, gtx.Dp(14))
	paint.FillShape(gtx.Ops, NRGBA(mix(a.theme.Panel, a.theme.Fg, 0.05), 0xff), shape.Op(gtx.Ops))

	left, right := r.Min.X+gtx.Dp(22), r.Max.X-gtx.Dp(22)
	const title = "关闭 Gon？"
	tabs := fmt.Sprintf("当前有 %d 个标签页。", len(a.tabs))
	const detail = "关闭窗口会结束所有终端会话。"
	label := clip.Rect(image.Rect(left, r.Min.Y+gtx.Dp(16), right, r.Min.Y+gtx.Dp(103))).Push(gtx.Ops)
	semantic.LabelOp(title + " " + tabs + " " + detail).Add(gtx.Ops)
	titlePx := a.uiPx(gtx, 18)
	a.drawText(gtx, a.textFit(title, titlePx, right-left), left, r.Min.Y+gtx.Dp(42), titlePx, a.theme.Fg, 0xff)
	px := a.uiPx(gtx, 13)
	a.drawText(gtx, a.textFit(tabs, px, right-left), left, r.Min.Y+gtx.Dp(73), px, a.theme.Dim, 0xff)
	a.drawText(gtx, a.textFit(detail, px, right-left), left, r.Min.Y+gtx.Dp(94), px, a.theme.Dim, 0xff)
	label.Pop()

	y, gap := r.Max.Y-gtx.Dp(54), gtx.Dp(10)
	btnW := min(gtx.Dp(104), (right-left-gap)/2)
	confirm := image.Rect(right-btnW, y, right, y+gtx.Dp(34))
	back := confirm.Sub(image.Pt(btnW+gap, 0))
	a.drawSettingsButton(gtx, back, &d.cancel, "取消", a.theme.PanelHi)
	a.drawSettingsButton(gtx, confirm, &d.confirm, "关闭窗口", mix(a.theme.Panel, 0xc42b1c, 0.45))
	if !gtx.Focused(focus) {
		gtx.Execute(key.FocusCmd{Tag: focus})
	}
}
