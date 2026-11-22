package ui

import (
	"fmt"
	"image"
	"strconv"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/key"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"

	"gon/internal/config"
)

type windowSettings struct {
	fullscreen, centered widget.Clickable
	width, height        widget.Editor
	x, y                 widget.Editor
	shaper               *text.Shaper
}

func (s *windowSettings) reset(w config.Window) {
	for _, field := range []struct {
		editor *widget.Editor
		value  int
		signed bool
	}{
		{&s.width, w.Width, false},
		{&s.height, w.Height, false},
		{&s.x, int(w.Position.X), true},
		{&s.y, int(w.Position.Y), true},
	} {
		*field.editor = widget.Editor{SingleLine: true, Filter: "0123456789", InputHint: key.HintNumeric}
		if field.signed {
			field.editor.Filter += "-"
		}
		field.editor.SetText(strconv.Itoa(field.value))
	}
}

func (s *windowSettings) update(gtx layout.Context, w *config.Window) {
	if s.fullscreen.Clicked(gtx) {
		w.Fullscreen = !w.Fullscreen
	}
	if s.centered.Clicked(gtx) {
		w.Position.Centered = !w.Position.Centered
	}
	for _, editor := range []*widget.Editor{&s.width, &s.height, &s.x, &s.y} {
		for {
			if _, ok := editor.Update(gtx); !ok {
				break
			}
		}
	}
}

func (s *windowSettings) apply(w *config.Window) error {
	width, widthErr := strconv.Atoi(s.width.Text())
	height, heightErr := strconv.Atoi(s.height.Text())
	x, xErr := strconv.ParseInt(s.x.Text(), 10, 32)
	y, yErr := strconv.ParseInt(s.y.Text(), 10, 32)
	validWidth := widthErr == nil && width >= config.MinWindowWidth && width <= config.MaxWindowSize
	validHeight := heightErr == nil && height >= config.MinWindowHeight && height <= config.MaxWindowSize
	switch {
	case !w.Fullscreen && !validWidth:
		return fmt.Errorf("宽度须在 %d 到 %d dp 之间", config.MinWindowWidth, config.MaxWindowSize)
	case !w.Fullscreen && !validHeight:
		return fmt.Errorf("高度须在 %d 到 %d dp 之间", config.MinWindowHeight, config.MaxWindowSize)
	case !w.Position.Centered && (xErr != nil || yErr != nil):
		return fmt.Errorf("请输入有效的 X、Y 整数坐标")
	}
	if validWidth {
		w.Width = width
	}
	if validHeight {
		w.Height = height
	}
	if xErr == nil && yErr == nil {
		w.Position.X, w.Position.Y = int32(x), int32(y)
	}
	return nil
}

func (a *App) drawWindowNumberPair(gtx layout.Context, row image.Rectangle, label, firstLabel, secondLabel string, first, second *widget.Editor, enabled bool) {
	px, baseline := a.drawSettingLabel(gtx, row, label)
	start := max(row.Min.X+a.textMeasure(label, px)+gtx.Dp(10), row.Max.X-gtx.Dp(232))
	y := row.Min.Y + (row.Dy()-gtx.Dp(30))/2
	fieldWidth := (row.Max.X - start - gtx.Dp(14) - a.textMeasure(firstLabel, px) - a.textMeasure(secondLabel, px)) / 2
	if fieldWidth <= 0 {
		return
	}
	x := start
	for _, field := range []struct {
		label  string
		editor *widget.Editor
	}{{firstLabel, first}, {secondLabel, second}} {
		a.drawText(gtx, field.label, x, baseline, px, a.theme.Dim, 0xff)
		x += a.textMeasure(field.label, px) + gtx.Dp(4)
		a.drawWindowNumber(gtx, image.Rect(x, y, x+fieldWidth, y+gtx.Dp(30)), field.label, field.editor, enabled)
		x += fieldWidth + gtx.Dp(6)
	}
}

func (a *App) drawWindowNumber(gtx layout.Context, r image.Rectangle, label string, editor *widget.Editor, enabled bool) {
	s := &a.settings.window
	if enabled && s.shaper == nil {
		s.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Regular()))
	}
	a.drawSettingEditor(gtx, r, label, "", editor, s.shaper, enabled)
}

func (a *App) drawSettingEditor(gtx layout.Context, r image.Rectangle, label, hint string, editor *widget.Editor, shaper *text.Shaper, enabled bool) {
	if r.Empty() {
		return
	}
	bg, fg := a.theme.PanelHi, a.theme.Fg
	if !enabled {
		bg, fg = a.theme.Panel, a.theme.Dim
	}
	if enabled && gtx.Focused(editor) {
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Accent, 0xff), clip.UniformRRect(r.Inset(-1), gtx.Dp(6)).Op(gtx.Ops))
	}
	paint.FillShape(gtx.Ops, NRGBA(bg, 0xff), clip.UniformRRect(r, gtx.Dp(5)).Op(gtx.Ops))
	px, pad := a.uiPx(gtx, 13), gtx.Dp(7)
	content := r
	content.Min.X, content.Max.X = content.Min.X+pad, content.Max.X-pad
	if content.Empty() {
		return
	}
	if !enabled {
		value := a.textFit(editor.Text(), px, content.Dx())
		a.drawText(gtx, value, content.Min.X, (r.Min.Y+r.Max.Y+a.textAscent(px))/2, px, fg, 0xff)
		return
	}
	if hint != "" && editor.Len() == 0 {
		baseline := (r.Min.Y + r.Max.Y + a.textAscent(px)) / 2
		a.drawText(gtx, a.textFit(hint, px, content.Dx()), content.Min.X, baseline, px, a.theme.Dim, 0xff)
	}

	textColor := op.Record(gtx.Ops)
	paint.ColorOp{Color: NRGBA(fg, 0xff)}.Add(gtx.Ops)
	ink := textColor.Stop()
	selectionColor := op.Record(gtx.Ops)
	paint.ColorOp{Color: NRGBA(a.theme.Selection, 0xff)}.Add(gtx.Ops)
	selection := selectionColor.Stop()

	defer clip.Rect(r).Push(gtx.Ops).Pop()
	semantic.LabelOp(label).Add(gtx.Ops)
	gtx.Constraints = layout.Constraints{Min: image.Pt(content.Dx(), 0), Max: content.Size()}
	macro := op.Record(gtx.Ops)
	dims := editor.Layout(gtx, shaper, font.Font{}, gtx.Metric.PxToSp(px), ink, selection)
	call := macro.Stop()
	baseline := (r.Dy() + a.textAscent(px)) / 2
	y := max(0, min(r.Dy()-dims.Size.Y, baseline-(dims.Size.Y-dims.Baseline)))
	defer op.Offset(content.Min.Add(image.Pt(0, y))).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}
