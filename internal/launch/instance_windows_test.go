package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoordinateForwardsToAnAlreadyOpenWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gon.json")
	request := Request{
		Context: Context{Cwd: dir, Dir: dir, Env: []string{"PATH=caller", "EXAMPLE=中文=two"}},
		Command: []string{"pwsh.exe", "-Command", "& {\nWrite-Output \"hello\"\n}", ""},
	}
	first, forwarded, err := Coordinate(path, false, request)
	if err != nil || forwarded || first == nil {
		t.Fatalf("initial window: forwarded=%v, error=%v", forwarded, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	received := make(chan Request, 2)
	go func() {
		defer close(done)
		first.Serve(ctx, func(_ context.Context, r Request) error {
			received <- r
			if r.Command[0] == "missing.exe" {
				return errors.New("command unavailable")
			}
			return nil
		})
	}()
	if _, forwarded, err := Coordinate(path, false, request); err != nil || forwarded {
		t.Fatalf("independent window: forwarded=%v, error=%v", forwarded, err)
	}
	if _, forwarded, err := Coordinate(path, true, request); err != nil || !forwarded {
		t.Fatalf("single-window launch: forwarded=%v, error=%v", forwarded, err)
	}
	if got := <-received; !reflect.DeepEqual(got, request) {
		t.Fatalf("launch context changed: %+v", got)
	}
	request.Command = []string{"missing.exe"}
	if _, forwarded, err := Coordinate(path, true, request); !forwarded || err == nil || err.Error() != "command unavailable" {
		t.Fatalf("launch result: forwarded=%v, error=%v", forwarded, err)
	}
	<-received
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestLaunchRelayProcess$")
	child.Env = append(os.Environ(), "GON_RELAY_TEST="+path)
	child.Dir = dir
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("separate launcher: %v\n%s", err, output)
	}
	if got := <-received; got.Cwd != dir || !reflect.DeepEqual(got.Command, []string{"cmd.exe"}) {
		t.Fatalf("separate launcher request: %+v", got)
	}
}

func TestLaunchRelayProcess(t *testing.T) {
	path := os.Getenv("GON_RELAY_TEST")
	if path == "" {
		t.Skip("launch relay helper")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Context: Context{Cwd: cwd, Env: []string{"FROM=second process"}}, Command: []string{"cmd.exe"}}
	if _, forwarded, err := Coordinate(path, true, request); err != nil || !forwarded {
		t.Fatalf("launch relay: forwarded=%v, error=%v", forwarded, err)
	}
}
