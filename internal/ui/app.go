package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"gon/internal/config"
	"gon/internal/fontset"
	"gon/internal/launch"
	"gon/internal/pty"
	"gon/internal/render"
	"gon/internal/term"
)

// App is the root UI state.
type App struct {
	cfg           config.Config
	configPath    string
	fontDirectory string
	wallpaperPath string
	wallpaper     wallpaper
	win           *app.Window
	hwnd          atomic.Uintptr
	theme         Theme
	windowAlpha   uint8

	view *render.View

	tabs   []*Tab
	active int

	hdrH int
	pad  int

	paneHeaderH  int
	paneInsetPx  int
	minPaneSize  image.Point
	dragPane     *Pane
	dropPane     *Pane
	dragStart    image.Point
	dragPoint    image.Point
	paneDragging bool
	dragID       pointer.ID
	resizeTile   *paneTile
	resizeOffset int

	selecting       *Pane
	selectionCursor func() (image.Point, bool)

	// clipboard
	clipboard     string
	clipWrite     bool
	clipWriteOnce sync.Once
	clipRead      *Pane
	clipTag       *clipTag
	clipIn        chan string
	outputPending atomic.Bool
	minimized     atomic.Bool

	// toast
	toast      string
	toastUntil time.Time

	winButtons  [3]widget.Clickable
	closeDialog closeDialog
	clickAt     time.Time
	clickPos    image.Point
	clickPane   *Pane
	clickCount  int
	titleCache  string

	sideScroll    int
	sidebarHidden bool
	revealTab     bool
	sidebarToggle widget.Clickable
	newTabButton  widget.Clickable
	hoverTab      *Tab
	tabTooltipAt  time.Time
	tabDrag       tabDrag

	searchOpen  bool
	searchQuery string
	searchAbs   int
	searchMiss  bool
	hoverMenu   bool
	menuOpen    bool
	menuScroll  int
	settings    settingsPanel

	lastSize        image.Point
	started         bool
	startup         launch.Request
	launches        chan pendingLaunch
	fullscreen      bool
	windowFocused   bool
	focusedTerminal *Pane
	imeTarget       atomic.Pointer[imeTarget]
	windowMaximized bool
	fontBase        int
	sidePx          int
}

const titleBarHeight = 42

type pendingLaunch struct {
	ctx     context.Context
	request launch.Request
	result  chan error
}

type clipTag struct{ pane *Pane }

func (*clipTag) ImplementsEventTag() {}
func (*clipTag) ImplementsEvent()    {}

// New creates the app from a config.
func New(cfg config.Config, win *app.Window, configPath string) *App {
	if configPath == "" {
		configPath = config.Path()
	}
	a := &App{
		cfg:           cfg,
		configPath:    configPath,
		fontDirectory: filepath.Join(filepath.Dir(configPath), "fonts"),
		wallpaperPath: expandPath(cfg.Wallpaper),
		win:           win,
		hdrH:          titleBarHeight,
		pad:           cfg.Window.Padding,
		paneHeaderH:   paneHeaderHeight,
		paneInsetPx:   paneHorizontalInset,
		minPaneSize:   image.Pt(minPaneWidth, minPaneHeight),
		fontBase:      int(cfg.Font.Size + 0.5),
		launches:      make(chan pendingLaunch, 16),
	}
	a.selectionCursor = func() (image.Point, bool) {
		return windowDragCursor(a.hwnd.Load())
	}
	a.theme = buildTheme(cfg)
	font, _ := fontset.ResolveChoice(a.fontDirectory, cfg.Font.Family)
	a.view = render.NewView(fontset.LoadFamily(font, cfg.Font.CJK), a.fontBase)
	a.view.SetLineHeight(cfg.Font.LineHeight)
	a.view.TransparentBackground = true
	if cfg.InterfaceFont.Family != "" {
		a.view.InterfaceFamily, _ = a.openFont(cfg.InterfaceFont.Family)
	}
	// Gio blends in linear space; compensate so the configured opacity reads
	// the same as it would in sRGB.
	a.view.BgAlpha = perfAlpha(clampF(cfg.Window.Opacity, 0.2, 1))
	a.clipIn = make(chan string, 8)
	a.view.CursorColor = a.theme.Cursor
	a.view.CursorBlinkMS = cfg.CursorBlink
	a.applyPalette()
	return a
}

func (a *App) openFont(value string) (*fontset.Family, error) {
	if value == "" {
		return fontset.LoadFamily("", a.cfg.Font.CJK), nil
	}
	path, err := fontset.ResolveChoice(a.fontDirectory, value)
	if err != nil {
		return nil, err
	}
	return fontset.OpenFamily(path, a.cfg.Font.CJK)
}

func buildTheme(cfg config.Config) Theme {
	t := Theme{
		Bg:        ParseColor(cfg.Theme.Background, 0x0b0f14),
		Fg:        ParseColor(cfg.Theme.Foreground, 0xe6edf3),
		Accent:    ParseColor(cfg.Theme.Accent, 0x4c9aff),
		Panel:     ParseColor(cfg.Theme.Panel, 0x0e131a),
		Selection: ParseColor(cfg.Theme.Selection, 0x3f6ea8),
		Cursor:    ParseColor(cfg.Theme.Cursor, 0x7ee787),
		TermBg:    ParseColor(cfg.Theme.Background, 0x0b0f14),
		TermFg:    ParseColor(cfg.Theme.Foreground, 0xe6edf3),
	}
	t.Dim = mix(t.Fg, t.Bg, 0.29)
	t.Border = lighten(t.Bg, 0.14)
	t.PanelHi = lighten(t.Panel, 0.08)
	if len(cfg.Theme.Palette) >= 16 {
		for i := 0; i < 16; i++ {
			t.Palette[i] = ParseColor(cfg.Theme.Palette[i], 0)
		}
	}
	return t
}

func (a *App) applyPalette() {
	pal := term.NewPalette(a.theme.TermFg, a.theme.TermBg)
	if len(a.cfg.Theme.Palette) >= 16 {
		for i := 0; i < 16; i++ {
			c := ParseColor(a.cfg.Theme.Palette[i], pal.Indexed[i])
			pal.SetIndexed(i, c)
		}
	}
	a.view.Pal = pal
}

// StartTabs opens the configured number of tabs.
func (a *App) StartTabs() error {
	a.started = true
	count := max(1, a.cfg.Tabs)
	if a.startup.Dir != "" || len(a.startup.Command) != 0 {
		count = 1
	}
	return a.openLaunch(a.startup, count)
}

// SetStartup sets the first launch request before the window starts.
func (a *App) SetStartup(request launch.Request) { a.startup = request }

// DeliverLaunch schedules a request on the UI event loop and waits for its result.
func (a *App) DeliverLaunch(ctx context.Context, request launch.Request) error {
	job := pendingLaunch{ctx: ctx, request: request, result: make(chan error, 1)}
	select {
	case a.launches <- job:
	case <-ctx.Done():
		return ctx.Err()
	}
	a.invalidate()
	select {
	case err := <-job.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) openLaunch(request launch.Request, count int) error {
	if len(request.Command) != 0 {
		command := request.Command[0]
		sh := config.Shell{Name: filepath.Base(command), Command: command, Args: request.Command[1:]}
		if request.Dir == "" {
			request.Dir = request.Cwd
		}
		_, err := a.newTab(sh, request.Context)
		return err
	}
	shell := a.cfg.DefaultShell
	if _, err := a.newShellTab(shell, request.Context); err != nil {
		found := false
		for i := range a.cfg.Shells {
			if i == shell {
				continue
			}
			if _, err := a.newShellTab(i, request.Context); err == nil {
				shell, found = i, true
				break
			}
		}
		if !found {
			return err
		}
	}
	for i := 1; i < count; i++ {
		if _, err := a.newShellTab(shell, request.Context); err != nil {
			return err
		}
	}
	return nil
}

// NewTab creates a tab with one pane running the given shell index.
func (a *App) NewTab(shellIdx int) (*Tab, error) {
	scope := a.startup.Context
	scope.Dir = ""
	return a.newShellTab(shellIdx, scope)
}

func (a *App) shellProfile(shellIdx int) (config.Shell, error) {
	if len(a.cfg.Shells) == 0 {
		return config.Shell{}, fmt.Errorf("no shells configured")
	}
	if shellIdx < 0 || shellIdx >= len(a.cfg.Shells) {
		shellIdx = 0
	}
	return a.cfg.Shells[shellIdx], nil
}

func (a *App) newShellTab(shellIdx int, scope launch.Context) (*Tab, error) {
	sh, err := a.shellProfile(shellIdx)
	if err != nil {
		return nil, err
	}
	return a.newTab(sh, scope)
}

func (a *App) newTab(sh config.Shell, scope launch.Context) (*Tab, error) {
	options := pty.Options{Dir: sh.Dir, CallerDir: scope.Cwd, Env: scope.Env}
	if scope.Dir != "" {
		options.Dir, options.RequireDir = scope.Dir, true
	}
	startup, err := pty.PrepareLaunch(sh.Command, sh.Args, options)
	if err != nil {
		return nil, err
	}
	p, err := a.newPane(sh.Name, startup, a.paneArea())
	if err != nil {
		return nil, err
	}
	t := &Tab{Title: p.ShellName(), launch: startup}
	t.addPane(p, SplitRight)
	a.tabs = append(a.tabs, t)
	a.activateTab(len(a.tabs) - 1)
	return t, nil
}

func (a *App) newPane(shellName string, startup pty.Launch, area image.Rectangle) (*Pane, error) {
	cols, rows := 80, 24
	if !area.Empty() {
		area = a.terminalArea(area)
		cols, rows = a.view.GridFrom(area.Dx(), area.Dy())
	}
	p, err := newPaneWithLaunch(shellName, startup, cols, rows, a.cfg.Scrollback)
	if err != nil {
		return nil, err
	}
	metrics := a.view.Metrics()
	p.parser.SetCellSize(image.Pt(metrics.CellW, metrics.CellH))
	p.scr.SetThemeColors(a.theme.TermFg, a.theme.TermBg, a.theme.Cursor)
	// OSC 52 arrives on the PTY read goroutine; hand it to the UI thread.
	p.SetClipboardHandler(func(s string) {
		select {
		case a.clipIn <- s:
		default:
		}
		a.invalidate()
	})
	go p.ReadLoop(func(chromeChanged bool) {
		if chromeChanged || p.visible.Load() {
			a.scheduleOutputFrame(p)
		}
	})
	return p, nil
}

// SplitActive divides the focused pane and reuses the tab's launch options.
func (a *App) SplitActive(mode SplitMode) {
	t := a.activeTab()
	if t == nil || t.Focused() == nil {
		return
	}
	if !t.CanSplit() {
		a.notify("最多支持 4 个分屏")
		return
	}
	t.Relayout(a.paneArea(), a.minPaneSize)
	focused := t.Focused()
	src := focused.Rect()
	span := mode.axis(src.Size())
	if span < 2*mode.axis(a.minPaneSize) {
		a.notify("空间不足，无法继续分屏")
		return
	}
	_, area := splitPaneArea(src, mode, (span+1)/2)
	p, err := a.newPane(focused.ShellName(), t.launch, paneBody(area, a.paneHeaderH))
	if err != nil {
		a.notify("启动失败：" + err.Error())
		return
	}
	a.resetPaneGesture()
	t.addPane(p, mode)
	p.visible.Store(true)
	a.invalidate()
}

func (a *App) activeTab() *Tab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *App) currentPane() *Pane {
	if t := a.activeTab(); t != nil {
		return t.Focused()
	}
	return nil
}

func (a *App) activateTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	if i != a.active {
		a.setActivePanesVisible(false)
		a.resetPaneGesture()
		a.selecting = nil
		a.active = i
		a.revealTab = true
	}
	a.setActivePanesVisible(true)
}

func (a *App) setActivePanesVisible(visible bool) {
	if t := a.activeTab(); t != nil {
		for _, p := range t.Panes {
			p.visible.Store(visible)
		}
	}
}

// Coalesce PTY bursts independently of interactive input and layout changes.
func (a *App) scheduleOutputFrame(p *Pane) {
	if a.win == nil || a.minimized.Load() {
		return
	}
	// TUI selections are painted by the application after receiving mouse
	// reports. Do not add the normal burst delay to that round trip. Gio
	// already merges immediate invalidations; synchronized output still shows
	// only completed application frames.
	if p.visible.Load() && p.mouseInputRecent(time.Now()) {
		a.invalidate()
		return
	}
	if !a.outputPending.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(time.Second/30, func() {
		a.outputPending.Store(false)
		if !a.minimized.Load() {
			a.invalidate()
		}
	})
}

// invalidate schedules a redraw when the app owns a window.
func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// perform forwards a system action when the app owns a window.
func (a *App) perform(act system.Action) {
	if a.win != nil {
		a.win.Perform(act)
	}
}

func (a *App) notify(msg string) {
	a.toast = msg
	a.toastUntil = time.Now().Add(2600 * time.Millisecond)
	a.invalidate()
}

// CloseTab closes the tab at index.
func (a *App) CloseTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	tab := a.tabs[i]
	a.tabs = slices.Delete(a.tabs, i, i+1)
	a.tabDrag = tabDrag{}
	if a.hoverTab == tab {
		a.hoverTab = nil
	}
	if i == a.active {
		a.resetPaneGesture()
		a.selecting = nil
		a.active = max(0, min(i, len(a.tabs)-1))
		a.revealTab = true
		a.setActivePanesVisible(true)
	} else if i < a.active {
		a.active--
	}
	if len(a.tabs) == 0 && a.searchOpen {
		a.closeSearch()
	}
	for _, p := range tab.Panes {
		p.Close()
	}
	a.invalidate()
}

func (a *App) closePane(p *Pane) {
	t := a.activeTab()
	if t == nil || p == nil {
		return
	}
	if len(t.Panes) == 1 && t.Focused() == p {
		a.CloseTab(a.active)
		return
	}
	a.resetPaneGesture()
	if a.selecting == p {
		a.selecting = nil
	}
	t.removePane(p)
	a.invalidate()
}

// CloseFocusedPane closes the pane in focus.
func (a *App) CloseFocusedPane() {
	a.closePane(a.currentPane())
}

// Run drives the window until it closes.
func (a *App) Run() error {
	var ops op.Ops
	defer a.shutdown()
	for {
		e := a.win.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			return e.Err
		case *app.ClosingEvent:
			a.handleWindowClosing(e)
		case app.FrameEvent:
			previous := a.cfg.Window
			a.lastSize = e.Size
			gtx := app.NewContext(&ops, e)
			a.layout(gtx)
			a.finishClipboard(gtx)
			e.Frame(gtx.Ops)
			if window := a.cfg.Window; window.Width != previous.Width || window.Height != previous.Height ||
				window.Position != previous.Position || window.Fullscreen != previous.Fullscreen {
				a.applyWindowGeometry()
			}
			a.updateWindowOpacity()
		case app.ConfigEvent:
			a.windowMaximized = e.Config.Mode == app.Maximized
			a.fullscreen = e.Config.Mode == app.Fullscreen
			wasMinimized := a.minimized.Swap(e.Config.Mode == app.Minimized)
			if wasMinimized && e.Config.Mode != app.Minimized {
				a.invalidate()
			}
			if a.windowFocused != e.Config.Focused {
				a.windowFocused = e.Config.Focused
				a.view.ResetCursorBlink()
				a.invalidate()
			}
		case app.Win32ViewEvent:
			if hwnd := e.HWND; a.hwnd.Swap(hwnd) != hwnd {
				a.titleCache = ""
				a.windowAlpha = 0
				if hwnd == 0 {
					continue
				}
				a.win.Run(func() {
					installWindowMoveFilter(hwnd)
					installTerminalIME(hwnd, a)
					styleWindow(hwnd)
					placeConfiguredWindow(hwnd, a.cfg.Window)
				})
				if a.cfg.Window.Fullscreen {
					a.perform(system.ActionFullscreen)
				}
				a.updateWindowOpacity()
			}
		}
		// Invalidate wakes a minimized window without necessarily drawing a frame.
		if len(a.launches) != 0 && isWindowMinimized(a.hwnd.Load()) {
			a.perform(system.ActionRaise)
		}
	}
}

func (a *App) shutdown() {
	a.settings.fonts.cancelDownload()
	for _, t := range a.tabs {
		for _, p := range t.Panes {
			p.Close()
		}
	}
	a.view.Family.Close()
	a.view.InterfaceFamily.Close()
}

func (a *App) layout(gtx layout.Context) {
	size := gtx.Constraints.Max
	a.lastSize = size

	// Scale dp-based config values for the current DPI once.
	a.pad = gtx.Dp(unit.Dp(float32(a.cfg.Window.Padding)))
	a.sidePx = gtx.Dp(unit.Dp(float32(a.cfg.Window.Sidebar)))
	a.hdrH = gtx.Dp(titleBarHeight)
	a.paneHeaderH = gtx.Dp(paneHeaderHeight)
	a.paneInsetPx = gtx.Dp(paneHorizontalInset)
	a.minPaneSize = image.Pt(gtx.Dp(minPaneWidth), gtx.Dp(minPaneHeight))
	if p := a.currentPane(); p != nil && p.outputChanged.Swap(false) {
		a.view.ResetCursorBlink()
	}
	for {
		select {
		case s := <-a.clipIn:
			a.clipboard = s
			a.clipWrite = true
		default:
			goto drained
		}
	}
drained:
	if px := gtx.Dp(unit.Dp(a.cfg.Font.Size)); px != a.fontBase {
		a.view.SetFontSize(px)
		a.fontBase = px
	}
	if !a.started && (a.win == nil || a.hwnd.Load() != 0) {
		// Defer tab creation until the window size is known so the initial
		// grid matches the visible area.
		if err := a.StartTabs(); err != nil {
			a.notify("启动 shell 失败：" + err.Error())
		}
	}
	for range len(a.launches) {
		job := <-a.launches
		err := job.ctx.Err()
		if err == nil {
			err = a.openLaunch(job.request, 1)
			if err != nil {
				a.notify("启动失败：" + err.Error())
			}
			a.perform(system.ActionRaise)
		}
		job.result <- err
	}
	dialogContext := gtx
	if a.closeDialog.open {
		gtx = gtx.Disabled()
	}
	if a.settings.open {
		a.settings.receivePicker()
		a.settings.receiveFontDownload()
		a.settings.updateSliders(gtx)
	}
	a.updateSidebarButtons(gtx)
	termRect := a.paneArea()
	sidebarRect := image.Rect(0, 0, termRect.Min.X, size.Y)
	var list sidebarList
	if !sidebarRect.Empty() {
		list = a.sidebarTabs(gtx, sidebarRect)
		if !list.area.Empty() && !a.settings.open {
			a.updateTabInput(gtx, list)
			a.updateTabScroll(gtx, list)
		}
	}
	a.paintBackground(gtx, size)
	if !sidebarRect.Empty() {
		sidebarContext := gtx
		if a.settings.open {
			sidebarContext = sidebarContext.Disabled()
		}
		a.drawSidebarToolbar(sidebarContext, sidebarRect, list.rowHeight)
	}
	a.drawPanes(gtx, termRect)
	active := a.activeTab()
	title := active.terminalTitle()
	a.updateWindowTitle(title)
	if !sidebarRect.Empty() {
		a.drawSidebar(gtx, sidebarRect, list, active, title)
	}
	a.drawTitleBar(gtx, image.Rect(0, 0, size.X, termRect.Min.Y), title)
	a.drawSearch(gtx, termRect)
	a.drawToast(gtx, size)
	a.drawMenu(gtx, size, sidebarRect)
	a.drawSettings(gtx, size)
	a.drawCloseDialog(dialogContext, size)
	a.updateTerminalFocus()
}

func (a *App) updateTerminalFocus() {
	var focused *Pane
	if a.windowFocused && !a.settings.open && !a.searchOpen && !a.closeDialog.open {
		focused = a.currentPane()
	}
	if focused == nil {
		a.imeTarget.Store(nil)
	} else {
		target := imeTarget{pane: focused, caret: focused.presented.caret}
		if current := a.imeTarget.Load(); current == nil || *current != target {
			a.imeTarget.Store(&target)
		}
	}
	if focused == a.focusedTerminal {
		return
	}
	if a.focusedTerminal != nil {
		a.focusedTerminal.sendFocus(false)
	}
	a.focusedTerminal = focused
	if focused != nil {
		focused.sendFocus(true)
	}
}

func (a *App) paintBackground(gtx layout.Context, size image.Point) {
	area := image.Rectangle{Max: size}
	path, blur, opacity := a.wallpaperPath, a.cfg.Window.Blur, a.view.BgAlpha
	if a.settings.open {
		draft := a.settings.draft
		blur, opacity = draft.Window.Blur, perfAlpha(draft.Window.Opacity)
		if draft.Wallpaper != a.cfg.Wallpaper {
			path = expandPath(draft.Wallpaper)
		}
	}
	if path != "" {
		if err := a.wallpaper.load(path); err == nil {
			defer clip.Rect(area).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, NRGBA(a.theme.Bg, 0xff))
			a.wallpaper.Layout(gtx, size, float32(blur))
			paint.Fill(gtx.Ops, NRGBA(a.theme.Bg, opacity))
			return
		}
	} else if a.wallpaper.path != "" {
		a.wallpaper = wallpaper{}
	}
	a.verticalGradient(gtx, area, lighten(a.theme.Bg, 0.04), darken(a.theme.Bg, 0.18))
}

func (a *App) verticalGradient(gtx layout.Context, r image.Rectangle, top, bottom uint32) {
	defer clip.Rect(r).Push(gtx.Ops).Pop()
	paint.LinearGradientOp{
		Stop1:  f32.Pt(0, float32(r.Min.Y)),
		Stop2:  f32.Pt(0, float32(r.Max.Y)),
		Color1: NRGBA(top, 0xff),
		Color2: NRGBA(bottom, 0xff),
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// drawTitleBar paints the custom title bar and its window buttons.
func (a *App) drawTitleBar(gtx layout.Context, bar image.Rectangle, title terminalTitle) {
	buttons := [...]struct {
		label  string
		action system.Action
	}{
		{"最小化", system.ActionMinimize},
		{"最大化", system.ActionMaximize},
		{"关闭窗口", system.ActionClose},
	}
	if a.windowMaximized || a.fullscreen {
		buttons[1].label = "还原"
		buttons[1].action = system.ActionUnmaximize
	}
	moveLeft := min(bar.Max.X, a.drawSidebarButtons(gtx, bar.Dy()))
	btnW := min(gtx.Dp(titleBarHeight), (bar.Max.X-moveLeft)/len(buttons))
	btnLeft := bar.Max.X - len(buttons)*btnW
	if !a.closeDialog.open && !a.fullscreen {
		area := clip.Rect(image.Rect(moveLeft, bar.Min.Y, btnLeft, bar.Max.Y)).Push(gtx.Ops)
		pointer.CursorDefault.Add(gtx.Ops)
		system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
		area.Pop()
	}

	titleArea := bar
	titleArea.Min.X = moveLeft + gtx.Dp(5)
	titleArea.Max.X = btnLeft - gtx.Dp(5)
	a.drawCenteredTitle(gtx, titleArea, title)
	for i, item := range buttons {
		x := btnLeft + i*btnW
		a.drawWinButton(gtx, image.Rect(x, bar.Min.Y, x+btnW, bar.Max.Y), &a.winButtons[i], item.label, item.action)
	}
}

func (a *App) drawWinButton(gtx layout.Context, r image.Rectangle, button *widget.Clickable, label string, action system.Action) {
	if button.Clicked(gtx) {
		a.perform(action)
	}
	defer op.Offset(r.Min).Push(gtx.Ops).Pop()
	button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorDefault.Add(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		bounds := image.Rectangle{Max: r.Size()}
		col := a.theme.Fg
		if a.win != nil && !a.windowFocused {
			col = a.theme.Dim
		}
		if button.Hovered() || button.Pressed() || gtx.Focused(button) {
			bg := NRGBA(a.theme.Fg, 0x0d)
			if action == system.ActionClose {
				col, bg = 0xffffff, NRGBA(0xc42b1c, 0xff)
			}
			paint.FillShape(gtx.Ops, bg, clip.Rect(bounds).Op())
		}
		drawWindowGlyph(gtx, bounds.Size(), action, col)
		return layout.Dimensions{Size: bounds.Size()}
	})
}

func drawWindowGlyph(gtx layout.Context, size image.Point, action system.Action, col uint32) {
	stroke := max(1, gtx.Dp(1))
	align := float32(stroke%2) / 2
	cx, cy := float32(size.X/2)+align, float32(size.Y/2)+align
	d := float32(gtx.Dp(5))
	x0, x1, y0, y1 := cx-d, cx+d, cy-d, cy+d
	var path clip.Path
	path.Begin(gtx.Ops)
	switch action {
	case system.ActionMinimize:
		path.MoveTo(f32.Pt(x0, cy))
		path.LineTo(f32.Pt(x1, cy))
	case system.ActionMaximize:
		path.MoveTo(f32.Pt(x0, y0))
		path.LineTo(f32.Pt(x1, y0))
		path.LineTo(f32.Pt(x1, y1))
		path.LineTo(f32.Pt(x0, y1))
		path.Close()
	case system.ActionUnmaximize:
		inset := float32(gtx.Dp(2))
		path.MoveTo(f32.Pt(x0+inset, y0+inset))
		path.LineTo(f32.Pt(x0+inset, y0))
		path.LineTo(f32.Pt(x1, y0))
		path.LineTo(f32.Pt(x1, y1-inset))
		path.LineTo(f32.Pt(x1-inset, y1-inset))
		path.MoveTo(f32.Pt(x0, y0+inset))
		path.LineTo(f32.Pt(x1-inset, y0+inset))
		path.LineTo(f32.Pt(x1-inset, y1))
		path.LineTo(f32.Pt(x0, y1))
		path.Close()
	case system.ActionClose:
		path.MoveTo(f32.Pt(x0, y0))
		path.LineTo(f32.Pt(x1, y1))
		path.MoveTo(f32.Pt(x0, y1))
		path.LineTo(f32.Pt(x1, y0))
	}
	paint.FillShape(gtx.Ops, NRGBA(col, 0xff), clip.Stroke{Path: path.End(), Width: float32(stroke)}.Op())
}

// drawSidebarToolbar draws the bottom action row: split buttons and menu.
func (a *App) drawSidebarToolbar(gtx layout.Context, area image.Rectangle, rowH int) {
	y := area.Max.Y - a.pad - rowH
	if y < area.Min.Y {
		return
	}
	inner := image.Rect(area.Min.X+a.pad/2, y, area.Max.X-a.pad/2, y+rowH)
	if inner.Dx() < 60 {
		return
	}
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x03), clip.UniformRRect(inner, gtx.Dp(9)).Op(gtx.Ops))
	actions := []struct {
		label string
		run   func()
	}{
		{"向右分屏", func() { a.SplitActive(SplitRight) }},
		{"向下分屏", func() { a.SplitActive(SplitDown) }},
		{"更多操作", func() {
			a.menuOpen = !a.menuOpen
			a.menuScroll = 0
		}},
	}
	for i, action := range actions {
		r := image.Rect(inner.Min.X+i*inner.Dx()/len(actions), inner.Min.Y,
			inner.Min.X+(i+1)*inner.Dx()/len(actions), inner.Max.Y)
		a.drawToolButton(gtx, r, action.label, i, action.run)
	}
}

type toolBtnTag struct{ idx int }

func (toolBtnTag) ImplementsEventTag() {}

func (a *App) drawToolButton(gtx layout.Context, r image.Rectangle, label string, idx int, fn func()) {
	tag := toolBtnTag{idx: idx}
	func() {
		defer clip.Rect(r).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		event.Op(gtx.Ops, tag)
	}()
	hover := false
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch pe.Kind {
		case pointer.Enter:
			hover = true
			a.invalidate()
		case pointer.Leave:
			a.invalidate()
		case pointer.Press:
			if isPrimaryPress(pe) {
				fn()
				a.invalidate()
			}
		}
	}
	if hover {
		paint.FillShape(gtx.Ops, NRGBA(a.theme.Fg, 0x06), clip.UniformRRect(r, gtx.Dp(6)).Op(gtx.Ops))
	}
	col := condColor(hover, a.theme.Fg, a.theme.Dim)
	a.drawToolIcon(gtx, r, idx, col)
}

// drawToolIcon renders the sidebar toolbar symbols with vector paths.
func (a *App) drawToolIcon(gtx layout.Context, r image.Rectangle, idx int, col uint32) {
	c := NRGBA(col, 0xff)
	s := float32(gtx.Dp(unit.Dp(5)))
	cx := float32(r.Min.X + r.Dx()/2)
	cy := float32(r.Min.Y + r.Dy()/2)
	line := func(x0, y0, x1, y1 float32) {
		var path clip.Path
		path.Begin(gtx.Ops)
		path.MoveTo(f32.Pt(x0, y0))
		path.LineTo(f32.Pt(x1, y1))
		paint.FillShape(gtx.Ops, c, clip.Stroke{Path: path.End(), Width: 1.1}.Op())
	}
	box := func() {
		var path clip.Path
		path.Begin(gtx.Ops)
		path.MoveTo(f32.Pt(cx-s, cy-s))
		path.LineTo(f32.Pt(cx+s, cy-s))
		path.LineTo(f32.Pt(cx+s, cy+s))
		path.LineTo(f32.Pt(cx-s, cy+s))
		path.Close()
		paint.FillShape(gtx.Ops, c, clip.Stroke{Path: path.End(), Width: 1.1}.Op())
	}
	switch idx {
	case 0: // split right
		box()
		line(cx, cy-s, cx, cy+s)
	case 1: // split down
		box()
		line(cx-s, cy, cx+s, cy)
	case 2: // menu
		for i := -1; i <= 1; i++ {
			dot := image.Rect(int(cx)+i*gtx.Dp(unit.Dp(4))-1, int(cy)-1,
				int(cx)+i*gtx.Dp(unit.Dp(4))+2, int(cy)+2)
			paint.FillShape(gtx.Ops, c, clip.UniformRRect(dot, 2).Op(gtx.Ops))
		}
	}
}

func (a *App) drawToast(gtx layout.Context, size image.Point) {
	if a.toast == "" || time.Now().After(a.toastUntil) {
		return
	}
	px := a.uiPx(gtx, 12)
	w := a.textMeasure(a.toast, px) + gtx.Dp(unit.Dp(30))
	h := gtx.Dp(unit.Dp(30))
	x := (size.X - w) / 2
	y := size.Y - h - gtx.Dp(unit.Dp(56))
	r := image.Rect(x, y, x+w, y+h)
	paint.FillShape(gtx.Ops, NRGBA(0x1b2029, perfAlpha(0.95)), clip.UniformRRect(r, gtx.Dp(unit.Dp(8))).Op(gtx.Ops))
	tw := a.textMeasure(a.toast, px)
	fg := condColor(a.toast == copiedToastText, 0xa6e3a1, a.theme.Fg)
	a.drawText(gtx, a.toast, r.Min.X+(r.Dx()-tw)/2, r.Min.Y+(r.Dy()+a.textAscent(px))/2, px, fg, 0xff)
	gtx.Execute(op.InvalidateCmd{At: a.toastUntil})
}

func (a *App) drawMenu(gtx layout.Context, size image.Point, sidebar image.Rectangle) {
	if !a.menuOpen {
		return
	}
	// Full-window dismiss layer; registered first so menu items stay on top.
	closeTag := menuCloseTag{}
	func() {
		defer clip.Rect(image.Rect(0, 0, size.X, size.Y)).Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, closeTag)
	}()

	// Build the item list: one entry per configured shell, then pane actions.
	type item struct {
		label string
		run   func()
	}
	items := []item{{"设置", a.openSettings}}
	for i, sh := range a.cfg.Shells {
		idx := i
		name := shellDisplayName(sh.Name, sh.Command)
		items = append(items, item{
			label: "新建 " + name,
			run: func() {
				if _, err := a.NewTab(idx); err != nil {
					a.notify("启动失败：" + err.Error())
				}
			},
		})
	}
	items = append(items,
		item{"向右分屏", func() { a.SplitActive(SplitRight) }},
		item{"向下分屏", func() { a.SplitActive(SplitDown) }},
		item{"关闭当前窗格", func() { a.CloseFocusedPane() }},
		item{"复制选中文本", func() { a.copySelection() }},
		item{"粘贴", func() { a.requestPaste() }},
	)

	px := a.uiPx(gtx, 12)
	x := sidebar.Min.X + a.pad
	w, rowH, inset := min(gtx.Dp(210), size.X-x), gtx.Dp(30), gtx.Dp(5)
	bottom := size.Y - a.pad - gtx.Dp(52)
	height := min(len(items)*rowH+2*inset, bottom-a.hdrH)
	if w <= 2*inset || height <= 2*inset {
		return
	}
	r := image.Rect(x, bottom-height, x+w, bottom)
	list := sidebarList{area: r.Inset(inset), rowHeight: rowH}
	filter := pointer.Filter{Target: menuScrollTag{}, Kinds: pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}}
	for {
		e, ok := gtx.Event(filter)
		if !ok {
			break
		}
		a.menuScroll += int(e.(pointer.Event).Scroll.Y)
	}
	a.menuScroll = list.clampScroll(a.menuScroll, len(items))
	paint.FillShape(gtx.Ops, NRGBA(0x151b23, perfAlpha(0.96)), clip.UniformRRect(r, gtx.Dp(unit.Dp(8))).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Border, 0x90), clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(unit.Dp(8))).Path(gtx.Ops), Width: 1}.Op())

	defer clip.Rect(list.area).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, menuScrollTag{})
	for i := a.menuScroll / rowH; i < len(items); i++ {
		ir := list.row(i, a.menuScroll)
		if ir.Min.Y >= list.area.Max.Y {
			break
		}
		it := items[i]
		tag := menuItemTag{idx: i}
		func() {
			defer clip.Rect(ir).Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			semantic.LabelOp(it.label).Add(gtx.Ops)
			semantic.Button.Add(gtx.Ops)
			event.Op(gtx.Ops, tag)
		}()
		hover := false
		clicked := false
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Enter})
			if !ok {
				break
			}
			if pe, ok := ev.(pointer.Event); ok {
				switch pe.Kind {
				case pointer.Enter:
					hover = true
				case pointer.Press:
					if isPrimaryPress(pe) {
						clicked = true
					}
				}
			}
		}
		if hover {
			paint.FillShape(gtx.Ops, NRGBA(lighten(a.theme.Panel, 0.12), 0xff), clip.UniformRRect(ir, gtx.Dp(unit.Dp(5))).Op(gtx.Ops))
		}
		pad := gtx.Dp(10)
		a.drawText(gtx, a.textFit(it.label, px, ir.Dx()-2*pad), ir.Min.X+pad, ir.Min.Y+(ir.Dy()+a.textAscent(px))/2, px, a.theme.Fg, 0xff)
		if clicked {
			a.menuOpen = false
			it.run()
			a.invalidate()
			return
		}
	}

	for {
		_, ok := gtx.Event(pointer.Filter{Target: closeTag, Kinds: pointer.Press})
		if !ok {
			break
		}
		a.menuOpen = false
		a.invalidate()
	}
}

type menuScrollTag struct{}
type menuItemTag struct{ idx int }

func (menuItemTag) ImplementsEventTag() {}

type menuCloseTag struct{}

func (menuCloseTag) ImplementsEventTag() {}

// ---------------------------------------------------------------------------
// clipboard

const copiedToastText = "已复制"

func (a *App) copySelection() {
	a.copyPaneSelection(a.currentPane())
}

func (a *App) copyPaneSelection(p *Pane) bool {
	if p == nil {
		return false
	}
	txt := p.SelectedText()
	if txt == "" {
		a.notify("没有选中文本")
		return false
	}
	a.clipboard = txt
	a.clipWrite = true
	a.notify(copiedToastText)
	return true
}

func (a *App) requestPaste() {
	if p := a.currentPane(); p != nil {
		a.clipRead = p
	}
}

func (a *App) finishClipboard(gtx layout.Context) {
	if a.clipWrite {
		a.clipWrite = false
		text := a.clipboard
		if text != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
		}
	}
	if tag := a.clipTag; tag != nil {
		for {
			ev, ok := gtx.Event(transfer.TargetFilter{Target: tag, Type: "application/text"})
			if !ok {
				break
			}
			dataEvent, ok := ev.(transfer.DataEvent)
			if !ok {
				continue
			}
			a.clipTag = nil
			reader := dataEvent.Open()
			data, err := io.ReadAll(reader)
			reader.Close()
			if err == nil {
				select {
				case <-tag.pane.done:
				default:
					hasImage := len(data) == 0 && a.win != nil && clipboardHasImage()
					tag.pane.pasteClipboard(string(data), hasImage)
				}
			}
			a.invalidate()
			break
		}
	}
	if p := a.clipRead; p != nil && a.clipTag == nil {
		a.clipRead = nil
		a.clipTag = &clipTag{pane: p}
		gtx.Execute(clipboard.ReadCmd{Tag: a.clipTag})
	}
}

// ---------------------------------------------------------------------------
// text helpers around render.Text

func (a *App) uiPx(gtx layout.Context, defaultSize float32) int {
	size := a.cfg.InterfaceFont.Size
	if size <= 0 {
		size = 13
	}
	return gtx.Dp(unit.Dp(defaultSize * float32(size) / 13))
}

func (a *App) textMeasure(s string, px int) int {
	return a.view.Measure(s, px)
}

func (a *App) textFit(s string, px, maxW int) string {
	return a.view.Fit(s, px, maxW)
}

func (a *App) textAscent(px int) int {
	return a.view.Ascent(px)
}

func (a *App) drawText(gtx layout.Context, s string, x, y, px int, col uint32, alpha uint8) int {
	return a.view.DrawUI(gtx, s, x, y, px, col, alpha)
}

// condColor picks yes when cond holds, else no.
func condColor(cond bool, yes, no uint32) uint32 {
	if cond {
		return yes
	}
	return no
}

// clampF bounds v to [lo,hi].
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// searchTag identifies the scrollback search field.
type searchTag struct{}

// drawSearch renders the scrollback search bar and handles its input.
func (a *App) drawSearch(gtx layout.Context, area image.Rectangle) {
	if !a.searchOpen {
		return
	}
	w := gtx.Dp(unit.Dp(300))
	h := gtx.Dp(unit.Dp(34))
	r := image.Rect(area.Max.X-w-gtx.Dp(unit.Dp(14)), area.Min.Y+gtx.Dp(unit.Dp(12)), area.Max.X-gtx.Dp(unit.Dp(14)), area.Min.Y+gtx.Dp(unit.Dp(12))+h)
	paint.FillShape(gtx.Ops, NRGBA(0x11161d, perfAlpha(0.97)), clip.UniformRRect(r, gtx.Dp(unit.Dp(8))).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Border, perfAlpha(0.7)), clip.Stroke{
		Path: clip.UniformRRect(r, gtx.Dp(unit.Dp(8))).Path(gtx.Ops), Width: 1,
	}.Op())

	tag := searchTag{}
	func() {
		defer clip.Rect(r).Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, tag)
	}()
	if !gtx.Focused(tag) {
		gtx.Execute(key.FocusCmd{Tag: tag})
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: tag},
			key.Filter{Focus: tag, Name: key.NameReturn},
			key.Filter{Focus: tag, Name: key.NameEnter},
			key.Filter{Focus: tag, Name: key.NameEscape},
			key.Filter{Focus: tag, Name: key.NameDeleteBackward},
			pointer.Filter{Target: tag, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.EditEvent:
			a.searchQuery += e.Text
			a.runSearch(true)
		case key.Event:
			switch e.Name {
			case key.NameEscape:
				a.closeSearch()
			case key.NameReturn, key.NameEnter:
				a.runSearch(!e.Modifiers.Contain(key.ModShift))
			case key.NameDeleteBackward:
				if q := []rune(a.searchQuery); len(q) > 0 {
					a.searchQuery = string(q[:len(q)-1])
					a.runSearch(true)
				}
			}
		case pointer.Event:
			if e.Kind == pointer.Press {
				a.closeSearch()
			}
		}
	}
	px := a.uiPx(gtx, 13)
	ascent := a.textAscent(px)
	text := a.searchQuery
	col := a.theme.Fg
	if a.searchMiss && text != "" {
		col = 0xff7b72
		text = text + "   无匹配"
	}
	if text == "" {
		text = "搜索回滚缓冲区…"
		col = a.theme.Dim
	}
	a.drawText(gtx, a.textFit(text, px, r.Dx()-gtx.Dp(unit.Dp(24))), r.Min.X+gtx.Dp(unit.Dp(12)), r.Min.Y+(r.Dy()+ascent)/2, px, col, 0xff)
}

// openSearch starts a scrollback search.
func (a *App) openSearch() {
	if a.currentPane() == nil {
		return
	}
	a.searchOpen = true
	a.searchQuery = ""
	a.searchMiss = false
	a.searchAbs = a.currentPane().ScrollbackLen()
	a.invalidate()
}

// closeSearch hides the search bar and returns focus to the terminal.
func (a *App) closeSearch() {
	a.searchOpen = false
	if p := a.currentPane(); p != nil {
		p.SelectionClear()
	}
	a.invalidate()
}

// runSearch jumps to the next or previous match.
func (a *App) runSearch(forward bool) {
	p := a.currentPane()
	if p == nil || a.searchQuery == "" {
		a.searchMiss = false
		return
	}
	abs, c0, c1, ok := p.Find(a.searchQuery, a.searchAbs, forward)
	if !ok {
		// Wrap around the buffer.
		if forward {
			abs, c0, c1, ok = p.Find(a.searchQuery, -1, true)
		} else {
			abs, c0, c1, ok = p.Find(a.searchQuery, 1<<30, false)
		}
	}
	a.searchMiss = !ok
	if ok {
		a.searchAbs = abs
		p.SelectAbs(abs, c0, abs, c1)
		p.ScrollToAbs(abs)
	}
	a.invalidate()
}
