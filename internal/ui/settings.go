package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"slices"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"gon/internal/config"
)

const (
	settingsAppearance = iota
	settingsWindow
	settingsFonts
	settingsTerminal
	settingsPages
)

const (
	setTerminalSize = iota
	setInterfaceSize
	setLineHeight
	setShell
	setShellProfile
	setStartupTabs
	setScrollback
	settingsSteppers
)

type pickerResult struct {
	value string
	err   error
}

type settingsPanel struct {
	open, focusNew  bool
	page            int
	draft           config.Config
	problem         string
	picker          chan pickerResult
	tabs            [settingsPages]widget.Clickable
	chooseWallpaper widget.Clickable
	clearWallpaper  widget.Clickable
	fonts           fontSettings
	adjust          [settingsSteppers][2]widget.Clickable
	opacity         widget.Float
	blur            widget.Float
	transparency    widget.Float
	window          windowSettings
	shells          shellSettings
	cursorBlink     widget.Clickable
	singleWindow    widget.Clickable
	blinkInterval   int
	cancel          widget.Clickable
	save            widget.Clickable
}

type settingsBackdropTag struct{}

func (a *App) openSettings() {
	s := &a.settings
	s.open, s.focusNew = true, true
	s.page = settingsAppearance
	s.draft = a.cfg
	s.opacity.Value = float32((s.draft.Window.Opacity - 0.2) / 0.8)
	s.blur.Value = float32(max(0, min(96, s.draft.Window.Blur))) / 96
	s.transparency.Value = float32(s.draft.Window.Transparency / config.MaxWindowTransparency)
	s.blinkInterval = s.draft.CursorBlink
	if s.blinkInterval <= 0 {
		s.blinkInterval = config.Default().CursorBlink
	}
	s.problem = ""
	s.fonts = fontSettings{}
	s.window.reset(s.draft.Window)
	s.shells.reset(s.draft.Shells, s.draft.DefaultShell)
	a.menuOpen = false
	a.hoverTab = nil
	if a.searchOpen {
		a.closeSearch()
	}
	a.resetPaneGesture()
	a.tabDrag = tabDrag{}
	a.selecting = nil
	a.invalidate()
}

func (a *App) drawSettings(gtx layout.Context, size image.Point) {
	s := &a.settings
	if !s.open {
		return
	}
	cancel := s.cancel.Clicked(gtx)
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ev.(key.Event).State == key.Press {
			if s.fonts.menuOpen {
				s.fonts.menuOpen = false
			} else {
				cancel = true
			}
		}
	}
	if cancel {
		s.fonts.cancelDownload()
		s.open = false
		a.invalidate()
		return
	}

	if s.page == settingsTerminal {
		s.updateShellSettings(gtx)
	}
	for i := range s.tabs {
		if s.tabs[i].Clicked(gtx) {
			s.page = i
			s.fonts.menuOpen = false
			gtx.Execute(key.FocusCmd{Tag: &s.tabs[i]})
		}
	}
	for i := range s.adjust {
		for j := range s.adjust[i] {
			for s.adjust[i][j].Clicked(gtx) {
				s.change(i, 2*j-1)
			}
		}
	}
	if s.clearWallpaper.Clicked(gtx) {
		s.draft.Wallpaper = ""
		a.invalidate()
	}
	if s.cursorBlink.Clicked(gtx) {
		if s.draft.CursorBlink > 0 {
			s.draft.CursorBlink = 0
		} else {
			s.draft.CursorBlink = s.blinkInterval
		}
	}
	if s.singleWindow.Clicked(gtx) {
		s.draft.SingleWindow = !s.draft.SingleWindow
	}
	if s.chooseWallpaper.Clicked(gtx) {
		a.startWallpaperPicker()
	}
	if s.page == settingsFonts {
		a.updateFontSettings(gtx)
	}
	if s.page == settingsWindow {
		s.window.update(gtx, &s.draft.Window)
	}
	if s.save.Clicked(gtx) && s.picker == nil && s.fonts.download == nil {
		if err := s.window.apply(&s.draft.Window); err != nil {
			s.page = settingsWindow
			s.problem = err.Error()
		} else if err := s.shells.apply(&s.draft); err != nil {
			s.page = settingsTerminal
			s.shells.open = true
			s.problem = err.Error()
		} else if err := a.saveSettings(); err != nil {
			s.problem = err.Error()
		} else {
			s.open = false
			a.invalidate()
			return
		}
	}

	area := image.Rect(0, min(a.hdrH, size.Y), size.X, size.Y)
	defer clip.Rect(area).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{A: 0x46})
	event.Op(gtx.Ops, settingsBackdropTag{})

	w, h := min(gtx.Dp(526), area.Dx()-2*gtx.Dp(12)), gtx.Dp(302)
	if w <= 0 {
		return
	}
	pos := area.Min.Add(image.Pt((area.Dx()-w)/2, max(0, (area.Dy()-h)/2)))
	r := image.Rectangle{Min: pos, Max: pos.Add(image.Pt(w, h))}
	shape := clip.UniformRRect(r, gtx.Dp(14))
	shadow := clip.UniformRRect(r.Add(image.Pt(0, gtx.Dp(5))).Inset(-gtx.Dp(2)), gtx.Dp(15))
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0x4c}, shadow.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(mix(a.theme.Panel, a.theme.Fg, 0.05), 0xff), shape.Op(gtx.Ops))

	inset := gtx.Dp(16)
	navWidth := gtx.Dp(110)
	nav := image.Rect(r.Min.X+inset, r.Min.Y+inset, r.Min.X+navWidth, r.Max.Y-inset)
	left, right := nav.Max.X+inset, r.Max.X-inset
	a.drawText(gtx, "设置", nav.Min.X+gtx.Dp(5), r.Min.Y+gtx.Dp(37), a.uiPx(gtx, 18), a.theme.Fg, 0xff)
	for i, name := range []string{"外观", "窗口", "字体", "终端"} {
		y := r.Min.Y + gtx.Dp(unit.Dp(57+i*36))
		tab := image.Rect(nav.Min.X, y, nav.Max.X-gtx.Dp(10), y+gtx.Dp(30))
		bg := condColor(i == s.page, mix(a.theme.Panel, a.theme.Fg, 0.18), 0)
		a.drawSettingsButton(gtx, tab, &s.tabs[i], name, bg)
	}
	if s.page == settingsFonts {
		y := r.Max.Y - gtx.Dp(43)
		button := image.Rect(nav.Min.X, y, nav.Max.X, y+gtx.Dp(30))
		a.drawSettingsButton(gtx, button, &s.fonts.directory, "字体目录", a.theme.PanelHi)
	}
	if s.page == settingsTerminal {
		y := r.Max.Y - gtx.Dp(43)
		button := image.Rect(nav.Min.X, y, nav.Max.X, y+gtx.Dp(30))
		label := "Shell 配置"
		if s.shells.open {
			label = "返回终端"
		}
		a.drawSettingsButton(gtx, button, &s.shells.toggle, label, a.theme.PanelHi)
	}

	rowH := gtx.Dp(43)
	row := func(i int) image.Rectangle {
		y := r.Min.Y + gtx.Dp(18) + i*rowH
		return image.Rect(left, y, right, y+rowH)
	}
	var fontAnchors [2]image.Rectangle
	switch s.page {
	case settingsAppearance:
		name := "渐变背景"
		if s.draft.Wallpaper != "" {
			name = filepath.Base(s.draft.Wallpaper)
		}
		a.drawWallpaperPicker(gtx, row(0), name)
		a.drawSettingSlider(gtx, row(1), &s.opacity, "壁纸遮罩不透明度", fmt.Sprintf("%.0f%%", 100*s.draft.Window.Opacity))
		a.drawSettingSlider(gtx, row(2), &s.blur, "壁纸模糊", fmt.Sprint(s.draft.Window.Blur))
		a.drawSettingSlider(gtx, row(3), &s.transparency, "窗口透明度", fmt.Sprintf("%.0f%%", 100*s.draft.Window.Transparency))
	case settingsWindow:
		a.drawSettingToggle(gtx, row(0), &s.window.fullscreen, "全屏", s.draft.Window.Fullscreen)
		a.drawWindowNumberPair(gtx, row(1), "窗口大小 (dp)", "宽度", "高度", &s.window.width, &s.window.height, !s.draft.Window.Fullscreen)
		a.drawSettingToggle(gtx, row(2), &s.window.centered, "居中", s.draft.Window.Position.Centered)
		a.drawWindowNumberPair(gtx, row(3), "窗口坐标 (px)", "X", "Y", &s.window.x, &s.window.y, !s.draft.Window.Position.Centered)
	case settingsFonts:
		fontAnchors[terminalFont] = a.drawFontPicker(gtx, row(0), terminalFont, "终端字体")
		a.drawSettingStepper(gtx, row(1), setTerminalSize, "终端字号", fmt.Sprintf("%g dp", s.draft.Font.Size))
		fontAnchors[interfaceFont] = a.drawFontPicker(gtx, row(2), interfaceFont, "界面字体")
		a.drawSettingStepper(gtx, row(3), setInterfaceSize, "界面字号", fmt.Sprintf("%g dp", s.draft.InterfaceFont.Size))
		a.drawSettingStepper(gtx, row(4), setLineHeight, "终端行距", fmt.Sprintf("%.2f", s.draft.Font.LineHeight))
	case settingsTerminal:
		if s.shells.open {
			a.drawShellSettings(gtx, row)
			break
		}
		shellName := "—"
		if i := s.draft.DefaultShell; i >= 0 && i < len(s.shells.profiles) {
			shellName = s.shells.profiles[i].displayName()
		}
		a.drawSettingStepper(gtx, row(0), setShell, "默认 Shell", shellName)
		a.drawSettingStepper(gtx, row(1), setStartupTabs, "启动标签数", fmt.Sprint(s.draft.Tabs))
		a.drawSettingStepper(gtx, row(2), setScrollback, "回滚行数", fmt.Sprint(s.draft.Scrollback))
		a.drawSettingToggle(gtx, row(3), &s.cursorBlink, "光标闪烁", s.draft.CursorBlink > 0)
		a.drawSettingToggle(gtx, row(4), &s.singleWindow, "单窗口启动", s.draft.SingleWindow)
	}

	if s.problem != "" {
		px := a.uiPx(gtx, 12)
		a.drawText(gtx, a.textFit(s.problem, px, right-left), left, r.Max.Y-gtx.Dp(45), px, 0xff817a, 0xff)
	}
	y := r.Max.Y - gtx.Dp(43)
	if s.page == settingsTerminal && s.shells.open {
		a.drawShellActions(gtx, image.Pt(left, y))
	}
	confirm := image.Rect(right-gtx.Dp(72), y, right, y+gtx.Dp(30))
	back := confirm.Sub(image.Pt(gtx.Dp(82), 0))
	a.drawSettingsButton(gtx, back, &s.cancel, "取消", a.theme.PanelHi)
	saveCtx := gtx
	if s.picker != nil || s.fonts.download != nil {
		saveCtx = gtx.Disabled()
	}
	a.drawSettingsButton(saveCtx, confirm, &s.save, "保存", mix(a.theme.Panel, a.theme.Accent, 0.43))
	if s.focusNew {
		gtx.Execute(key.FocusCmd{Tag: &s.tabs[s.page]})
		s.focusNew = false
	}
	if s.fonts.menuOpen {
		a.drawFontMenu(gtx, area, fontAnchors[s.fonts.field])
	}
}

func (s *settingsPanel) updateSliders(gtx layout.Context) {
	if s.page != settingsAppearance {
		return
	}
	if s.opacity.Update(gtx) {
		s.draft.Window.Opacity = math.Round(20+80*float64(s.opacity.Value)) / 100
	}
	if s.blur.Update(gtx) {
		s.draft.Window.Blur = int(math.Round(96 * float64(s.blur.Value)))
	}
	if s.transparency.Update(gtx) {
		s.draft.Window.Transparency = math.Round(100*config.MaxWindowTransparency*float64(s.transparency.Value)) / 100
	}
}

func (a *App) drawSettingSlider(gtx layout.Context, row image.Rectangle, slider *widget.Float, label, value string) {
	px, baseline := a.drawSettingLabel(gtx, row, label)
	right := row.Max.X - gtx.Dp(50)
	left := max(row.Min.X+gtx.Dp(132), right-gtx.Dp(186))
	w, h := right-left, gtx.Dp(28)
	a.drawText(gtx, value, row.Max.X-a.textMeasure(value, px), baseline, px, a.theme.Dim, 0xff)
	if w <= 0 {
		return
	}
	pos := image.Pt(left, row.Min.Y+(row.Dy()-h)/2)
	defer op.Offset(pos).Push(gtx.Ops).Pop()
	defer clip.Rect(image.Rect(-gtx.Dp(7), 0, w+gtx.Dp(7), h)).Push(gtx.Ops).Pop()
	semantic.LabelOp(label).Add(gtx.Ops)
	semantic.DescriptionOp(value).Add(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	gtx.Constraints = layout.Exact(image.Pt(w, h))
	slider.Layout(gtx, layout.Horizontal, 6)

	y := h / 2
	rail := image.Rect(0, y-gtx.Dp(2), w, y+gtx.Dp(2))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x35), clip.UniformRRect(rail, gtx.Dp(2)).Op(gtx.Ops))
	x := int(float32(w)*slider.Value + 0.5)
	if x > 0 {
		rail.Max.X = x
		paint.FillShape(gtx.Ops, NRGBA(mix(a.theme.Accent, a.theme.Fg, 0.23), 0xff), clip.UniformRRect(rail, gtx.Dp(2)).Op(gtx.Ops))
	}
	radius := gtx.Dp(6)
	dot := image.Rect(x-radius, y-radius, x+radius, y+radius)
	paint.FillShape(gtx.Ops, NRGBA(0x000000, 0x52), clip.Ellipse(dot.Add(image.Pt(0, 1))).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(0xf2f4f7, 0xff), clip.Ellipse(dot).Op(gtx.Ops))
}

func (a *App) drawSettingToggle(gtx layout.Context, row image.Rectangle, button *widget.Clickable, label string, on bool) {
	a.drawSettingLabel(gtx, row, label)
	w, h := gtx.Dp(38), gtx.Dp(22)
	defer op.Offset(image.Pt(row.Max.X-w, row.Min.Y+(row.Dy()-h)/2)).Push(gtx.Ops).Pop()
	button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		semantic.LabelOp(label).Add(gtx.Ops)
		semantic.Switch.Add(gtx.Ops)
		semantic.SelectedOp(on).Add(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		rail := image.Rect(0, 0, w, h)
		bg := condColor(on, mix(a.theme.Panel, a.theme.Accent, 0.7), mix(a.theme.Panel, a.theme.Fg, 0.2))
		paint.FillShape(gtx.Ops, NRGBA(bg, 0xff), clip.UniformRRect(rail, h/2).Op(gtx.Ops))
		inset := gtx.Dp(3)
		x := inset
		if on {
			x = w - h + inset
		}
		dot := image.Rect(x, inset, x+h-2*inset, h-inset)
		paint.FillShape(gtx.Ops, NRGBA(0xf2f4f7, 0xff), clip.Ellipse(dot).Op(gtx.Ops))
		return layout.Dimensions{Size: rail.Size()}
	})
}

func (a *App) drawSettingStepper(gtx layout.Context, r image.Rectangle, id int, label, value string) {
	px, baseline := a.drawSettingLabel(gtx, r, label)
	w, h := gtx.Dp(34), gtx.Dp(30)
	y := r.Min.Y + (r.Dy()-h)/2
	x := r.Max.X - min(gtx.Dp(212), r.Dx()*3/5)
	prev := image.Rect(x, y, x+w, y+h)
	next := image.Rect(r.Max.X-w, y, r.Max.X, y+h)
	prevLabel, nextLabel := "−", "+"
	if id == setShell || id == setShellProfile {
		prevLabel, nextLabel = "‹", "›"
	}
	a.drawSettingsButton(gtx, prev, &a.settings.adjust[id][0], prevLabel, a.theme.PanelHi)
	a.drawSettingsButton(gtx, next, &a.settings.adjust[id][1], nextLabel, a.theme.PanelHi)
	value = a.textFit(value, px, next.Min.X-prev.Max.X-gtx.Dp(8))
	x = (prev.Max.X + next.Min.X - a.textMeasure(value, px)) / 2
	a.drawText(gtx, value, x, baseline, px, a.theme.Fg, 0xff)
}

func (a *App) drawWallpaperPicker(gtx layout.Context, r image.Rectangle, value string) {
	px, baseline := a.drawSettingLabel(gtx, r, "壁纸")
	h := gtx.Dp(30)
	y := r.Min.Y + (r.Dy()-h)/2
	choose := image.Rect(r.Max.X-gtx.Dp(62), y, r.Max.X, y+h)
	pickerCtx := gtx
	if a.settings.picker != nil {
		pickerCtx = gtx.Disabled()
	}
	a.drawSettingsButton(pickerCtx, choose, &a.settings.chooseWallpaper, "选择…", a.theme.PanelHi)
	right := choose.Min.X - gtx.Dp(7)
	button := image.Rect(right-gtx.Dp(76), y, right, y+h)
	a.drawSettingsButton(pickerCtx, button, &a.settings.clearWallpaper, "清除", a.theme.PanelHi)
	right = button.Min.X
	x := r.Min.X + min(gtx.Dp(116), r.Dx()/3)
	a.drawText(gtx, a.textFit(value, px, right-x-gtx.Dp(9)), x, baseline, px, a.theme.Dim, 0xff)
}

func (a *App) drawSettingLabel(gtx layout.Context, r image.Rectangle, label string) (px, baseline int) {
	px = a.uiPx(gtx, 13)
	baseline = r.Min.Y + (r.Dy()+a.textAscent(px))/2
	a.drawText(gtx, label, r.Min.X, baseline, px, a.theme.Fg, 0xff)
	return
}

func (s *settingsPanel) change(id, step int) {
	switch id {
	case setTerminalSize:
		s.draft.Font.Size = clampF(s.draft.Font.Size+float64(step), 8, 40)
	case setInterfaceSize:
		s.draft.InterfaceFont.Size = clampF(s.draft.InterfaceFont.Size+float64(step), 10, 18)
	case setLineHeight:
		s.draft.Font.LineHeight = clampF(math.Round(s.draft.Font.LineHeight*100+float64(step*5))/100, 0.8, 2)
	case setShell:
		if n := len(s.shells.profiles); n != 0 {
			s.draft.DefaultShell = (s.draft.DefaultShell + n + step) % n
		}
	case setShellProfile:
		if n := len(s.shells.profiles); n != 0 {
			s.shells.selected = (s.shells.selected + n + step) % n
			s.problem = ""
		}
	case setStartupTabs:
		s.draft.Tabs = min(9, max(1, s.draft.Tabs+step))
	case setScrollback:
		s.draft.Scrollback = min(200000, max(0, s.draft.Scrollback+step*1000))
	}
}

func (a *App) startWallpaperPicker() {
	s := &a.settings
	hwnd := a.hwnd.Load()
	if hwnd == 0 || s.picker != nil {
		return
	}
	path := expandPath(s.draft.Wallpaper)
	results := make(chan pickerResult, 1)
	s.picker = results
	go func() {
		value, err := chooseWallpaperFile(hwnd, path)
		results <- pickerResult{value: value, err: err}
		a.invalidate()
	}()
}

func (s *settingsPanel) receivePicker() {
	select {
	case result := <-s.picker:
		s.picker = nil
		if result.err != nil {
			s.problem = result.err.Error()
		} else if result.value != "" {
			s.problem = ""
			s.draft.Wallpaper = result.value
		}
	default:
	}
}

func (a *App) saveSettings() error {
	cfg := a.settings.draft
	shellsChanged := !slices.EqualFunc(cfg.Shells, a.cfg.Shells, func(left, right config.Shell) bool {
		return left.Name == right.Name && left.Command == right.Command && left.Dir == right.Dir && slices.Equal(left.Args, right.Args)
	})
	if cfg.Font.Family == a.cfg.Font.Family && cfg.Font.Size == a.cfg.Font.Size &&
		cfg.Font.LineHeight == a.cfg.Font.LineHeight && cfg.Scrollback == a.cfg.Scrollback &&
		cfg.CursorBlink == a.cfg.CursorBlink &&
		cfg.InterfaceFont == a.cfg.InterfaceFont && cfg.Wallpaper == a.cfg.Wallpaper &&
		cfg.Window == a.cfg.Window && cfg.Tabs == a.cfg.Tabs && cfg.DefaultShell == a.cfg.DefaultShell && !shellsChanged &&
		cfg.SingleWindow == a.cfg.SingleWindow && !a.settings.fonts.reload[terminalFont] && !a.settings.fonts.reload[interfaceFont] {
		return nil
	}
	base, labels := a.view.Family, a.view.InterfaceFamily
	defer func() {
		if base != a.view.Family {
			base.Close()
		}
		if labels != a.view.InterfaceFamily {
			labels.Close()
		}
	}()
	if cfg.Font.Family != a.cfg.Font.Family || a.settings.fonts.reload[terminalFont] {
		family, err := a.openFont(cfg.Font.Family)
		if err != nil {
			return fmt.Errorf("终端字体　%w", err)
		}
		base = family
	}
	if cfg.InterfaceFont.Family != a.cfg.InterfaceFont.Family || a.settings.fonts.reload[interfaceFont] {
		labels = nil
		if cfg.InterfaceFont.Family != "" {
			family, err := a.openFont(cfg.InterfaceFont.Family)
			if err != nil {
				return fmt.Errorf("界面字体　%w", err)
			}
			labels = family
		}
	}
	wallpaperPath := expandPath(cfg.Wallpaper)
	if cfg.Wallpaper != a.cfg.Wallpaper && wallpaperPath != "" {
		if err := a.wallpaper.load(wallpaperPath); err != nil {
			return fmt.Errorf("无法读取壁纸　%w", err)
		}
	}
	if err := config.SaveSettings(a.configPath, cfg, shellsChanged); err != nil {
		return fmt.Errorf("保存失败　%w", err)
	}
	if base != a.view.Family {
		a.view.Family.Close()
		a.view.SetFamily(base)
	}
	if labels != a.view.InterfaceFamily {
		a.view.InterfaceFamily.Close()
	}
	a.view.InterfaceFamily = labels
	a.view.SetLineHeight(cfg.Font.LineHeight)
	if cfg.Scrollback != a.cfg.Scrollback {
		for _, tab := range a.tabs {
			for _, pane := range tab.Panes {
				pane.SetScrollbackLimit(cfg.Scrollback)
			}
		}
	}
	a.cfg = cfg
	a.wallpaperPath = wallpaperPath
	if wallpaperPath == "" {
		a.wallpaper = wallpaper{}
	}
	a.view.BgAlpha = perfAlpha(cfg.Window.Opacity)
	return nil
}

func (a *App) drawSettingsButton(gtx layout.Context, r image.Rectangle, button *widget.Clickable, label string, bg uint32) {
	if r.Empty() {
		return
	}
	defer op.Offset(r.Min).Push(gtx.Ops).Pop()
	button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		if button.Hovered() || button.Pressed() || gtx.Focused(button) {
			if bg == 0 {
				bg = a.theme.PanelHi
			}
			bg = lighten(bg, 0.14)
		}
		if bg != 0 {
			paint.FillShape(gtx.Ops, NRGBA(bg, 0xff), clip.UniformRRect(image.Rectangle{Max: r.Size()}, gtx.Dp(5)).Op(gtx.Ops))
		}
		px := a.uiPx(gtx, 13)
		a.drawText(gtx, label, (r.Dx()-a.textMeasure(label, px))/2, (r.Dy()+a.textAscent(px))/2,
			px, a.theme.Fg, 0xff)
		return layout.Dimensions{Size: r.Size()}
	})
}
