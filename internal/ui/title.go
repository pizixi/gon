package ui

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"gon/internal/config"
	"gon/internal/term"
)

var defaultShellProfiles = config.Default().Shells

type terminalTitle struct {
	name     string
	native   bool
	elevated bool
	activity term.ProgressState
}

type tabLabel struct {
	name, folder string
	original     string
}

func (t *Tab) terminalTitle() terminalTitle {
	if t == nil {
		return terminalTitle{}
	}
	title := terminalTitle{name: t.Title}
	if pane := t.Focused(); pane != nil {
		title.elevated = pane.sess != nil && pane.sess.Elevated()
		native, progress, exited := pane.titleStatus()
		if native != "" {
			title.name, title.native = native, true
			return title
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

func (t *Tab) sidebarLabel(title terminalTitle) tabLabel {
	label := tabLabel{name: title.name}
	if pane := t.Focused(); pane != nil && pane.sess != nil {
		sess := pane.sess
		if shell := sidebarShellName(sess.Executable(), pane.ShellName(), title); shell != "" {
			label.name = shell
			if dir := sess.StartingDirectory(); dir != "" {
				label.folder = filepath.Base(dir)
				if label.folder == string(filepath.Separator) {
					label.folder = filepath.Clean(dir)
				}
			}
			if title.native {
				label.original = title.name
			}
		}
	}
	if len(t.Panes) > 1 {
		label.name = fmt.Sprintf("%s (%d)", label.name, len(t.Panes))
	}
	return label
}

func sidebarShellName(executable, profileName string, title terminalTitle) string {
	if title.native && !isExecutableTitle(title.name, executable) {
		return ""
	}
	basename := filepath.Base(executable)
	name := strings.TrimSpace(profileName)
	for _, shell := range defaultShellProfiles {
		if strings.EqualFold(basename, shell.Command) {
			basename = shell.Command
			if strings.EqualFold(name, shell.Name) {
				name = ""
			}
			break
		}
	}
	if name != "" && !strings.EqualFold(name, basename) {
		return name
	}
	return strings.TrimSuffix(basename, filepath.Ext(basename))
}

// isExecutableTitle recognizes a default console title by the full launch path.
func isExecutableTitle(title, executable string) bool {
	path := strings.TrimSpace(title)
	// An elevated console can have both a localized and an English prefix.
	for {
		colon := strings.IndexAny(path, ":：")
		if colon < 0 {
			break
		}
		prefix := strings.TrimSpace(path[:colon])
		if prefix != "管理员" && !strings.EqualFold(prefix, "Administrator") {
			break
		}
		path = strings.TrimSpace(strings.TrimLeft(path[colon:], ":："))
	}
	path = strings.Trim(path, "\"")
	return filepath.IsAbs(path) && strings.EqualFold(filepath.Clean(path), executable)
}

func (l tabLabel) full() string {
	if l.folder == "" {
		return l.name
	}
	return l.folder + " · " + l.name
}

func (a *App) fitTabLabel(label tabLabel, px, width int) string {
	if label.folder != "" {
		suffix := " · " + label.name
		if folder := a.textFit(label.folder, px, width-a.textMeasure(suffix, px)); folder != "" {
			return folder + suffix
		}
	}
	return a.textFit(label.name, px, width)
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

func (a *App) drawCenteredTitle(gtx layout.Context, area image.Rectangle, title terminalTitle) {
	if area.Empty() {
		return
	}
	defer clip.Rect(area).Push(gtx.Ops).Pop()
	px := a.uiPx(gtx, 13)
	capHeight := a.view.TextRenderer().CapHeight(px)
	baseline := area.Min.Y + (area.Dy()+capHeight)/2
	shieldWidth, shieldGap := 0, 0
	if title.elevated && area.Dx() >= gtx.Dp(17) {
		shieldWidth, shieldGap = gtx.Dp(12), gtx.Dp(5)
	}
	shieldSpace := shieldWidth + shieldGap
	fitted := a.textFit(title.caption(), px, area.Dx()-shieldSpace)
	w := a.textMeasure(fitted, px)
	x := area.Min.X + (area.Dx()-w-shieldSpace)/2
	if shieldWidth > 0 {
		center := image.Pt(x+shieldWidth/2, baseline-capHeight/2)
		drawAdminShield(gtx, center, mix(a.theme.Accent, a.theme.Fg, 0.23))
		if !a.settings.open {
			h := gtx.Dp(7)
			r := image.Rect(x, center.Y-h, x+shieldWidth, center.Y+h)
			stack := clip.Rect(r).Push(gtx.Ops)
			semantic.LabelOp("管理员权限").Add(gtx.Ops)
			stack.Pop()
		}
		x += shieldSpace
	}
	if !a.settings.open && title.name != "" && w > 0 {
		r := image.Rect(x, area.Min.Y, x+w, area.Max.Y)
		stack := clip.Rect(r).Push(gtx.Ops)
		semantic.LabelOp(title.name).Add(gtx.Ops)
		stack.Pop()
	}
	a.drawText(gtx, fitted, x, baseline, px, a.titleColor(gtx, title, a.theme.Fg), 0xff)
}

func drawAdminShield(gtx layout.Context, center image.Point, col uint32) {
	x, y := float32(center.X), float32(center.Y)
	w, h := float32(gtx.Dp(5)), float32(gtx.Dp(6))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x, y-h))
	path.LineTo(f32.Pt(x+w, y-h*0.67))
	path.LineTo(f32.Pt(x+w, y))
	path.QuadTo(f32.Pt(x+w, y+h*0.55), f32.Pt(x, y+h))
	path.QuadTo(f32.Pt(x-w, y+h*0.55), f32.Pt(x-w, y))
	path.LineTo(f32.Pt(x-w, y-h*0.67))
	path.Close()
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(1))}.Op())
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
