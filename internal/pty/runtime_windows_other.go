//go:build windows && !amd64

package pty

func bundledConsolePath() (string, error) { return "", nil }
