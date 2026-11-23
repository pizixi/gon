package ui

import (
	"image"
	"strings"
	"testing"

	"gon/internal/config"
	"gon/internal/term"
)

func TestExecutableTitleUsesFullPath(t *testing.T) {
	const exe = `C:\Program Files\PowerShell\7\pwsh.exe`
	for _, tc := range []struct {
		title string
		match bool
	}{
		{exe, true},
		{` Administrator: "c:/program files/powershell/7/PWSH.EXE" `, true},
		{"管理员: " + exe, true},
		{"管理员：" + exe, true},
		{"管理员:Administrator:" + exe, true},
		{` aDmInIsTrAtOr： 管理员: "c:/program files/powershell/7/PWSH.EXE" `, true},
		{"Review: " + exe, false},
		{"管理员:Administrator: Review: " + exe, false},
		{`Administrator: D:\PowerShell\pwsh.exe`, false},
	} {
		label := sessionTabLabel(terminalTitle{name: tc.title, native: true}, exe, "PowerShell", "")
		if got := label.original != ""; got != tc.match {
			t.Errorf("title %q matched the executable path = %t, want %t", tc.title, got, tc.match)
		}
	}
}

func TestSessionTabLabelUsesProgramName(t *testing.T) {
	const (
		pwsh       = `C:\Tools\PowerShell\PWSH.EXE`
		powershell = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
		cmd        = `C:\Windows\System32\cmd.exe`
		wsl        = `C:\Windows\System32\wsl.exe`
		bash       = `C:\Program Files\Git\bin\bash.exe`
		nu         = `C:\Tools\nu.exe`
	)
	for _, tc := range []struct {
		exe, profile, title, want string
	}{
		{pwsh, "PowerShell", "", "pwsh"},
		{powershell, "Windows PowerShell", "", "powershell"},
		{powershell, "Windows PowerShell", "管理员: " + powershell, "powershell"},
		{cmd, "Command Prompt", "", "cmd"},
		{cmd, "Command Prompt", cmd, "cmd"},
		{cmd, "Dev Shell", cmd, "Dev Shell"},
		{wsl, "WSL", wsl, "wsl"},
		{pwsh, "PowerShell", "管理员:Administrator: Building", ""},
		{cmd, "Command Prompt", powershell, ""},
		{wsl, "WSL", "user@host: ~/gon", ""},
		{bash, "  Git Bash  ", "", "Git Bash"},
		{bash, "Git Bash", "管理员:Administrator:" + bash, "Git Bash"},
		{bash, "Git Bash", "MINGW64:/gon", ""},
		{bash, "Git Bash", `D:\Other\bash.exe`, ""},
		{nu, "", "", "nu"},
		{nu, "NU.EXE", nu, "nu"},
	} {
		title := terminalTitle{name: tc.title, native: tc.title != ""}
		want := tc.want
		if want == "" {
			want = tc.title
		}
		if label := sessionTabLabel(title, tc.exe, tc.profile, ""); label.name != want {
			t.Errorf("sessionTabLabel(%q, %q, %q) = %q, want %q", tc.title, tc.exe, tc.profile, label.name, want)
		}
	}
}

func TestSessionTabLabelUsesLaunchDirectory(t *testing.T) {
	const (
		exe   = `C:\tools\hulkbox\bin\codex.exe`
		dir   = `C:\tools`
		short = "tools · codex"
	)
	for _, tc := range []struct {
		title, want string
	}{
		{"", short},
		{"tools", short},
		{`管理员: Administrator: "TOOLS"`, short},
		{`"c:/tools/."`, short},
		{"codex", short},
		{"CODEX.EXE", short},
		{`D:\Other\tools`, `D:\Other\tools`},
		{"◐  tools", "◐  tools"},
		{"tools / Review changes", "tools / Review changes"},
		{"other-project", "other-project"},
	} {
		title := terminalTitle{name: tc.title, native: tc.title != ""}
		if label := sessionTabLabel(title, exe, "codex.exe", dir); label.full() != tc.want || label.original != "" {
			t.Errorf("title %q produced %+v, want %q with program details", tc.title, label, tc.want)
		}
	}
}

func TestTabLabelKeepsShellNameWhenFolderIsLong(t *testing.T) {
	a := New(config.Default(), nil, "")
	defer a.shutdown()
	const px = 13
	const want = "… · pwsh"
	label := tabLabel{name: "pwsh", folder: strings.Repeat("项目", 30)}
	if got := a.fitTabLabel(label, px, a.textMeasure(want, px)); got != want {
		t.Fatalf("fitted label = %q, want %q", got, want)
	}
}

func TestTabTitlesPreserveOSC(t *testing.T) {
	cfg := config.Default()
	cfg.CursorBlink = 0
	a := New(cfg, nil, "")
	defer a.shutdown()
	a.started = true
	first, second := &Tab{Title: "PowerShell"}, &Tab{Title: "Claude Code"}
	pane, agent := newInputPane(), newInputPane()
	pane.parser, agent.parser = term.NewParser(pane.scr), term.NewParser(agent.scr)
	first.addPane(pane, SplitRight)
	second.addPane(agent, SplitRight)
	a.tabs = []*Tab{first, second}
	a.activateTab(0)

	const working = "◐  Review internal/ui "
	if !agent.applyOutput([]byte("\x1b]2;"+working+"\x07\x1b]9;4;3;0\x07")) || agent.visible.Load() {
		t.Fatal("a title update on a background pane must notify the chrome")
	}
	h := &layoutTest{App: a, size: image.Pt(900, 601)}
	h.frame()
	h.controlBounds(t, working, 0)
	a.activateTab(1)
	for _, title := range []string{
		working, "◑  Review internal/ui ", "✳  Review internal/ui 2",
		` Administrator: C:\Windows\System32\cmd.exe `,
	} {
		agent.applyOutput([]byte("\x1b]2;" + title + "\x1b\\"))
		h.frame()
		header, row := h.controlBounds(t, title, 0), h.controlBounds(t, title, 1)
		plus := h.controlBounds(t, "新建标签页", 0)
		if header.Min.X < plus.Max.X || row.Max.X > a.sidePx {
			t.Fatalf("the title %q was not rendered in both places", title)
		}
		if got := second.terminalTitle(); got.caption() != title || got.activity != term.ProgressHidden {
			t.Fatalf("native title %q was modified: %+v", title, got)
		}
		if at, scheduled := h.router.WakeupTime(); scheduled && !at.IsZero() {
			t.Fatal("the native title scheduled an extra animation")
		}
	}
	if !agent.applyOutput([]byte("\x1b]2;\x07")) {
		t.Fatal("clearing the native title did not notify the chrome")
	}
	if got := second.terminalTitle(); got.name != second.Title || got.activity != term.ProgressIndeterminate {
		t.Fatalf("title after OSC reset = %+v", got)
	}
	agent.applyOutput([]byte("\x1b]9;4;0\x07"))
	if second.terminalTitle().activity != term.ProgressHidden {
		t.Fatal("clearing OSC progress did not stop the animation")
	}
	newPane := newInputPane()
	second.addPane(newPane, SplitRight)
	h.frame()
	if got := second.terminalTitle(); got.name != second.Title || got.activity != term.ProgressHidden {
		t.Fatalf("title after changing pane focus = %+v", got)
	}
}

func TestPerfAlpha(t *testing.T) {
	if got := perfAlpha(1); got != 0xff {
		t.Fatalf("perfAlpha(1) = %d", got)
	}
	if got := perfAlpha(0); got != 0 {
		t.Fatalf("perfAlpha(0) = %d", got)
	}
	if got := perfAlpha(0.84); got < 0xf0 {
		t.Fatalf("perfAlpha(0.84) = %#x, expected a near-opaque alpha", got)
	}
}
