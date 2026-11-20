// Command gon is a native Windows terminal built with pure Go and Gio.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"gioui.org/app"

	"gon/internal/config"
	"gon/internal/launch"
	"gon/internal/ui"
)

func main() {
	path, request, err := parseArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gon: %v\n", err)
		os.Exit(2)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gon: config: %v\n", err)
	}
	instance, forwarded, err := launch.Coordinate(path, cfg.SingleWindow, request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gon: launch: %v\n", err)
		if cfg.SingleWindow {
			os.Exit(1)
		}
	}
	if forwarded {
		return
	}

	w := new(app.Window)
	w.Option(
		app.Title("Gon"),
		app.Decorated(false),
		ui.StartupWindowSize(cfg.Window),
	)

	a := ui.New(cfg, w, path)
	a.SetStartup(request)
	ctx, stop := context.WithCancel(context.Background())
	if instance != nil {
		go instance.Serve(ctx, a.DeliverLaunch)
	}
	go func() {
		err := a.Run()
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gon: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}
