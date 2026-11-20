package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gon/internal/pty"
	"gon/internal/term"
)

func TestClaudeInPowerShellTrace(t *testing.T) {
	if os.Getenv("GON_MOUSE_PROBE") != "1" {
		t.Skip("local CLI trace")
	}
	claude, err := exec.LookPath("claude.exe")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	work := filepath.Join(root, "workspace")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[],"models":[]}`)
			return
		}
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_local\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		for row := range 58 {
			data, _ := json.Marshal(fmt.Sprintf("probe-answer-%d-line-%02d 宽行用于检查分屏重排。The line also wraps and contains several words.\n\n", request, row))
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text_delta\":%s,\"text\":%s}}\n\n", data, data)
			w.(http.Flusher).Flush()
			time.Sleep(12 * time.Millisecond)
		}
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":500}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer api.Close()
	data, _ := json.Marshal(map[string]any{
		"hasCompletedOnboarding": true, "theme": "dark",
		"projects": map[string]any{filepath.ToSlash(work): map[string]any{"hasTrustDialogAccepted": true, "hasCompletedProjectOnboarding": true}},
	})
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(strings.ToUpper(entry), "=")
		if !strings.HasPrefix(key, "ANTHROPIC_") && !strings.HasPrefix(key, "CLAUDE_") {
			env = append(env, entry)
		}
	}
	env = append(env, "CLAUDE_CONFIG_DIR="+root, "ANTHROPIC_BASE_URL="+api.URL, "ANTHROPIC_AUTH_TOKEN=local-fixture",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
	if extra := os.Getenv("GON_MOUSE_PROBE_CLAUDE"); extra != "" {
		env = append(env, extra)
	}
	p, err := newPaneWithOptions("pwsh", "pwsh.exe", []string{"-NoProfile", "-NoLogo", "-NoExit", "-Command",
		"Write-Host '[probe] PowerShell profile loaded' -ForegroundColor Green; & '" + strings.ReplaceAll(claude, "'", "''") + "' --model claude-sonnet-4-6 --permission-mode plan"},
		pty.Options{Dir: work, Env: env}, 120, 36, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var raw bytes.Buffer
	var wireMu sync.Mutex
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := p.sess.Read(buf)
			if n > 0 {
				wireMu.Lock()
				raw.Write(buf[:n])
				p.applyOutput(buf[:n])
				wireMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	phase := func(name string) {
		t.Helper()
		wireMu.Lock()
		defer wireMu.Unlock()
		var data []string
		p.Screen(func(s *term.Screen) {
			for i := range s.TotalLines() {
				if text := s.Text(i); strings.Contains(text, "Claude Code") || strings.Contains(text, "profile loaded") || strings.Contains(text, "probe-answer-") && strings.Contains(text, "line-00") {
					data = append(data, fmt.Sprintf("%d=%s", i, text))
				}
			}
			t.Logf("%s: %dx%d cursor=%d,%d history=%d alt=%v mouse=%d/%d, matches %v", name, s.Cols, s.Rows, s.CursorX, s.CursorY, s.ScrollbackLen(), s.AltScreen(), s.MouseMode(), s.MouseEncoding(), data)
		})
		wire := raw.String()
		modeCodes := regexp.MustCompile(`\x1b\[(?:\?(?:[0-9]+;)*[0-9]+[hl]|\d*[JS]|\d+S)`).FindAllString(wire, -1)
		if len(modeCodes) < 75 {
			t.Logf("%s wire controls %q", name, modeCodes)
		}
		for _, marker := range []string{"Claude Code"} {
			prev := 0
			for {
				i := strings.Index(wire[prev:], marker)
				if i < 0 {
					break
				}
				i += prev
				t.Logf("%s wire banner at %d: %q", name, i, wire[max(0, i-120):min(len(wire), i+105)])
				prev = i + len(marker)
			}
		}
		raw.Reset()
	}
	if !waitText(t, p, ">", 8*time.Second) {
		t.Fatal(screenText(p))
	}
	time.Sleep(750 * time.Millisecond)
	phase("start")
	for _, size := range [][2]int{{75, 35}, {120, 14}, {84, 20}, {96, 36}} {
		p.Resize(size[0], size[1])
		time.Sleep(350 * time.Millisecond)
		phase("startup resize")
	}
	for request := 1; request <= 2; request++ {
		p.SendTextInput(fmt.Sprintf("中文测试 %d\r", request))
		deadline := time.Now().Add(8 * time.Second)
		for requests.Load() < int32(request) && time.Now().Before(deadline) {
			time.Sleep(35 * time.Millisecond)
		}
		if requests.Load() < int32(request) {
			t.Fatalf("could not submit %d:\n%s", request, screenText(p))
		}
		p.Resize(84, 21)
		time.Sleep(200 * time.Millisecond)
		p.Resize(96, 36)
		time.Sleep(1800 * time.Millisecond)
		phase("response")
	}
	p.SendText("\x1b[<64;12;8M\x1b[<64;12;8M\x1b[5~")
	time.Sleep(450 * time.Millisecond)
	phase("mouse wheel and paging")
}
