//go:build windows

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"golang.org/x/sys/windows"

	"gon/internal/config"
	"gon/internal/launch"
	"gon/internal/pty"
	"gon/internal/term"
)

// screenText concatenates the visible rows of a pane.
func screenText(p *Pane) string {
	var b strings.Builder
	p.Screen(func(scr *term.Screen) {
		for y := 0; y < scr.Rows; y++ {
			b.WriteString(scr.Text(scr.ScrollbackLen() + y))
			b.WriteByte('\n')
		}
	})
	return b.String()
}

func waitText(t *testing.T, p *Pane, want string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(screenText(p), want) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (h *layoutTest) shellSidebarRow(t *testing.T, label string, alsoInHeader bool) image.Rectangle {
	t.Helper()
	index := 0
	if alsoInHeader {
		index = 1
	}
	row := h.controlBounds(t, label, index)
	if row.Max.X > h.sidePx {
		t.Fatalf("control %q at %v is outside the sidebar", label, row)
	}
	return row
}

func TestPaneRunsShell(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	p, err := NewPane("cmd", "cmd.exe", []string{"/Q"}, "", 80, 24, 1000)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer p.Close()
	go p.ReadLoop(nil)

	if !waitText(t, p, ">", 8*time.Second) {
		t.Fatalf("no prompt; screen=%q", screenText(p))
	}
	p.SendTextInput("echo HELLO-PANE\r")

	// The marker appears once as the echoed command and once as its output.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(screenText(p), "HELLO-PANE") >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("marker not found; screen=%q", screenText(p))
}

func TestShellMenuScrollsToLaterProfiles(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	cfg := config.Default()
	cfg.Shells = make([]config.Shell, 24)
	for i := range cfg.Shells {
		cfg.Shells[i] = config.Shell{Name: fmt.Sprintf("Shell %d", i+1), Command: "cmd.exe", Args: []string{"/D", "/Q"}}
	}
	a := New(cfg, nil, "")
	defer a.shutdown()
	a.started = true
	h := &layoutTest{App: a, size: image.Pt(config.MinWindowWidth, config.MinWindowHeight)}
	h.frame()
	h.clickControl(t, "更多操作", 0)
	row := h.controlBounds(t, "设置", 0)
	at := row.Min.Add(row.Size().Div(2))
	h.router.Queue(pointer.Event{
		Source: pointer.Mouse, Kind: pointer.Scroll,
		Position: f32.Pt(float32(at.X), float32(at.Y)), Scroll: f32.Pt(0, 2000),
	})
	h.frame()
	last := h.controlBounds(t, "新建 Shell 24", 0)
	if !last.In(image.Rectangle{Max: h.size}) {
		t.Fatalf("last Shell menu item was outside the window: %v", last)
	}
	h.click(last.Min.Add(last.Size().Div(2)))
	if pane := a.currentPane(); pane == nil || pane.ShellName() != "Shell 24" {
		t.Fatal("the last Shell profile could not be started from the menu")
	}
}

func TestConfiguredShellSidebarLabel(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	dir := filepath.Join(t.TempDir(), "tools")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "dev shell.cmd")
	if err := os.WriteFile(command, []byte("@echo off\r\necho SHELL-READY\r\nset /p GON_TEST_INPUT=\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Shells = []config.Shell{{Name: "Dev Shell", Command: command, Dir: dir}}
	a := New(cfg, nil, "")
	defer a.shutdown()
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	pane := a.currentPane()
	if pane == nil || !waitText(t, pane, "SHELL-READY", 5*time.Second) {
		t.Fatal("the configured shell did not start")
	}

	label := filepath.Base(dir) + " · Dev Shell"
	for _, title := range []string{command, "tools", dir, filepath.Base(command)} {
		pane.applyOutput([]byte("\x1b]2;" + title + "\x07"))
		h.frame()
		h.shellSidebarRow(t, label, false)
	}

	const customTitle = "Administrator: Reviewing changes"
	pane.applyOutput([]byte("\x1b]2;" + customTitle + "\x07"))
	h.frame()
	h.shellSidebarRow(t, customTitle, true)

	pane.applyOutput([]byte("\x1b]2;\x07"))
	h.frame()
	h.shellSidebarRow(t, label, false)
}

func TestForwardedTabKeepsCallerEnvironment(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	if _, err := exec.LookPath("pwsh.exe"); err != nil {
		t.Skip("PowerShell 7 unavailable")
	}
	root := t.TempDir()
	path := filepath.Join(root, "gon.json")
	requestFor := func(name string, onlyFirst bool) launch.Request {
		t.Helper()
		dir := filepath.Join(root, name+" directory")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var env []string
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(entry), "GON_TEST_TAB_") {
				env = append(env, entry)
			}
		}
		env = append(env, "GON_TEST_TAB_NAME="+name)
		if onlyFirst {
			env = append(env, "GON_TEST_TAB_ONLY_FIRST=present")
		}
		return launch.Request{
			Context: launch.Context{Cwd: root, Dir: dir, Env: env},
			Command: []string{"pwsh.exe", "-NoProfile", "-NoLogo", "-NoExit", "-Command",
				fmt.Sprintf(`& {
$host.UI.RawUI.WindowTitle = "tab-$env:GON_TEST_TAB_NAME"
Write-Output "env=$env:GON_TEST_TAB_NAME,dir=$((Get-Item .).Name),only=$env:GON_TEST_TAB_ONLY_FIRST"
Write-Output "arg=%s"
}`, name)},
		}
	}
	firstRequest, secondRequest := requestFor("FIRST", true), requestFor("SECOND", false)
	owner, forwarded, err := launch.Coordinate(path, false, firstRequest)
	if err != nil || forwarded {
		t.Fatalf("primary launch: forwarded=%v, error=%v", forwarded, err)
	}
	a := New(config.Default(), nil, path)
	defer a.shutdown()
	a.SetStartup(firstRequest)
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	defer func() { cancel(); <-serverDone }()
	go func() {
		defer close(serverDone)
		owner.Serve(ctx, a.DeliverLaunch)
	}()
	h := &layoutTest{App: a, size: image.Pt(1180, 760)}
	h.frame()
	if len(a.tabs) != 1 {
		t.Fatalf("startup opened %d tabs", len(a.tabs))
	}
	first := a.currentPane()
	result := make(chan error, 1)
	go func() {
		_, forwarded, err := launch.Coordinate(path, true, secondRequest)
		if err == nil && !forwarded {
			err = errors.New("launcher tried to create another window")
		}
		result <- err
	}()
	timeout := time.After(8 * time.Second)
	for opened := false; !opened; {
		h.frame()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
			opened = true
		case <-timeout:
			t.Fatal("launch request did not reach the window")
		case <-time.After(10 * time.Millisecond):
		}
	}
	second := a.currentPane()
	if len(a.tabs) != 2 || second == first {
		t.Fatal("forwarded launch did not select a new tab")
	}
	if !waitText(t, first, "env=FIRST,dir=FIRST directory,only=present", 8*time.Second) ||
		!waitText(t, second, "env=SECOND,dir=SECOND directory,only=\n", 8*time.Second) {
		t.Fatalf("wrong environment or working directory: %q / %q", screenText(first), screenText(second))
	}
	if first.Title() != "tab-FIRST" || second.Title() != "tab-SECOND" {
		t.Fatalf("wrong PowerShell titles: %q / %q", first.Title(), second.Title())
	}
	h.frame()
	h.shellSidebarRow(t, "tab-FIRST", false)
	h.shellSidebarRow(t, "tab-SECOND", true)
	var shields []image.Rectangle
	for _, node := range h.router.AppendSemantics(nil) {
		if node.Desc.Label == "管理员权限" {
			shields = append(shields, node.Desc.Bounds)
		}
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		header := h.controlBounds(t, "tab-SECOND", 0)
		if len(shields) != 1 || shields[0].Max.X > header.Min.X ||
			iabs(shields[0].Min.Y+shields[0].Max.Y-header.Min.Y-header.Max.Y) > 2 {
			t.Fatalf("expected one shield centered beside the active title %v, got %v", header, shields)
		}
	} else if len(shields) != 0 {
		t.Fatalf("ordinary session has an administrator shield: %v", shields)
	}

	firstTitle := "管理员:Administrator:" + first.sess.Executable()
	first.applyOutput([]byte("\x1b]2;" + firstTitle + "\x07"))
	second.applyOutput([]byte("\x1b]2;" + second.sess.Executable() + "\x07"))
	h.size = image.Pt(config.MinWindowWidth, config.MinWindowHeight)
	h.frame()
	const firstLabel = "FIRST directory · pwsh"
	h.shellSidebarRow(t, firstLabel, false)
	h.shellSidebarRow(t, "SECOND directory · pwsh", false)
	a.moveTab(0, 2)
	h.frame()
	row := h.shellSidebarRow(t, firstLabel, false)
	h.mouse(pointer.Move, row.Min.Add(row.Size().Div(2)), 0)

	wantTooltip := firstLabel
	if windows.GetCurrentProcessToken().IsElevated() {
		wantTooltip += "\n管理员权限"
	}
	wantTooltip += "\n启动目录  " + firstRequest.Dir + "\n原始标题  " + firstTitle
	assertTooltipHidden := func() {
		for _, node := range h.router.AppendSemantics(nil) {
			if node.Desc.Label == wantTooltip {
				t.Fatal("tab tooltip should be hidden")
			}
		}
	}
	assertTooltipHidden()
	a.tabTooltipAt = time.Now().Add(-time.Second)
	h.frame()
	tip := h.controlBounds(t, wantTooltip, 0)
	if tip.Min.X <= a.sidePx || !tip.In(image.Rectangle{Max: h.size}) {
		t.Fatalf("tooltip bounds %v outside terminal area", tip)
	}
	h.mouse(pointer.Move, tip.Min, 0)
	assertTooltipHidden()

	h.size = image.Pt(1180, 760)
	h.frame()
	a.SplitActive(SplitDown)
	split := a.currentPane()
	if split == second || len(a.activeTab().Panes) != 2 {
		t.Fatalf("splitting the forwarded tab failed: %s", a.toast)
	}
	if split.sess.Executable() != second.sess.Executable() || split.sess.StartingDirectory() != secondRequest.Dir {
		t.Fatalf("split started %q in %q", split.sess.Executable(), split.sess.StartingDirectory())
	}
	if !waitText(t, split, "env=SECOND,dir=SECOND directory,only=\narg=SECOND\n", 8*time.Second) {
		t.Fatalf("split did not inherit the forwarded launch: %q", screenText(split))
	}
}

func TestSplitKeepsOriginalShellProfile(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	startDir := t.TempDir()
	profileDir := filepath.Join(startDir, "unavailable at launch")
	t.Setenv("USERPROFILE", startDir)
	t.Setenv("GON_TEST_SPLIT_PROFILE", "original")
	cfg := config.Default()
	cfg.Shells = []config.Shell{
		{Name: "Default", Command: "cmd.exe", Args: []string{"/D", "/Q"}},
		{Name: "Custom Shell", Command: "cmd.exe", Dir: profileDir,
			Args: []string{"/D", "/Q", "/K", "echo PROFILE-%GON_TEST_SPLIT_PROFILE%"}},
	}
	a := New(cfg, nil, "")
	defer a.shutdown()
	a.lastSize = image.Pt(1180, 760)
	tab, err := a.NewTab(1)
	if err != nil {
		t.Fatal(err)
	}
	checkLaunch := func(p *Pane) {
		t.Helper()
		if p.ShellName() != "Custom Shell" || p.sess.StartingDirectory() != startDir {
			t.Fatalf("started profile %q in %q", p.ShellName(), p.sess.StartingDirectory())
		}
		if !waitText(t, p, "PROFILE-original", 5*time.Second) {
			t.Fatalf("wrong profile args or environment: %q", screenText(p))
		}
	}
	previous := tab.Focused()
	checkLaunch(previous)
	cfg.Shells[1].Args[3] = "echo changed"
	t.Setenv("GON_TEST_SPLIT_PROFILE", "changed")
	if err := os.Mkdir(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []SplitMode{SplitRight, SplitDown} {
		a.SplitActive(mode)
		pane := tab.Focused()
		if pane == previous || len(tab.Panes) != 2 {
			t.Fatalf("splitting the configured tab failed: %s", a.toast)
		}
		checkLaunch(pane)
		a.closePane(previous)
		previous = pane
	}
}

func TestCloseFirstTabKeepsOtherShellRunning(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	cfg := config.Default()
	cfg.Shells = []config.Shell{{Name: "cmd", Command: "cmd.exe", Args: []string{"/D", "/Q"}, Dir: t.TempDir()}}
	a := New(cfg, nil, "")
	defer a.shutdown()
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	first := a.currentPane()
	if first == nil {
		t.Fatal("first shell did not start")
	}
	h.clickControl(t, "新建标签页", 0)
	second := a.currentPane()
	if len(a.tabs) != 2 || second == first || second == nil {
		t.Fatal("sidebar plus did not create a second tab")
	}
	firstRow := a.sidebarTabs(layout.Context{}, image.Rect(0, 0, a.sidePx, h.size.Y)).row(0, a.sideScroll)
	hover := firstRow.Min.Add(firstRow.Size().Div(2))
	h.mouse(pointer.Move, hover, 0)
	h.clickControl(t, "关闭标签页", 0)
	if len(a.tabs) != 1 || a.currentPane() != second {
		t.Fatal("closing the first tab affected the second tab")
	}
	select {
	case <-first.done:
	default:
		t.Fatal("the first shell was left running")
	}
	if !waitText(t, second, ">", 5*time.Second) {
		t.Fatal("the surviving shell did not reach its prompt")
	}
	second.SendTextInput("echo TAB-SURVIVED\r")
	if !waitText(t, second, "\nTAB-SURVIVED\n", 5*time.Second) {
		t.Fatalf("the surviving shell did not answer: %q", screenText(second))
	}
	h.mouse(pointer.Move, image.Pt(a.sidePx+10, hover.Y), 0)
	h.mouse(pointer.Move, hover, 0)
	h.clickControl(t, "关闭标签页", 0)
	if a.currentPane() != nil {
		t.Fatal("the last tab did not close")
	}
	h.clickControl(t, "新建标签页", 0)
	if len(a.tabs) != 1 || a.currentPane() == nil {
		t.Fatal("a new tab could not be opened after closing all tabs")
	}
}

func TestPaneSelection(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	p, err := NewPane("cmd", "cmd.exe", []string{"/Q"}, "", 80, 24, 1000)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer p.Close()
	go p.ReadLoop(nil)

	if !waitText(t, p, ">", 8*time.Second) {
		t.Fatalf("no prompt; screen=%q", screenText(p))
	}
	p.SelectionStart(0, 0)
	p.SelectionExtend(20, 0)
	if got := p.SelectedText(); got == "" {
		t.Fatal("empty selection")
	}
	p.SelectionClear()
	if p.SelectedText() != "" {
		t.Fatal("selection not cleared")
	}
}

func TestPaneScrollAndResize(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	p, err := NewPane("cmd", "cmd.exe", []string{"/Q"}, "", 40, 10, 1000)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer p.Close()
	go p.ReadLoop(nil)

	if !waitText(t, p, ">", 8*time.Second) {
		t.Fatalf("no prompt; screen=%q", screenText(p))
	}
	for i := 0; i < 30; i++ {
		p.SendTextInput("echo line\r")
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		p.Screen(func(scr *term.Screen) { n = scr.ScrollbackLen() })
		if n >= 10 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	p.ScrollBy(5)
	if got := p.ScrollOffset(); got != 5 {
		t.Fatalf("scroll offset = %d, want 5", got)
	}
	p.ScrollBy(100000)
	if got := p.ScrollOffset(); got < 5 {
		t.Fatalf("scroll offset after clamp = %d", got)
	}
	p.ScrollBy(-100000)
	if got := p.ScrollOffset(); got != 0 {
		t.Fatalf("scroll offset after reset = %d", got)
	}

	p.Resize(60, 20)
	if c, r := p.Size(); c != 60 || r != 20 {
		t.Fatalf("size = %dx%d, want 60x20", c, r)
	}
}

func TestPaneKeepsScrollPositionDuringResizeRepaint(t *testing.T) {
	p := newInputPane()
	p.parser = term.NewParser(p.scr)
	const frame = "Workspace terminal v2.1.328\r\nUTF-8 console active"
	p.applyOutput([]byte("before\r\nanchor\r\n" + frame + "\r\ncontent\r\nprompt"))
	p.Resize(80, 3)
	p.ScrollBy(2)
	top := p.scr.ViewTop(p.ScrollOffset())
	if got := p.scr.Text(top); got != "anchor" {
		t.Fatalf("scrolled to %q", got)
	}
	p.applyOutput([]byte("\x1b[?2026h\x1b[2J\x1b[H" + frame + "\r\nprompt\x1b[?2026l"))
	top = p.scr.ViewTop(p.ScrollOffset())
	if got := p.scr.Text(top); got != "anchor" || p.ScrollbackLen() != 2 {
		t.Fatalf("repaint moved the viewport to %q with %d history rows", got, p.ScrollbackLen())
	}
}

func TestPaneExitDetection(t *testing.T) {
	if !pty.Available() {
		t.Skip("ConPTY unavailable")
	}
	p, err := NewPane("cmd", "cmd.exe", []string{"/c", "exit 3"}, "", 80, 24, 100)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer p.Close()
	go p.ReadLoop(nil)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok, code := p.Exited(); ok {
			if code != 3 {
				t.Fatalf("exit code = %d, want 3", code)
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("shell exit not detected")
}

func TestWordAndLineSelection(t *testing.T) {
	p := newInputPane()
	term.NewParser(p.scr).Write([]byte("hello world foo\r\nsecond line"))

	if s, e, ok := p.WordRange(0, 8); !ok || s != 6 || e != 10 {
		t.Fatalf("word range = %d..%d ok=%v, want 6..10", s, e, ok)
	}
	if s, e, ok := p.WordRange(0, 2); !ok || s != 0 || e != 4 {
		t.Fatalf("word range = %d..%d ok=%v, want 0..4", s, e, ok)
	}
	if e, ok := p.LineRange(0); !ok || e != 14 {
		t.Fatalf("line range end = %d ok=%v, want 14", e, ok)
	}

	p.SelectRange(0, 6, 0, 10)
	if got := p.SelectedText(); got != "world" {
		t.Fatalf("selected = %q, want %q", got, "world")
	}
}

func TestFindInScrollback(t *testing.T) {
	p := newInputPane()
	term.NewParser(p.scr).Write([]byte("alpha\r\nbeta\r\ngamma\r\nalpha two"))
	// Beta is at absolute line 1 of the 24-row screen; scrolling is not needed.
	abs, c0, c1, ok := p.Find("beta", -1, true)
	if !ok || c0 != 0 || c1 != 3 {
		t.Fatalf("find beta = %d %d..%d ok=%v", abs, c0, c1, ok)
	}
	// The next match after line 1 is "alpha two" at line 3.
	abs2, _, _, ok := p.Find("alpha", abs, true)
	if !ok || abs2 <= abs {
		t.Fatalf("second find = %d ok=%v", abs2, ok)
	}
	// Wrapping backwards from the start finds the last match.
	abs3, _, _, ok := p.Find("alpha", -1, false)
	if !ok || abs3 != abs2 {
		t.Fatalf("backward find = %d ok=%v, want %d", abs3, ok, abs2)
	}
	if _, _, _, ok := p.Find("missing", -1, true); ok {
		t.Fatal("found text that is not there")
	}
}
