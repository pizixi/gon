package pty

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"gon/internal/term"
)

const consoleInputSample = "\x1b[<0;4;3M\x1b[<32;6;3M\x1b[<0;6;3m\x1b[<64;6;3M中文🙂\r\x1bv\x1b[200~\x1b[201~"

func TestConsoleAcceptsMouseReportsAndUnicode(t *testing.T) {
	if os.Getenv("GON_CONSOLE_TEST_CHILD") == "1" {
		exerciseConsoleInput(t)
		return
	}
	if !Available() {
		t.Skip("ConPTY unavailable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := StartWithOptions(exe, []string{"-test.run=^TestConsoleAcceptsMouseReportsAndUnicode$"},
		Options{Env: append(os.Environ(), "GON_CONSOLE_TEST_CHILD=1")}, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !session.ModernConsole() {
		t.Skip("bundled console host unavailable")
	}
	screen := term.NewScreen(50, 10, 200)
	parser := term.NewParser(screen)
	parser.SetModernConsole(true)
	var raw bytes.Buffer
	var mu sync.Mutex
	go func() {
		var buf [8192]byte
		for {
			n, err := session.Read(buf[:])
			mu.Lock()
			raw.Write(buf[:n])
			parser.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	waitFor := func(marker string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			mu.Lock()
			found := strings.Contains(raw.String(), marker)
			mu.Unlock()
			if found {
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("waiting for %q, console wrote %q", marker, raw.String())
	}
	waitFor("console-ready")
	mu.Lock()
	mode, encoding := screen.MouseMode(), screen.MouseEncoding()
	mu.Unlock()
	if mode != term.MouseDrag || encoding != term.MouseSGR {
		t.Fatalf("application mouse request reached the terminal as %d/%d", mode, encoding)
	}
	if _, err := session.Write([]byte(consoleInputSample)); err != nil {
		t.Fatal(err)
	}
	waitFor("console-input-ok")
	mu.Lock()
	defer mu.Unlock()
	parser.Resize(37, 7)
	parser.Resize(66, 16)
	var count int
	for i := range screen.TotalLines() {
		if strings.HasPrefix(screen.Text(i), "output-line-") {
			count++
		}
	}
	if count != 45 {
		t.Fatalf("terminal retained %d of 45 output lines after resizing", count)
	}
}

func exerciseConsoleInput(t *testing.T) {
	t.Helper()
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	if err := windows.SetConsoleMode(in, windows.ENABLE_VIRTUAL_TERMINAL_INPUT|windows.ENABLE_MOUSE_INPUT|windows.ENABLE_EXTENDED_FLAGS); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(out, windows.ENABLE_PROCESSED_OUTPUT|windows.ENABLE_WRAP_AT_EOL_OUTPUT|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for i := range 45 {
		fmt.Fprintf(&output, "output-line-%02d\r\n", i)
	}
	fmt.Fprint(&output, "\x1b[?1002;1006;2004hconsole-ready\r\n")
	_, _ = os.Stdout.WriteString(output.String())
	input := make([]uint16, len(utf16.Encode([]rune(consoleInputSample))))
	for offset := 0; offset < len(input); {
		var n uint32
		if err := windows.ReadConsole(in, &input[offset], uint32(len(input)-offset), &n, nil); err != nil || n == 0 {
			t.Fatalf("read console input: %v", err)
		}
		offset += int(n)
	}
	if got := string(utf16.Decode(input)); got != consoleInputSample {
		t.Fatalf("console received %q", got)
	}
	_, _ = os.Stdout.WriteString("console-input-ok\r\n")
}
