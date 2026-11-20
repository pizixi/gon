//go:build windows

package pty

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConPTYEcho starts a real cmd.exe inside ConPTY and checks that typed
// input is echoed back and that a command's output is captured.
func TestConPTYEcho(t *testing.T) {
	if !Available() {
		t.Skip("ConPTY unavailable")
	}
	s, err := Start("cmd.exe", []string{"/Q"}, "", 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()

	// Wait for the prompt before typing.
	time.Sleep(500 * time.Millisecond)
	if _, err := s.Write([]byte("echo MARK-42\r\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	deadline := time.After(8 * time.Second)
	for {
		mu.Lock()
		got := out.String()
		mu.Unlock()
		// The marker appears once as the echoed input and once as output.
		if strings.Count(got, "MARK-42") >= 2 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timed out waiting for output, got %q", out.String())
			mu.Unlock()
		case <-done:
			mu.Lock()
			t.Fatalf("session ended early, got %q", out.String())
			mu.Unlock()
		case <-time.After(25 * time.Millisecond):
		}
	}

	if err := s.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
}

func TestResolveCommand(t *testing.T) {
	if _, err := exec.LookPath("cmd.exe"); err != nil {
		t.Skip("cmd.exe not found")
	}
	exe, _, err := ResolveCommand("cmd.exe", nil)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(exe), "cmd.exe") {
		t.Fatalf("unexpected path %q", exe)
	}
	if _, _, err := ResolveCommand("definitely-not-a-real-binary-xyz", nil); err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestConPTYResolvesBatchFromCallerPath(t *testing.T) {
	if !Available() {
		t.Skip("ConPTY unavailable")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "commands")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "test launcher.cmd"), []byte("@echo off\r\necho %GON_TEST_BATCH%:%~1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "Path=commands", "PATHEXT=.CMD", "GON_TEST_BATCH=VALUE")
	s, err := StartWithOptions("test launcher", []string{"with spaces"}, Options{Dir: dir, Env: env}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mu sync.Mutex
	var output bytes.Buffer
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := s.Read(buf)
			mu.Lock()
			output.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-s.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("batch command did not exit")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(output.String(), "VALUE:with spaces") {
		t.Fatalf("batch output = %q", output.String())
	}
}

func TestQuoteArg(t *testing.T) {
	cases := map[string]string{
		"":          `""`,
		"plain":     "plain",
		"has space": `"has space"`,
		`a"b`:       `"a\"b"`,
		`c:\dir\`:   `c:\dir\`,
	}
	for in, want := range cases {
		if got := quoteArg(in); got != want {
			t.Errorf("quoteArg(%q) = %q, want %q", in, got, want)
		}
	}
}
