//go:build windows

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"

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
				`& {
$host.UI.RawUI.WindowTitle = "tab-$env:GON_TEST_TAB_NAME"
Write-Output "env=$env:GON_TEST_TAB_NAME,dir=$((Get-Item .).Name),only=$env:GON_TEST_TAB_ONLY_FIRST"
}`},
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
