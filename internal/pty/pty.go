// Package pty starts shells attached to a pseudo console and streams their
// output. On Windows it uses ConPTY, which gives full VT support.
package pty

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Session is one running shell attached to a terminal of a given size.
type Session struct {
	pty      ptmx
	once     sync.Once
	doneOnce sync.Once
	mu       sync.Mutex

	executable  string
	startingDir string
	// alive is false once the shell has exited.
	alive bool
	exit  int
	done  chan struct{}
	// closer is set by the platform starter; it releases the console.
	closer func()
}

// Options describes the environment and directories of the launching process.
type Options struct {
	Dir        string
	CallerDir  string
	Env        []string // nil inherits the local process environment
	RequireDir bool
}

// Start launches command with args inside a pseudo terminal of cols x rows.
func Start(command string, args []string, dir string, cols, rows int) (*Session, error) {
	return StartWithOptions(command, args, Options{Dir: dir}, cols, rows)
}

// StartWithOptions launches a process using an explicit launch environment.
func StartWithOptions(command string, args []string, options Options, cols, rows int) (*Session, error) {
	if command == "" {
		return nil, errors.New("empty command")
	}
	env := options.Env
	if env == nil {
		env = os.Environ()
	}
	values := environmentMap(env)
	cwd := options.CallerDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	dir := options.Dir
	if dir != "" {
		dir = absoluteFrom(cwd, dir, values)
		if !isDir(dir) {
			if options.RequireDir {
				return nil, fmt.Errorf("starting directory is unavailable: %s", dir)
			}
			dir = ""
		}
	}
	if dir == "" {
		dir = values["USERPROFILE"]
		if !isDir(dir) {
			dir = cwd
		}
	}

	s := &Session{done: make(chan struct{}), alive: true, exit: -1, startingDir: dir}
	p, closer, err := startPTY(command, args, dir, env, cols, rows, s)
	if err != nil {
		return nil, err
	}
	s.pty = p
	s.closer = closer
	return s, nil
}

// Executable returns the resolved path of the program started in the console.
func (s *Session) Executable() string { return s.executable }

// StartingDirectory returns the working directory used to start the program.
func (s *Session) StartingDirectory() string { return s.startingDir }

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Read reads shell output.
func (s *Session) Read(b []byte) (int, error) {
	if s.pty == nil {
		return 0, errors.New("closed")
	}
	return s.pty.Read(b)
}

// Write sends input bytes to the shell.
func (s *Session) Write(b []byte) (int, error) {
	if s.pty == nil {
		return 0, errors.New("closed")
	}
	return s.pty.Write(b)
}

// Resize changes the terminal size.
func (s *Session) Resize(cols, rows int) error {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if s.pty == nil {
		return nil
	}
	return s.pty.Resize(cols, rows)
}

// Close terminates the session.
func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.alive = false
		s.mu.Unlock()
		if s.closer != nil {
			s.closer()
		}
		if s.pty != nil {
			s.pty.Close()
		}
		s.closeDone()
	})
}

// Done is closed when the session exits.
func (s *Session) Done() <-chan struct{} { return s.done }

// Exited reports whether the shell has exited and its exit code.
func (s *Session) Exited() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.alive, s.exit
}

// setExit records the exit status and wakes waiters.
func (s *Session) setExit(code int) {
	s.mu.Lock()
	s.exit = code
	s.alive = false
	s.mu.Unlock()
}

// finishExit drains pending output, then marks the session done.
func (s *Session) finishExit(code int) {
	s.setExit(code)
	time.Sleep(40 * time.Millisecond)
	s.closeDone()
}

// closeDone closes the done channel exactly once.
func (s *Session) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

// ResolveCommand expands a command name to a full path.
func ResolveCommand(command string, args []string) (string, []string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	exe, err := resolveCommand(command, cwd, environmentMap(os.Environ()))
	return exe, args, err
}

func resolveCommand(command, dir string, env map[string]string) (string, error) {
	if command == "" {
		return "", errors.New("empty command")
	}
	extensions := strings.Split(env["PATHEXT"], ";")
	if env["PATHEXT"] == "" {
		extensions = []string{".com", ".exe", ".bat", ".cmd"}
	}
	find := func(path string) string {
		if filepath.Ext(path) != "" && isFile(path) {
			return path
		}
		for _, ext := range extensions {
			if ext == "" {
				continue
			}
			if ext[0] != '.' {
				ext = "." + ext
			}
			if candidate := path + ext; isFile(candidate) {
				return candidate
			}
		}
		return ""
	}
	if strings.ContainsAny(command, `/\:`) {
		if exe := find(absoluteFrom(dir, command, env)); exe != "" {
			return exe, nil
		}
	} else {
		for _, path := range append(filepath.SplitList(env["PATH"]), shellDirs(env)...) {
			if path != "" {
				if exe := find(filepath.Join(absoluteFrom(dir, path, env), command)); exe != "" {
					return exe, nil
				}
			}
		}
	}
	return "", fmt.Errorf("command not found: %s", command)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func absoluteFrom(dir, path string, env map[string]string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if vol := filepath.VolumeName(path); vol != "" {
		if !strings.EqualFold(vol, filepath.VolumeName(dir)) {
			dir = env["="+strings.ToUpper(vol)]
			if dir == "" {
				dir = vol + `\`
			}
		}
		path = path[len(vol):]
	}
	if len(path) > 0 && os.IsPathSeparator(path[0]) {
		return filepath.Clean(filepath.VolumeName(dir) + path)
	}
	return filepath.Join(dir, path)
}

func splitEnv(entry string) (name, value string, ok bool) {
	if len(entry) != 0 {
		if i := strings.IndexByte(entry[1:], '='); i >= 0 {
			return entry[:i+1], entry[i+2:], true
		}
	}
	return "", "", false
}

func environmentMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		if name, value, ok := splitEnv(entry); ok {
			values[strings.ToUpper(name)] = value
		}
	}
	return values
}

func shellDirs(env map[string]string) []string {
	var dirs []string
	if pf := env["PROGRAMFILES"]; pf != "" {
		dirs = append(dirs, filepath.Join(pf, "PowerShell", "7"), filepath.Join(pf, "PowerShell", "6"))
	}
	if pf := env["PROGRAMFILES(X86)"]; pf != "" {
		dirs = append(dirs, filepath.Join(pf, "PowerShell", "7"))
	}
	if la := env["LOCALAPPDATA"]; la != "" {
		dirs = append(dirs, filepath.Join(la, "Microsoft", "WindowsApps"))
	}
	if sd := env["SYSTEMROOT"]; sd != "" {
		dirs = append(dirs, filepath.Join(sd, "System32", "WindowsPowerShell", "v1.0"), filepath.Join(sd, "System32"))
	}
	return dirs
}

// buildCommandLine joins a program and its arguments into a Windows command line.
func buildCommandLine(exe string, args []string) string {
	var b strings.Builder
	b.WriteString(quoteArg(exe))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(quoteArg(a))
	}
	return b.String()
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
