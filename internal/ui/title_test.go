package ui

import (
	"image"
	"testing"

	"gon/internal/config"
	"gon/internal/term"
)

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
		working, "◑  Review internal/ui ", "✳  Review internal/ui ",
		` Administrator: C:\Windows\System32\cmd.exe `,
	} {
		agent.applyOutput([]byte("\x1b]2;" + title + "\x1b\\"))
		h.frame()
		header, row := h.controlBounds(t, title, 0), h.controlBounds(t, title, 1)
		if header.Min.X < a.sidePx || row.Max.X > a.sidePx {
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
