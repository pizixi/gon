package ui

import (
	"image"
	"strings"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/rivo/uniseg"
)

const tabTooltipDelay = 450 * time.Millisecond

func (a *App) drawTabTooltip(gtx layout.Context, row image.Rectangle, sidebarRight int, label tabLabel) {
	if row.Empty() || !gtx.Enabled() || a.menuOpen || a.tabDrag.tab != nil || gtx.Now.IsZero() {
		return
	}
	if gtx.Now.Before(a.tabTooltipAt) {
		gtx.Execute(op.InvalidateCmd{At: a.tabTooltipAt})
		return
	}

	px := a.uiPx(gtx, 12)
	gap, padX, padY := gtx.Dp(8), gtx.Dp(10), gtx.Dp(8)
	x, top := sidebarRight+gap, a.hdrH+gap
	lineH := max(gtx.Dp(18), px+gtx.Dp(4))
	textWidth := min(gtx.Dp(560), a.lastSize.X-x-gap) - 2*padX
	maxLines := (a.lastSize.Y - top - gap - 2*padY) / lineH
	if textWidth <= 0 || maxLines <= 0 {
		return
	}

	full := label.full()
	details := []string{full}
	if pane := a.hoverTab.Focused(); pane != nil && pane.sess != nil {
		sess := pane.sess
		if sess.Elevated() {
			details = append(details, "管理员权限")
		}
		if dir := sess.StartingDirectory(); dir != "" {
			details = append(details, "启动目录  "+dir)
		}
		if label.original != "" {
			details = append(details, "原始标题  "+label.original)
		} else if exe := sess.Executable(); exe != "" && !strings.Contains(full, exe) {
			details = append(details, "程序  "+exe)
		}
	}

	lines := a.wrapTooltipText(nil, full, px, textWidth)
	titleLines := len(lines)
	for _, detail := range details[1:] {
		lines = a.wrapTooltipText(lines, detail, px, textWidth)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = a.textFit(lines[maxLines-1]+"…", px, textWidth)
	}
	width := 0
	for _, line := range lines {
		width = max(width, a.textMeasure(line, px))
	}
	height := 2*padY + len(lines)*lineH
	y := max(top, min(row.Min.Y, a.lastSize.Y-gap-height))
	box := image.Rect(x, y, x+min(width, textWidth)+2*padX, y+height)
	shape := clip.UniformRRect(box, gtx.Dp(7))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.PanelHi, 0xff), shape.Op(gtx.Ops))
	border := clip.Stroke{Path: shape.Path(gtx.Ops), Width: float32(max(1, gtx.Dp(1)))}
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Border, 0xff), border.Op())
	defer clip.Rect(box.Inset(1)).Push(gtx.Ops).Pop()
	semantic.LabelOp(strings.Join(details, "\n")).Add(gtx.Ops)

	baseline := y + padY + (lineH+a.textAscent(px)-px/4)/2
	for i, line := range lines {
		col := condColor(i < titleLines, a.theme.Fg, a.theme.Dim)
		a.drawText(gtx, line, x+padX, baseline+i*lineH, px, col, 0xff)
	}
}

func (a *App) wrapTooltipText(lines []string, text string, px, width int) []string {
	start, used := 0, 0
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		part := clusters.Str()
		from, to := clusters.Positions()
		if part == "\n" || part == "\r\n" || part == "\r" {
			lines = append(lines, text[start:from])
			start, used = to, 0
			continue
		}
		advance := a.textMeasure(part, px)
		if from > start && used+advance > width {
			lines = append(lines, text[start:from])
			start, used = from, 0
		}
		used += advance
	}
	return append(lines, text[start:])
}
