package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSettingsRetainsCustomShellFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gon.json")
	const data = `{"shells":[{"name":"Bash","command":"bash.exe","args":["-i"],"profile_id":"dev"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Shells[0].Name = "Dev Bash"
	cfg.Shells[0].Args = nil
	if err := SaveSettings(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Shells []struct {
			Name      string   `json:"name"`
			Args      []string `json:"args"`
			ProfileID string   `json:"profile_id"`
		} `json:"shells"`
	}
	if err := json.Unmarshal(saved, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Shells) != 1 || document.Shells[0].Name != "Dev Bash" ||
		document.Shells[0].ProfileID != "dev" || len(document.Shells[0].Args) != 0 {
		t.Fatalf("saved profiles = %+v", document.Shells)
	}
}

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if len(c.Shells) == 0 {
		t.Fatal("no shells")
	}
	if c.Font.Size <= 0 {
		t.Fatal("bad font size")
	}
	if c.Window.Opacity <= 0 || c.Window.Opacity > 1 {
		t.Fatalf("opacity %v", c.Window.Opacity)
	}
}

func TestLoadCreatesDefaultFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "gon.json")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Shells) == 0 {
		t.Fatal("no shells")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default config not written: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gon.json")
	c := Default()
	c.Tabs = 3
	c.Font.Size = 18
	c.Theme.Accent = "#ff8800"
	c.Window.Opacity = 0.7
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Tabs != 3 || got.Font.Size != 18 || got.Theme.Accent != "#ff8800" || got.Window.Opacity != 0.7 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestNormalizeClampsValues(t *testing.T) {
	c := Config{
		DefaultShell: 99,
		Font:         Font{Size: 1000, LineHeight: 10},
		Window:       Window{Opacity: 5, Transparency: 2, Width: 10, Height: 10, Sidebar: 1, Padding: 999},
		Tabs:         100,
		Scrollback:   -5,
	}
	c.normalize()
	if c.DefaultShell != 0 {
		t.Fatalf("default shell = %d", c.DefaultShell)
	}
	if c.Font.Size != 15 || c.Font.LineHeight != 1.05 {
		t.Fatalf("font not clamped: %+v", c.Font)
	}
	if c.Window.Opacity != 0.82 || c.Window.Transparency != defaultWindowTransparency || c.Window.Width != 1180 || c.Window.Sidebar != 210 {
		t.Fatalf("window not clamped: %+v", c.Window)
	}
	if c.Tabs != 9 {
		t.Fatalf("tabs = %d", c.Tabs)
	}
	if c.Scrollback != 0 {
		t.Fatalf("scrollback = %d", c.Scrollback)
	}
}

func TestPathEnvOverride(t *testing.T) {
	t.Setenv("GON_CONFIG", `C:\tmp\custom.json`)
	if got := Path(); got != `C:\tmp\custom.json` {
		t.Fatalf("Path() = %q", got)
	}
}
