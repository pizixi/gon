package ui

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"gon/internal/config"
	"gon/internal/term"
)

var defaultShellProfiles = config.Default().Shells

type terminalTitle struct {
	name     string
	native   bool
	activity term.ProgressState
}

type tabLabel struct {
	name, folder string
	original     string
	elevated     bool
}

func (t *Tab) terminalTitle() terminalTitle {
	if t == nil {
		return terminalTitle{}
	}
	title := terminalTitle{name: t.Title}
	if pane := t.Focused(); pane != nil {
		native, progress, exited := pane.titleStatus()
		if native != "" {
			return terminalTitle{name: native, native: true}
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
		label.elevated = sess.Elevated()
		if shell := sidebarShellName(sess.Executable(), pane.ShellName(), title); shell != "" {
			label.name = fmt.Sprintf("%s %d", shell, t.ID)
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

func (l tabLabel) accessibleName() string {
	if l.elevated {
		return "管理员权限 · " + l.full()
	}
	return l.full()
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
