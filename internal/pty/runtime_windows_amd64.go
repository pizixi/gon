package pty

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed runtime/windows-amd64/* runtime/LICENSE
var conPTYFiles embed.FS

func bundledConsolePath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dll, err := conPTYFiles.ReadFile("runtime/windows-amd64/conpty.dll")
	if err != nil {
		return "", err
	}
	host, err := conPTYFiles.ReadFile("runtime/windows-amd64/OpenConsole.exe")
	if err != nil {
		return "", err
	}
	license, err := conPTYFiles.ReadFile("runtime/LICENSE")
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write(dll)
	digest.Write(host)
	dir := filepath.Join(cache, "gon", "conpty", fmt.Sprintf("%x", digest.Sum(nil)[:12]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, file := range []struct {
		name string
		data []byte
	}{{"LICENSE", license}, {"OpenConsole.exe", host}, {"conpty.dll", dll}} {
		if err := installConsoleFile(filepath.Join(dir, file.name), file.data); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "conpty.dll"), nil
}

func installConsoleFile(path string, data []byte) error {
	matches := func() bool {
		current, err := os.ReadFile(path)
		return err == nil && bytes.Equal(current, data)
	}
	if matches() {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil && !matches() {
		return err
	}
	return nil
}
