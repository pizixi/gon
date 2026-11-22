package ui

import (
	"encoding/json"
	"errors"
	"image"
	"path/filepath"
	"slices"
	"strings"

	"gioui.org/font/gofont"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"

	"gon/internal/config"
)

type shellFields struct {
	name, command, args, dir string
}

type shellProfile struct {
	original                 *config.Shell
	initial                  shellFields
	name, command, args, dir widget.Editor
}

type shellSettings struct {
	open                bool
	selected            int
	profiles            []*shellProfile
	toggle, add, remove widget.Clickable
	shaper              *text.Shaper
}

func fieldsForShell(shell config.Shell) shellFields {
	f := shellFields{name: shell.Name, command: shell.Command, dir: shell.Dir}
	if len(shell.Args) != 0 {
		args, _ := json.Marshal(shell.Args)
		f.args = string(args)
	}
	return f
}

func newShellProfile(original *config.Shell) *shellProfile {
	p := &shellProfile{original: original}
	var f shellFields
	if original != nil {
		f = fieldsForShell(*original)
	}
	for _, field := range []struct {
		editor *widget.Editor
		value  string
	}{{&p.name, f.name}, {&p.command, f.command}, {&p.args, f.args}, {&p.dir, f.dir}} {
		field.editor.SingleLine = true
		field.editor.SetText(field.value)
	}
	p.initial = p.fields()
	return p
}

func (p *shellProfile) fields() shellFields {
	return shellFields{p.name.Text(), p.command.Text(), p.args.Text(), p.dir.Text()}
}

func shellDisplayName(name, command string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if command = strings.TrimSpace(command); command != "" {
		base := filepath.Base(command)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return "未命名 Shell"
}

func (p *shellProfile) displayName() string {
	return shellDisplayName(p.name.Text(), p.command.Text())
}

func (p *shellProfile) shell() (config.Shell, error) {
	f := p.fields()
	var sh config.Shell
	if p.original != nil {
		sh = *p.original
		if f == p.initial {
			return sh, nil
		}
	}
	if f.name != p.initial.name {
		sh.Name = strings.TrimSpace(f.name)
	}
	if f.command != p.initial.command {
		sh.Command = strings.Trim(strings.TrimSpace(f.command), `"`)
	}
	if strings.TrimSpace(sh.Command) == "" {
		return config.Shell{}, errors.New("请输入 Shell 程序名或路径")
	}
	if f.dir != p.initial.dir {
		sh.Dir = strings.Trim(strings.TrimSpace(f.dir), `"`)
	}
	if f.args != p.initial.args {
		sh.Args = nil
		if args := strings.TrimSpace(f.args); args != "" && json.Unmarshal([]byte(args), &sh.Args) != nil {
			return config.Shell{}, errors.New("参数须为 JSON 字符串数组")
		}
	}
	return sh, nil
}

func (s *shellSettings) reset(profiles []config.Shell, selected int) {
	*s = shellSettings{selected: max(0, min(selected, len(profiles)-1))}
	for i := range profiles {
		s.profiles = append(s.profiles, newShellProfile(&profiles[i]))
	}
}

func (s *shellSettings) current() *shellProfile {
	if len(s.profiles) == 0 {
		return nil
	}
	return s.profiles[s.selected]
}

func (s *shellSettings) apply(cfg *config.Config) error {
	profiles := make([]config.Shell, len(s.profiles))
	for i, profile := range s.profiles {
		sh, err := profile.shell()
		if err != nil {
			s.selected = i
			return err
		}
		profiles[i] = sh
	}
	cfg.Shells = profiles
	return nil
}

func (s *settingsPanel) updateShellSettings(gtx layout.Context) {
	shells := &s.shells
	if profile := shells.current(); shells.open && profile != nil {
		for _, editor := range []*widget.Editor{&profile.name, &profile.command, &profile.args, &profile.dir} {
			for {
				if _, ok := editor.Update(gtx); !ok {
					break
				}
			}
		}
	}
	if shells.toggle.Clicked(gtx) {
		shells.open = !shells.open
		s.problem = ""
	}
	if !shells.open {
		return
	}
	if shells.add.Clicked(gtx) {
		profile := newShellProfile(nil)
		shells.profiles = append(shells.profiles, profile)
		shells.selected = len(shells.profiles) - 1
		s.problem = ""
		gtx.Execute(key.FocusCmd{Tag: &profile.name})
	}
	if shells.remove.Clicked(gtx) && len(shells.profiles) > 1 {
		i := shells.selected
		shells.profiles = slices.Delete(shells.profiles, i, i+1)
		last := len(shells.profiles) - 1
		shells.selected = min(i, last)
		if i < s.draft.DefaultShell {
			s.draft.DefaultShell--
		}
		s.draft.DefaultShell = min(s.draft.DefaultShell, last)
		s.problem = ""
	}
}

func (a *App) drawShellSettings(gtx layout.Context, row func(int) image.Rectangle) {
	s := &a.settings.shells
	profile := s.current()
	if profile == nil {
		return
	}
	a.drawSettingStepper(gtx, row(0), setShellProfile, "编辑 Shell", profile.displayName())
	if s.shaper == nil {
		s.shaper = text.NewShaper(text.WithCollection(gofont.Regular()))
	}
	for i, field := range []struct {
		label, hint string
		editor      *widget.Editor
	}{
		{"名称", "选填，默认使用程序名", &profile.name},
		{"程序", "程序名或绝对路径", &profile.command},
		{"参数 (JSON)", `["--login", "-i"]`, &profile.args},
		{"启动目录", "选填，使用默认启动目录", &profile.dir},
	} {
		r := row(i + 1)
		a.drawSettingLabel(gtx, r, field.label)
		y := r.Min.Y + (r.Dy()-gtx.Dp(30))/2
		fieldArea := image.Rect(r.Min.X+gtx.Dp(98), y, r.Max.X, y+gtx.Dp(30))
		a.drawSettingEditor(gtx, fieldArea, field.label, field.hint, field.editor, s.shaper, true)
	}
}

func (a *App) drawShellActions(gtx layout.Context, at image.Point) {
	s := &a.settings.shells
	button := image.Rectangle{Min: at, Max: at.Add(image.Pt(gtx.Dp(84), gtx.Dp(30)))}
	a.drawSettingsButton(gtx, button, &s.add, "添加 Shell", a.theme.PanelHi)
	if len(s.profiles) > 1 {
		button = button.Add(image.Pt(gtx.Dp(92), 0))
		a.drawSettingsButton(gtx, button, &s.remove, "删除 Shell", a.theme.PanelHi)
	}
}
