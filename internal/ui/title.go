package ui

import (
	"image"
	"math"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"gon/internal/term"
)

type terminalTitle struct {
	name     string
	activity term.ProgressState
}

func (t *Tab) terminalTitle() terminalTitle {
	if t == nil {
		return terminalTitle{}
	}
	title := terminalTitle{name: t.Title}
	if pane := t.Focused(); pane != nil {
		native, progress, exited := pane.titleStatus()
		if native != "" {
			return terminalTitle{name: native}
		}
		if name := pane.ShellName(); name != "" {
			title.name = name
		}
		if !exited && (progress.State != term.ProgressNormal || progress.Percent < 100) {
			title.activity = progress.State
		}
	}
	if title.name == "" {
		title.name = "终端"
	}
	return title
}

func (t terminalTitle) caption() string {
	if t.name == "" {
		return "Gon"
	}
	return t.name
}

func (a *App) updateWindowTitle(title terminalTitle) {
	caption := title.caption()
	if a.win == nil || caption == a.titleCache {
		return
	}
	if hwnd := a.hwnd.Load(); hwnd != 0 {
		a.titleCache = caption
		a.win.Run(func() { setWindowTitle(hwnd, caption) })
	}
}

func (a *App) drawCenteredTitle(gtx layout.Context, bar image.Rectangle, left, right int, title terminalTitle) {
	if right <= left {
		return
	}
	px := a.uiPx(gtx, 13)
	fitted := a.textFit(title.caption(), px, right-left)
	w := a.textMeasure(fitted, px)
	x := max(left, min(right-w, bar.Min.X+(bar.Dx()-w)/2))
	if !a.settings.open && title.name != "" && w > 0 {
		r := image.Rect(x, bar.Min.Y, x+w, bar.Max.Y)
		area := clip.Rect(r).Push(gtx.Ops)
		semantic.LabelOp(title.name).Add(gtx.Ops)
		area.Pop()
	}
	a.drawText(gtx, fitted, x, (bar.Min.Y+bar.Max.Y+a.textAscent(px))/2, px, a.titleColor(gtx, title, a.theme.Fg), 0xff)
}

func (a *App) titleColor(gtx layout.Context, title terminalTitle, base uint32) uint32 {
	if gtx.Now.IsZero() || (title.activity != term.ProgressNormal && title.activity != term.ProgressIndeterminate) {
		return base
	}
	const period = 1400 * time.Millisecond
	angle := 2 * math.Pi * float64(gtx.Now.UnixNano()%int64(period)) / float64(period)
	// A small palette bounds the number of colour variants in the glyph atlas.
	level := math.Round(5 * (1 - math.Cos(angle)))
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 24)})
	return mix(base, a.theme.Accent, float32(level)*0.035)
}
