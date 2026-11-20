package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gon/internal/config"
	"gon/internal/launch"
)

func parseArgs(args []string, output io.Writer) (string, launch.Request, error) {
	var path, dir string
	flags := flag.NewFlagSet("gon", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&path, "config", "", "path to gon.json")
	flags.StringVar(&dir, "d", "", "starting directory")
	flags.StringVar(&dir, "startingDirectory", "", "starting directory (same as -d)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: gon.exe [options] [--] [command [arguments...]]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return "", launch.Request{}, err
	}
	if path == "" {
		path = config.Path()
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", launch.Request{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", launch.Request{}, err
	}
	if dir != "" {
		dir, err = filepath.Abs(dir)
		if err != nil {
			return "", launch.Request{}, err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return "", launch.Request{}, fmt.Errorf("starting directory: %w", err)
		}
		if !info.IsDir() {
			return "", launch.Request{}, fmt.Errorf("starting directory %q is not a directory", dir)
		}
	}
	command := flags.Args()
	if len(command) != 0 && command[0] == "" {
		return "", launch.Request{}, fmt.Errorf("empty command")
	}
	return path, launch.Request{
		Context: launch.Context{Cwd: cwd, Dir: dir, Env: os.Environ()},
		Command: command,
	}, nil
}
