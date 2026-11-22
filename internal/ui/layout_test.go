package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"gon/internal/config"
	"gon/internal/fontset"
	"gon/internal/term"
)

type layoutTest struct {
	*App
	router input.Router
	ops    op.Ops
	size   image.Point
	metric unit.Metric
}

func (h *layoutTest) frame() {
	h.ops.Reset()
	metric := h.metric
	if metric.PxPerDp == 0 {
		metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	}
	gtx := layout.Context{
		Ops:         &h.ops,
		Source:      h.router.Source(),
		Now:         time.Now(),
		Metric:      metric,
		Constraints: layout.Exact(h.size),
	}
	h.layout(gtx)
	h.finishClipboard(gtx)
	h.router.Frame(&h.ops)
}

func (h *layoutTest) mouse(kind pointer.Kind, at image.Point, buttons pointer.Buttons) {
	h.router.Queue(pointer.Event{Kind: kind, Position: f32.Pt(float32(at.X), float32(at.Y)), Buttons: buttons})
	h.frame()
}

func (h *layoutTest) click(at image.Point) {
	h.mouse(pointer.Press, at, pointer.ButtonPrimary)
	h.mouse(pointer.Release, at, 0)
}

func (h *layoutTest) controlBounds(t *testing.T, label string, index int) image.Rectangle {
	t.Helper()
	var matches []image.Rectangle
	for _, node := range h.router.AppendSemantics(nil) {
		if node.Desc.Label == label {
			matches = append(matches, node.Desc.Bounds)
		}
	}
	slices.SortFunc(matches, func(a, b image.Rectangle) int { return a.Min.Y - b.Min.Y })
	if index >= len(matches) {
		t.Fatalf("control %q at index %d was not laid out", label, index)
	}
	return matches[index]
}

func (h *layoutTest) clickControl(t *testing.T, label string, index int) {
	t.Helper()
	r := h.controlBounds(t, label, index)
	h.click(r.Min.Add(r.Size().Div(2)))
}

func TestTerminalRightClickCopiesAndWheelUpShowsHistory(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	p := &Pane{scr: term.NewScreen(12, 2, 2), cols: 12, rows: 2}
	p.parser = term.NewParser(p.scr)
	tab := new(Tab)
	tab.addPane(p, SplitRight)
	a.tabs = []*Tab{tab}
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	p.applyOutput([]byte(strings.Repeat("line\r\n", p.rows+3) + "copy"))
	x, y := p.scr.Cursor()
	p.SelectRange(y, x-4, y, x-1)
	at := p.Rect().Min.Add(image.Pt(18, 18))
	h.mouse(pointer.Press, at, pointer.ButtonSecondary)
	if a.clipboard != "copy" {
		t.Fatalf("right click copied %q", a.clipboard)
	}
	h.mouse(pointer.Release, at, 0)
	p.SelectionClear()
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(float32(at.X), float32(at.Y)), Scroll: f32.Pt(0, -120)})
	h.frame()
	if got := p.ScrollOffset(); got != 2 {
		t.Fatalf("wheel up gave history offset %d", got)
	}
	p.applyOutput([]byte("\x1b[?1049hfull screen"))
	if p.ScrollOffset() != 0 || p.ScrollbackLen() != 0 {
		t.Fatal("alternate screen exposed shell history")
	}
}

func TestWindowControlsLeaveTitleBarDraggable(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	for _, middle := range []string{"最大化", "还原"} {
		a.windowMaximized = middle == "还原"
		h.frame()
		bounds := make(map[string]image.Rectangle)
		for _, node := range h.router.AppendSemantics(nil) {
			bounds[node.Desc.Label] = node.Desc.Bounds
		}
		right := h.size.X
		for _, label := range []string{"关闭窗口", middle, "最小化"} {
			r := bounds[label]
			if r.Empty() || r.Max.X != right || r.Min.Y != 0 || r.Max.Y != a.hdrH {
				t.Fatalf("window control %q has bounds %v", label, r)
			}
			center := r.Min.Add(r.Size().Div(2))
			if act, _ := h.router.ActionAt(f32.Pt(float32(center.X), float32(center.Y))); act == system.ActionMove {
				t.Fatalf("window control %q overlaps the drag area", label)
			}
			right = r.Min.X
		}
		if act, _ := h.router.ActionAt(f32.Pt(float32(a.paneArea().Min.X+20), float32(a.hdrH/2))); act != system.ActionMove {
			t.Fatal("the title bar above the terminal is not draggable")
		}
		if act, _ := h.router.ActionAt(f32.Pt(20, float32(a.hdrH/2))); act == system.ActionMove {
			t.Fatal("window drag extends into the sidebar controls")
		}
	}
}

func TestTitleBarUsesSpaceBesideSidebarControls(t *testing.T) {
	a := New(config.Default(), nil, "")
	defer a.shutdown()
	a.started = true
	caption := strings.Repeat("项目构建 / release · ", 14)
	tab := &Tab{Title: caption}
	tab.addPane(newInputPane(), SplitRight)
	a.tabs = []*Tab{tab}
	a.activateTab(0)

	for _, scale := range []float32{1, 1.5} {
		size := image.Pt(int(config.MinWindowWidth*scale), int(config.MinWindowHeight*scale))
		h := &layoutTest{App: a, size: size, metric: unit.Metric{PxPerDp: scale, PxPerSp: scale}}
		h.frame()
		plus := h.controlBounds(t, "新建标签页", 0)
		minimize := h.controlBounds(t, "最小化", 0)
		header := h.controlBounds(t, caption, 0)
		if header.Min.X < plus.Max.X || header.Min.X >= a.paneArea().Min.X || header.Max.X > minimize.Min.X {
			t.Fatalf("at scale %g title %v is outside the space between %v and %v", scale, header, plus, minimize)
		}
		if header.Dx() <= minimize.Min.X-a.paneArea().Min.X {
			t.Fatalf("at scale %g title did not use the reclaimed space: %v", scale, header)
		}
		center := plus.Min.Add(plus.Size().Div(2))
		if action, _ := h.router.ActionAt(f32.Pt(float32(center.X), float32(center.Y))); action == system.ActionMove {
			t.Fatal("the drag region overlaps the new tab button")
		}
	}
}

func TestSidebarToggleExpandsTerminal(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	tab := new(Tab)
	pane := newInputPane()
	tab.addPane(pane, SplitRight)
	a.tabs = []*Tab{tab}
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	before := a.paneArea()
	toggle := h.controlBounds(t, "隐藏侧栏", 0)
	plus := h.controlBounds(t, "新建标签页", 0)
	if plus.Max.X >= before.Min.X || plus.Min.X != toggle.Max.X+3 || plus.Max.Y > a.hdrH {
		t.Fatalf("new tab control is outside the sidebar header: %v", plus)
	}
	h.clickControl(t, "隐藏侧栏", 0)
	if !a.sidebarHidden || pane.Rect() != a.paneArea() || pane.Rect().Min.X != 0 {
		t.Fatalf("collapsed sidebar left terminal at %v", pane.Rect())
	}
	grid := a.view.Grid(a.terminalArea(pane.Rect()))
	cols, rows := grid.Cols, grid.Rows
	if c, r := pane.Size(); c != cols || r != rows {
		t.Fatalf("expanded terminal grid = %dx%d, want %dx%d", c, r, cols, rows)
	}
	if act, _ := h.router.ActionAt(f32.Pt(20, float32(a.hdrH/2))); act == system.ActionMove {
		t.Fatal("revealing the sidebar starts a window drag")
	}
	h.clickControl(t, "展开侧栏", 0)
	if a.sidebarHidden || pane.Rect() != before || a.currentPane() != pane {
		t.Fatalf("sidebar reopened with terminal %v and active tab %p", pane.Rect(), a.activeTab())
	}
}

func TestTabDragReordersWithoutChangingTheActiveShell(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	first, second, third := &Tab{Title: "First"}, &Tab{Title: "Second"}, &Tab{Title: "Third"}
	for _, tab := range []*Tab{first, second, third} {
		tab.addPane(newInputPane(), SplitRight)
	}
	a.tabs = []*Tab{first, second, third}
	a.activateTab(1)
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	for _, node := range h.router.AppendSemantics(nil) {
		if node.Desc.Label == "关闭标签页" {
			t.Fatal("a close button was shown before hover")
		}
	}
	r := h.controlBounds(t, "First", 0)
	from := r.Min.Add(r.Size().Div(2))
	to := image.Pt(from.X, h.controlBounds(t, "Third", 0).Max.Y-2)
	h.mouse(pointer.Press, from, pointer.ButtonPrimary)
	h.mouse(pointer.Move, to, pointer.ButtonPrimary)
	if a.tabDrag.tab != first || !a.tabDrag.moving || a.tabDrag.at != to || a.tabs[0] != first {
		t.Fatalf("tab drag did not preview the insert: %+v", a.tabDrag)
	}
	h.mouse(pointer.Release, to, 0)
	if !slices.Equal(a.tabs, []*Tab{second, third, first}) || a.activeTab() != second || a.tabDrag.tab != nil {
		t.Fatal("dropping a tab changed the active shell or produced the wrong order")
	}
	r = h.controlBounds(t, "First", 0)
	h.mouse(pointer.Move, r.Min.Add(r.Size().Div(2)), 0)
	close := h.controlBounds(t, "关闭标签页", 0)
	h.mouse(pointer.Move, image.Pt(a.sidePx+30, r.Min.Y), 0)
	for _, node := range h.router.AppendSemantics(nil) {
		if node.Desc.Label == "关闭标签页" {
			t.Fatal("the close button stayed visible after leaving the tab")
		}
	}
	h.mouse(pointer.Move, close.Min.Add(close.Size().Div(2)), 0)
	h.clickControl(t, "关闭标签页", 0)
	if !slices.Equal(a.tabs, []*Tab{second, third}) || a.activeTab() != second {
		t.Fatal("the reordered close button removed the wrong tab")
	}
}

func TestPaneHeaderCloseAndGridReflow(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	tb := new(Tab)
	left, right := newInputPane(), newInputPane()
	left.done = make(chan struct{})
	tb.addPane(left, SplitRight)
	tb.addPane(right, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(909, 601)}
	h.frame()
	assertTiled(t, tb, h.paneArea())

	// Click close on the non-focused pane, near the resize handle.
	h.click(image.Pt(left.Rect().Max.X-5, left.Rect().Min.Y+13))
	select {
	case <-left.done:
	default:
		t.Fatal("left pane was not closed")
	}
	if len(tb.Panes) != 1 || tb.Focused() != right || right.Rect() != h.paneArea() {
		t.Fatalf("remaining panes = %d, focus = %p, right rect = %v", len(tb.Panes), tb.Focused(), right.Rect())
	}
	for _, size := range []image.Point{image.Pt(909, 601), image.Pt(560, 360)} {
		h.size = size
		h.frame()
		area := a.terminalArea(h.paneArea())
		wantCols, wantRows := a.view.GridFrom(area.Dx(), area.Dy())
		if cols, rows := right.Size(); cols != wantCols || rows != wantRows {
			t.Fatalf("terminal grid = %dx%d, want %dx%d for %v", cols, rows, wantCols, wantRows, area)
		}
	}
}

func TestPaneDragSwapsAndDividersResizeTogether(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	tb := new(Tab)
	first, second, third := newInputPane(), newInputPane(), newInputPane()
	tb.addPane(first, SplitRight)
	tb.addPane(second, SplitRight)
	tb.addPane(third, SplitDown)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(909, 601)}
	h.frame()
	left, top := first.Rect(), second.Rect()

	from := left.Min.Add(image.Pt(35, 13))
	drop := top.Min.Add(image.Pt(35, 70))
	h.mouse(pointer.Press, from, pointer.ButtonPrimary)
	h.mouse(pointer.Move, drop, pointer.ButtonPrimary)
	if !a.paneDragging || a.dragPoint != drop || a.dropPane != second || first.Rect() != left || second.Rect() != top {
		t.Fatal("drag did not preview the target without moving the panes")
	}
	h.mouse(pointer.Release, drop, 0)
	if a.paneDragging || first.Rect() != top || second.Rect() != left || tb.Focused() != first {
		t.Fatal("drag did not swap the two positions and retain focus")
	}
	assertTiled(t, tb, h.paneArea())

	start := image.Pt(second.Rect().Max.X+2, left.Min.Y+95)
	end := start.Add(image.Pt(102, 0))
	h.mouse(pointer.Press, start, pointer.ButtonPrimary)
	h.mouse(pointer.Move, end, pointer.ButtonPrimary)
	h.mouse(pointer.Release, end, 0)
	if want := left.Max.X + 102; second.Rect().Max.X != want || first.Rect().Min.X != want || third.Rect().Min.X != want {
		t.Fatalf("panes did not follow the vertical divider: %v, %v, %v", second.Rect(), first.Rect(), third.Rect())
	}
	assertTiled(t, tb, h.paneArea())

	start = image.Pt(third.Rect().Min.X+44, third.Rect().Min.Y)
	end = image.Pt(start.X, h.size.Y+100)
	h.mouse(pointer.Press, start, pointer.ButtonPrimary)
	h.mouse(pointer.Move, end, pointer.ButtonPrimary)
	h.mouse(pointer.Release, end, 0)
	if first.Rect().Max.Y != third.Rect().Min.Y || third.Rect().Dy() != a.minPaneSize.Y {
		t.Fatalf("horizontal divider did not clamp to pane minimum: %v, %v", first.Rect(), third.Rect())
	}
	assertTiled(t, tb, h.paneArea())
}

func TestTerminalSingleClickDoesNotSelect(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	tb := new(Tab)
	p := newInputPane()
	term.NewParser(p.scr).Write([]byte("first second"))
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(909, 601)}
	h.frame()
	start := p.Rect().Min.Add(image.Pt(2, 2))
	h.click(start)
	if _, _, _, _, selected := p.SelectionRange(); selected {
		t.Fatal("a click highlighted a cell")
	}

	start.X += 6 * a.view.Metrics().CellW
	end := start.Add(image.Pt(5*a.view.Metrics().CellW, 0))
	h.mouse(pointer.Press, start, pointer.ButtonPrimary)
	h.mouse(pointer.Move, end, pointer.ButtonPrimary)
	h.mouse(pointer.Release, end, 0)
	if got := p.SelectedText(); got != "second" {
		t.Fatalf("drag selected %q, want second", got)
	}
}

func TestSplitPaneSelectionAndMouseUseTheDrawnGrid(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	tb := new(Tab)
	p := newInputPane()
	tb.addPane(newInputPane(), SplitRight)
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(1280, 720), metric: unit.Metric{PxPerDp: 1.5, PxPerSp: 1.5}}
	h.frame()
	p.parser = term.NewParser(p.scr)
	p.applyOutput([]byte("header\r\nA中👩🏻‍💻e\u0301Z"))
	h.frame()
	grid := p.presented.grid
	point := func(x, y int) image.Point {
		return grid.Bounds.Min.Add(image.Pt(x*grid.Metrics.CellW+grid.Metrics.CellW/2, y*grid.Metrics.CellH+grid.Metrics.CellH/2))
	}
	// Start on the trailing half of the wide Chinese cell and end on the
	// combined Latin cell. Both the highlight and clipboard cover whole glyphs.
	h.mouse(pointer.Press, point(2, 1), pointer.ButtonPrimary)
	h.mouse(pointer.Move, point(5, 1), pointer.ButtonPrimary)
	h.mouse(pointer.Release, point(5, 1), 0)
	if _, start, _, end, active := p.SelectionRange(); !active || start != 1 || end != 5 {
		t.Fatalf("selection columns = %d through %d, active=%v", start, end, active)
	}
	if got := p.SelectedText(); got != "中👩🏻‍💻e\u0301" {
		t.Fatalf("selection copied %q", got)
	}
	p.SelectionClear()
	p.applyOutput([]byte("\x1b[?1002;1006h"))
	h.frame()
	h.mouse(pointer.Press, point(4, 2), pointer.ButtonPrimary)
	h.mouse(pointer.Move, point(5, 2), pointer.ButtonPrimary)
	h.mouse(pointer.Release, point(5, 2), 0)
	for _, want := range []string{"\x1b[<0;5;3M", "\x1b[<32;6;3M", "\x1b[<0;6;3m"} {
		if got := takeWrite(t, p); got != want {
			t.Fatalf("mouse report = %q, want %q", got, want)
		}
	}
}

func TestTerminalDragPreviewsCurrentMousePosition(t *testing.T) {
	a := New(config.Default(), nil, "")
	a.started = true
	defer a.shutdown()
	tb := new(Tab)
	p := newInputPane()
	tb.addPane(newInputPane(), SplitRight)
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(1000, 540), metric: unit.Metric{PxPerDp: 1.5, PxPerSp: 1.5}}
	h.frame()
	term.NewParser(p.scr).Write([]byte("header\r\na b c d e f"))
	h.frame()
	grid := p.presented.grid
	point := func(col int) image.Point {
		m := grid.Metrics
		return grid.Bounds.Min.Add(image.Pt(col*m.CellW+m.CellW/2, m.CellH+m.CellH/2))
	}
	a.selectionCursor = func() (image.Point, bool) { return point(7), true }

	h.mouse(pointer.Press, point(2), pointer.ButtonPrimary)
	h.mouse(pointer.Move, point(4), pointer.ButtonPrimary)
	if _, start, _, end, active := p.SelectionRange(); !active || start != 2 || end != 7 {
		t.Fatalf("preview columns = %d through %d, active=%v", start, end, active)
	}
	t.Run("highlight", func(t *testing.T) {
		win, err := headless.NewWindow(h.size.X, h.size.Y)
		if err != nil {
			t.Skipf("headless GPU unavailable: %v", err)
		}
		defer win.Release()
		if err := win.Frame(&h.ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: h.size})
		if err := win.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		inside, outside := point(7), point(9)
		if img.RGBAAt(inside.X, inside.Y) == img.RGBAAt(outside.X, outside.Y) {
			t.Fatal("selection highlight did not reach the current mouse position")
		}
	})

	h.mouse(pointer.Release, point(5), 0)
	if got := p.SelectedText(); got != "b c" {
		t.Fatalf("released selection = %q, want b c", got)
	}
}

func TestSettingsControlsApplyAndSaveToActiveConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.json")
	if err := os.WriteFile(path, []byte(`{"font":{"size":15,"family":"Consolas","extra":34},"future":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, path)
	defer a.shutdown()
	a.started = true
	tb := new(Tab)
	p := newInputPane()
	tb.addPane(p, SplitRight)
	a.tabs = []*Tab{tb}
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	button := func(label string, index int) { h.clickControl(t, label, index) }

	button("更多操作", 0)
	button("设置", 0)
	if !a.settings.open {
		t.Fatal("settings did not open")
	}
	h.router.Queue(key.EditEvent{Text: "modal input"})
	h.frame()
	if len(p.writeQ) != 0 {
		t.Fatal("settings input reached the shell")
	}
	// Drag the appearance slider to roughly three quarters of its travel.
	const dialogW, dialogH = 526, 302
	panelLeft := (h.size.X - dialogW) / 2
	panelTop := a.hdrH + (h.size.Y-a.hdrH-dialogH)/2
	trackEnd := panelLeft + dialogW - 16 - 50
	trackStart := max(panelLeft+110+16+132, trackEnd-186)
	h.click(image.Pt(trackStart+(trackEnd-trackStart)*3/4, panelTop+18+43+21))
	opacity := a.settings.draft.Window.Opacity
	if opacity < 0.79 || opacity > 0.81 {
		t.Fatalf("slider set opacity to %v, want approximately 0.80", opacity)
	}
	h.click(image.Pt(trackStart+(trackEnd-trackStart)/2, panelTop+18+2*43+21))
	blur := a.settings.draft.Window.Blur
	if blur < 47 || blur > 49 {
		t.Fatalf("slider set blur to %d, want approximately 48", blur)
	}
	if a.settings.draft.Window.Transparency != 0.05 {
		t.Fatalf("legacy config inherited %v window transparency", a.settings.draft.Window.Transparency)
	}
	h.click(image.Pt(trackStart+(trackEnd-trackStart)/4, panelTop+18+3*43+21))
	transparency := a.settings.draft.Window.Transparency
	if transparency < 0.19 || transparency > 0.21 || a.cfg.Window.Transparency != 0.05 {
		t.Fatalf("slider preview = %v, saved value = %v", transparency, a.cfg.Window.Transparency)
	}
	imagePath := filepath.Join(filepath.Dir(path), "wallpaper.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 20, 40)))
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.settings.picker = make(chan pickerResult, 1)
	a.settings.picker <- pickerResult{value: imagePath}
	h.frame()

	button("字体", 0)
	button("+", 0) // Terminal font size.
	button("+", 1) // Interface font size.
	button("+", 2) // Terminal line spacing.
	interfaceFont := cfg.InterfaceFont.Family
	if fontPath := fontset.FontPath("segoeui.ttf"); fontPath != "" {
		font, err := os.ReadFile(fontPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(a.fontDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		fontPath = filepath.Join(a.fontDirectory, "Fixture.ttf")
		if err := os.WriteFile(fontPath, font, 0o600); err != nil {
			t.Fatal(err)
		}
		button("Segoe UI ▾", 0)
		button("Fixture", 0)
		interfaceFont = "app:Fixture.ttf"
	}
	button("终端", 0)
	button("›", 0)
	button("+", 0) // Startup tabs.
	button("−", 1) // Scrollback limit.
	button("光标闪烁", 0)
	button("单窗口启动", 0)
	button("保存", 0)
	h.frame()
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.settings.open || got.Font.Size != 16 || got.Font.LineHeight != 1.10 || got.Window.Opacity != opacity ||
		got.Window.Blur != blur || got.Window.Transparency != transparency ||
		got.DefaultShell != 1 || got.Wallpaper != imagePath || got.InterfaceFont.Size != 14 ||
		got.InterfaceFont.Family != interfaceFont || got.Tabs != 2 || got.Scrollback != 7000 || got.CursorBlink != 0 || !got.SingleWindow {
		t.Fatalf("settings were not saved: %+v", got)
	}
	if a.view.FontSize() != 16 || a.view.BgAlpha != perfAlpha(opacity) ||
		a.wallpaper.op.Size() != image.Pt(20, 40) || a.uiPx(layout.Context{}, 13) != 14 {
		t.Fatal("saved settings were not applied")
	}
	if fontset.FontPath("segoeui.ttf") != "" && (a.view.InterfaceFamily == nil || a.view.InterfaceFamily == a.view.Family) {
		t.Fatal("interface font did not stay independent of the terminal font")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"extra": 34`) || !strings.Contains(string(data), `"future": true`) {
		t.Fatalf("unrelated settings were lost: %s (%v)", data, err)
	}
	a.openSettings()
	h.frame()
	h.click(image.Pt(trackStart+(trackEnd-trackStart)/2, panelTop+18+3*43+21))
	if a.settings.draft.Window.Transparency == transparency {
		t.Fatal("the reopened settings did not preview a transparency change")
	}
	button("取消", 0)
	if a.settings.open || a.cfg.Window.Transparency != transparency {
		t.Fatal("cancel did not restore the saved transparency")
	}
}

func TestShellSettingsEditAndSaveProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gon.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, path)
	t.Cleanup(a.shutdown)
	a.started = true
	h := &layoutTest{App: a, size: image.Pt(config.MinWindowWidth, config.MinWindowHeight)}
	openEditor := func() {
		t.Helper()
		a.openSettings()
		h.frame()
		h.clickControl(t, "终端", 0)
		h.clickControl(t, "Shell 配置", 0)
	}
	enter := func(label string, editor *widget.Editor, value string) {
		t.Helper()
		h.clickControl(t, label, 0)
		h.router.Queue(key.Event{Name: "A", Modifiers: key.ModShortcut, State: key.Press})
		h.frame()
		start, end := editor.Selection()
		h.router.Queue(key.EditEvent{Range: key.Range{Start: min(start, end), End: max(start, end)}, Text: value})
		h.frame()
	}
	save := func() config.Config {
		t.Helper()
		h.clickControl(t, "保存", 0)
		if a.settings.open {
			t.Fatalf("could not save Shell profiles: %s", a.settings.problem)
		}
		got, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	openEditor()
	h.clickControl(t, "添加 Shell", 0)
	h.clickControl(t, "保存", 0)
	if !a.settings.open || a.settings.problem != "请输入 Shell 程序名或路径" || len(a.cfg.Shells) != len(cfg.Shells) {
		t.Fatal("an empty Shell profile was saved")
	}
	profile := a.settings.shells.current()
	const name = "项目 Shell"
	const command = `C:\Tools\Git for Windows\bin\bash.exe`
	const dir = `D:\Work Trees\gon`
	enter("名称", &profile.name, name)
	enter("程序", &profile.command, `"`+command+`"`)
	enter("参数 (JSON)", &profile.args, `["--login", "-i", "one two", "", "line\nnext"]`)
	enter("启动目录", &profile.dir, dir)
	h.clickControl(t, "返回终端", 0)
	h.clickControl(t, "‹", 0)
	got := save()
	last := len(cfg.Shells)
	if len(got.Shells) != last+1 || got.DefaultShell != last || a.cfg.DefaultShell != last {
		t.Fatalf("saved Shell count = %d, default = %d, live default = %d", len(got.Shells), got.DefaultShell, a.cfg.DefaultShell)
	}
	created := got.Shells[last]
	if created.Name != name || created.Command != command || created.Dir != dir ||
		!slices.Equal(created.Args, []string{"--login", "-i", "one two", "", "line\nnext"}) {
		t.Fatalf("saved Shell = %+v", created)
	}
	h.frame()
	h.clickControl(t, "更多操作", 0)
	h.controlBounds(t, "新建 "+name, 0)

	openEditor()
	h.clickControl(t, "›", 0)
	profile = a.settings.shells.current()
	enter("名称", &profile.name, "Bash")
	enter("程序", &profile.command, command)
	enter("参数 (JSON)", &profile.args, "[]")
	got = save()
	if first := got.Shells[0]; first.Name != "Bash" || first.Command != command || len(first.Args) != 0 || got.DefaultShell != last {
		t.Fatalf("edited Shell = %+v, default = %d", first, got.DefaultShell)
	}

	openEditor()
	h.clickControl(t, "删除 Shell", 0)
	h.clickControl(t, "取消", 0)
	if len(a.cfg.Shells) != last+1 || a.cfg.DefaultShell != last {
		t.Fatal("cancel applied Shell edits")
	}

	openEditor()
	h.clickControl(t, "删除 Shell", 0)
	got = save()
	if len(got.Shells) != last || got.DefaultShell != last-1 {
		t.Fatalf("after deleting the default Shell, count = %d, default = %d", len(got.Shells), got.DefaultShell)
	}
}

func TestWindowSettingsEditAndSaveGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gon.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, path)
	t.Cleanup(a.shutdown)
	a.started = true
	a.openSettings()
	h := &layoutTest{App: a, size: image.Pt(800, 540)}
	h.frame()
	h.clickControl(t, "窗口", 0)
	enter := func(label string, editor *widget.Editor, value string) {
		t.Helper()
		h.clickControl(t, label, 0)
		h.router.Queue(key.Event{Name: "A", Modifiers: key.ModShortcut, State: key.Press})
		h.frame()
		start, end := editor.Selection()
		if editor.SelectedText() != editor.Text() {
			t.Fatalf("%q did not select the input", label)
		}
		h.router.Queue(key.EditEvent{Range: key.Range{Start: min(start, end), End: max(start, end)}, Text: value})
		h.frame()
	}
	window := &a.settings.window
	enter("宽度", &window.width, "200")
	h.clickControl(t, "保存", 0)
	if !a.settings.open || a.settings.problem == "" || a.cfg.Window != cfg.Window {
		t.Fatal("an invalid width was saved")
	}

	enter("宽度", &window.width, "960")
	enter("高度", &window.height, "640")
	h.clickControl(t, "居中", 0)
	enter("X", &window.x, "-1200")
	enter("Y", &window.y, "80")
	h.clickControl(t, "全屏", 0)
	h.clickControl(t, "保存", 0)

	want := cfg.Window
	want.Width, want.Height, want.Fullscreen = 960, 640, true
	want.Position = config.WindowPosition{X: -1200, Y: 80}
	if a.settings.open || a.cfg.Window != want {
		t.Fatalf("applied window = %+v, want %+v", a.cfg.Window, want)
	}
	got, err := config.Load(path)
	if err != nil || got.Window != want {
		t.Fatalf("reopened window settings = %+v, %v", got.Window, err)
	}
}
