// Package launch carries a terminal launch between Gon processes.
package launch

import (
	"errors"
	"path/filepath"
)

// Context is captured by the launcher, before handing a tab to another window.
type Context struct {
	Cwd string
	Dir string
	Env []string
}

// Request opens one tab. An empty Command selects the configured default shell.
type Request struct {
	Context
	Command []string
}

func (r Request) validate() error {
	if !filepath.IsAbs(r.Cwd) || (r.Dir != "" && !filepath.IsAbs(r.Dir)) || r.Env == nil {
		return errors.New("invalid launch context")
	}
	if len(r.Command) > 0 && r.Command[0] == "" {
		return errors.New("empty command")
	}
	return nil
}
