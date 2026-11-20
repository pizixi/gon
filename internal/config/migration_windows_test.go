package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyDefaultConfigAndFonts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("GON_CONFIG", "")
	legacy := filepath.Join(root, "mygo-term")
	fonts := filepath.Join(legacy, "fonts")
	if err := os.MkdirAll(fonts, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"startup_tabs": 3, "custom_key": true}`
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fonts, "custom.ttf"), []byte("custom font"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := Path()
	if want := filepath.Join(root, "gon", "gon.json"); path != want {
		t.Fatalf("config path = %q, want %q", path, want)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tabs != 3 {
		t.Fatalf("migrated tab count = %d", cfg.Tabs)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != data {
		t.Fatalf("migrated config = %q, %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(filepath.Dir(path), "fonts", "custom.ttf"))
	if err != nil || string(b) != "custom font" {
		t.Fatalf("migrated font = %q, %v", b, err)
	}
}
