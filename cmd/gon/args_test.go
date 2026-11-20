package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseArgsPreservesChildCommandLine(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MY_CC_TITLE", "新标签页")
	child := []string{"pwsh.exe", "-NoExit", "-ExecutionPolicy", "Bypass", "-Command", "& {\nWrite-Output \"hello world\"\n}", ""}
	path, request, err := parseArgs(append([]string{"-config", "custom.json", "-d", "."}, child...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(cwd, "custom.json") || request.Dir != cwd || request.Cwd != cwd {
		t.Fatalf("unexpected paths: %s, %s, %s", path, request.Dir, request.Cwd)
	}
	if !slices.Equal(request.Command, child) || !slices.Contains(request.Env, "MY_CC_TITLE=新标签页") {
		t.Fatal("the child arguments or environment changed")
	}
}
