package ui

import (
	"image"
	"io"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/transfer"

	"gon/internal/config"
	"gon/internal/term"
)

func TestSearchEditorAndNavigation(t *testing.T) {
	a := New(config.Default(), nil, "")

	t.Cleanup(a.shutdown)
	a.started = true
	p := &Pane{scr: term.NewScreen(12, 2, 256), cols: 12, rows: 2}
	p.parser = term.NewParser(p.scr)
	tab := new(Tab)
	tab.addPane(p, SplitRight)
	a.tabs = []*Tab{tab}
	h := &layoutTest{App: a, size: image.Pt(config.MinWindowWidth, config.MinWindowHeight)}
	h.frame()
	p.applyOutput([]byte("旧 目标\r\n" + strings.Repeat("line\r\n", p.rows+2) + "新 目标"))
	_, y := p.scr.Cursor()
	newest := p.ScrollbackLen() + y
	assertMatch := func(want int) {
		t.Helper()
		line, _, _, _, active := p.SelectionRange()
		if !active || line != want || p.SelectedText() != "目标" || a.search.miss {
			t.Fatalf("search selected %q on row %d, want row %d", p.SelectedText(), line, want)
		}
	}

	press := func(name key.Name, mods key.Modifiers) {
		h.router.Queue(key.Event{Name: name, State: key.Press, Modifiers: mods})
		h.frame()
	}
	press("F", key.ModCtrl|key.ModShift)
	h.clickControl(t, searchLabel, 0)
	if !h.router.Source().Focused(&a.search.editor) {
		t.Fatal("clicking the search field did not leave its editor focused")
	}
	press("V", key.ModShortcut)
	if !h.router.ClipboardRequested() {
		t.Fatal("paste did not go to the search editor")
	}
	h.router.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser {
		return io.NopCloser(strings.NewReader("目标"))
	}})
	h.frame()
	assertMatch(newest)
	h.clickControl(t, "下一个匹配", 0)
	assertMatch(0)
	press(key.NameReturn, key.ModShift)
	assertMatch(newest)
	press(key.NameReturn, 0)
	assertMatch(0)
	h.clickControl(t, "上一个匹配", 0)
	assertMatch(newest)

	press("A", key.ModShortcut)
	start, end := a.search.editor.Selection()
	h.router.Queue(key.EditEvent{Text: "没有结果", Range: key.Range{Start: min(start, end), End: max(start, end)}})
	h.frame()
	if !a.search.miss || a.search.query != "没有结果" || p.SelectedText() != "" {
		t.Fatal("editing the query kept an old result")
	}
	press(key.NameEscape, 0)
	if a.search.pane != nil || !h.router.Source().Focused(paneTag{p: p}) {
		t.Fatal("Escape did not close search and return focus to the pane")
	}
	press("F", key.ModCtrl|key.ModShift)
	h.clickControl(t, "关闭搜索", 0)
	if a.search.pane != nil {
		t.Fatal("the close button left search open")
	}
}

func TestArchivedBlankColumnsAndSearchAfterHistoryAdvances(t *testing.T) {
	p := &Pane{scr: term.NewScreen(20, 2, 3), cols: 20, rows: 2}
	p.parser = term.NewParser(p.scr)
	p.applyOutput([]byte("first needle\r\nsecond needle\r\nthird needle\r\nfourth needle\r\n"))
	p.SetScrollOffset(3)
	if first, last, ok := p.WordRange(0, 19); !ok || first != 19 || last != 19 {
		t.Fatalf("word range in archived blank tail = %d..%d, %t", first, last, ok)
	}
	p.SelectRange(0, 0, 1, 19)
	if got := p.SelectedText(); got != "first needle\nsecond needle" {
		t.Fatalf("copy across archived lines = %q", got)
	}
	p.SetScrollOffset(0)
	if !p.findAndSelect("needle", false, true) || !p.findAndSelect("needle", false, false) {
		t.Fatal("did not find the two newest matches")
	}
	p.applyOutput([]byte("new text\r\n"))
	if !p.findAndSelect("needle", true, false) {
		t.Fatal("search lost its current position when the ring advanced")
	}
	match, _, _, _, _ := p.SelectionRange()
	if got := p.scr.Text(match); got != "fourth needle" {
		t.Fatalf("next match after scrolling = %q", got)
	}
}
