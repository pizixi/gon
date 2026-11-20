// Package config loads and saves the Gon configuration file.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Shell describes one entry in the "new tab" list.
type Shell struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Dir     string   `json:"dir,omitempty"`
}

// Font controls the terminal cell font.
type Font struct {
	// Family is a font family name such as "Cascadia Code" or "Consolas".
	// If empty (or not installed) a built-in monospace fallback is used.
	Family string `json:"family"`
	// Size is the cell font size in device independent pixels.
	Size float64 `json:"size"`
	// LineHeight is a multiplier applied to the natural line height.
	LineHeight float64 `json:"line_height"`
	// CJK lists fallback font files used when the primary face has no glyph.
	CJK []string `json:"cjk_fallback,omitempty"`
}

// InterfaceFont controls the family and base size of interface labels.
type InterfaceFont struct {
	Family string  `json:"family"`
	Size   float64 `json:"size"`
}

// MaxWindowTransparency leaves enough opacity to find and use the controls.
const MaxWindowTransparency = 0.80

const defaultWindowTransparency = 0.05

const (
	MinWindowWidth  = 560
	MinWindowHeight = 360
	MaxWindowSize   = 32767
)

// WindowPosition specifies either monitor centering or a desktop pixel coordinate.
type WindowPosition struct {
	Centered bool  `json:"centered"`
	X        int32 `json:"x"`
	Y        int32 `json:"y"`
}

// Window holds window geometry, transparency, and wallpaper settings.
type Window struct {
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	Fullscreen   bool           `json:"fullscreen"`
	Position     WindowPosition `json:"position"`
	Transparency float64        `json:"transparency"`
	Opacity      float64        `json:"opacity"` // wallpaper tint opacity, 0.2 to 1
	Blur         int            `json:"blur"`    // wallpaper blur strength, 0 disables
	Sidebar      int            `json:"sidebar_width"`
	Padding      int            `json:"padding"`
	Radius       int            `json:"radius"`
}

// Theme holds colors as "#rrggbb" strings.
type Theme struct {
	Background string   `json:"background"`
	Foreground string   `json:"foreground"`
	Cursor     string   `json:"cursor"`
	Selection  string   `json:"selection"`
	Accent     string   `json:"accent"`
	Panel      string   `json:"panel"`
	Palette    []string `json:"palette,omitempty"` // optional 16 color override
}

// Config is the on-disk configuration.
type Config struct {
	Shells       []Shell `json:"shells"`
	DefaultShell int     `json:"default_shell"`
	// Tabs is the number of tabs opened at startup (1 - 9).
	Tabs          int           `json:"startup_tabs"`
	SingleWindow  bool          `json:"single_window"`
	Font          Font          `json:"font"`
	InterfaceFont InterfaceFont `json:"interface_font"`
	Window        Window        `json:"window"`
	Theme         Theme         `json:"theme"`
	Wallpaper     string        `json:"wallpaper"`
	Scrollback    int           `json:"scrollback"`
	CursorBlink   int           `json:"cursor_blink_ms"`
}

// Default returns the configuration used when no config file exists.
func Default() Config {
	return Config{
		Shells: []Shell{
			{Name: "PowerShell", Command: "pwsh.exe", Args: []string{"-NoLogo"}},
			{Name: "Windows PowerShell", Command: "powershell.exe", Args: []string{"-NoLogo"}},
			{Name: "Command Prompt", Command: "cmd.exe"},
			{Name: "WSL", Command: "wsl.exe"},
		},
		DefaultShell: 0,
		Tabs:         1,
		Font: Font{
			Family:     "Cascadia Mono",
			Size:       15,
			LineHeight: 1.05,
			CJK:        []string{"msyh.ttc", "simhei.ttf", "simsun.ttc", "Deng.ttf"},
		},
		InterfaceFont: InterfaceFont{Family: "Segoe UI", Size: 13},
		Window: Window{
			Width:        1180,
			Height:       760,
			Position:     WindowPosition{Centered: true},
			Transparency: defaultWindowTransparency,
			Opacity:      0.82,
			Blur:         36,
			Sidebar:      210,
			Padding:      10,
			Radius:       10,
		},
		Theme: Theme{
			Background: "#0b0f14",
			Foreground: "#e6edf3",
			Cursor:     "#7ee787",
			Selection:  "#3f6ea8",
			Accent:     "#4c9aff",
			Panel:      "#0e131a",
		},
		Scrollback:  8000,
		CursorBlink: 530,
	}
}

// Path returns the config file location.
func Path() string {
	if p := os.Getenv("GON_CONFIG"); p != "" {
		return p
	}
	return defaultPath()
}

const fileName = "gon.json"

func defaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return fileName
	}
	return filepath.Join(dir, "gon", fileName)
}

// Load reads the config from path, writing a default file if it does not exist.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && filepath.IsAbs(path) && path == defaultPath() {
		if err := migrateLegacyConfig(path); err != nil {
			return cfg, err
		}
		data, err = os.ReadFile(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := Save(path, cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.normalize()
	return cfg, nil
}

// Publish the previous config and its fonts together on the first default launch.
func migrateLegacyConfig(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const legacyFile = "config.json"
	root := filepath.Dir(dir)
	legacyDir := filepath.Join(root, "mygo-term")
	if _, err := os.Stat(filepath.Join(legacyDir, legacyFile)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	stage, err := os.MkdirTemp(root, ".gon-migration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.CopyFS(stage, os.DirFS(legacyDir)); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(stage, legacyFile), filepath.Join(stage, fileName)); err != nil {
		return err
	}
	if err := os.Rename(stage, dir); err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return err
		}
		// Another instance finished the migration first.
	}
	return nil
}

func (c *Config) normalize() {
	if len(c.Shells) == 0 {
		c.Shells = Default().Shells
	}
	if c.DefaultShell < 0 || c.DefaultShell >= len(c.Shells) {
		c.DefaultShell = 0
	}
	if c.Font.Size < 8 || c.Font.Size > 40 {
		c.Font.Size = 15
	}
	if c.Font.LineHeight < 0.8 || c.Font.LineHeight > 2 {
		c.Font.LineHeight = 1.05
	}
	if c.InterfaceFont.Size < 10 || c.InterfaceFont.Size > 18 {
		c.InterfaceFont.Size = 13
	}
	if c.Window.Opacity < 0.2 || c.Window.Opacity > 1 {
		c.Window.Opacity = 0.82
	}
	if c.Window.Transparency < 0 || c.Window.Transparency > MaxWindowTransparency {
		c.Window.Transparency = defaultWindowTransparency
	}
	if c.Window.Width < 480 {
		c.Window.Width = 1180
	}
	if c.Window.Height < 320 {
		c.Window.Height = 760
	}
	c.Window.Width = min(MaxWindowSize, max(MinWindowWidth, c.Window.Width))
	c.Window.Height = min(MaxWindowSize, max(MinWindowHeight, c.Window.Height))
	if c.Window.Sidebar < 120 {
		c.Window.Sidebar = 210
	}
	if c.Window.Padding < 0 || c.Window.Padding > 64 {
		c.Window.Padding = 10
	}
	if c.Window.Radius < 0 || c.Window.Radius > 32 {
		c.Window.Radius = 10
	}
	if c.Tabs < 1 {
		c.Tabs = 1
	}
	if c.Tabs > 9 {
		c.Tabs = 9
	}
	if c.Scrollback < 0 {
		c.Scrollback = 0
	}
	if c.Scrollback > 200000 {
		c.Scrollback = 200000
	}
	if c.CursorBlink < 0 {
		c.CursorBlink = 0
	}
}

// Save writes the config as indented JSON.
func Save(path string, cfg Config) error {
	return writeJSON(path, cfg)
}

// SaveSettings updates the preferences exposed by the settings panel while
// preserving the other keys in the existing config file.
func SaveSettings(path string, cfg Config) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Save(path, cfg)
	}
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	if document == nil {
		document = make(map[string]json.RawMessage)
	}
	for _, update := range []struct {
		section string
		values  map[string]any
	}{
		{"", map[string]any{"wallpaper": cfg.Wallpaper, "startup_tabs": cfg.Tabs, "single_window": cfg.SingleWindow,
			"default_shell": cfg.DefaultShell, "scrollback": cfg.Scrollback, "cursor_blink_ms": cfg.CursorBlink}},
		{"font", map[string]any{"family": cfg.Font.Family, "size": cfg.Font.Size, "line_height": cfg.Font.LineHeight}},
		{"interface_font", map[string]any{"family": cfg.InterfaceFont.Family, "size": cfg.InterfaceFont.Size}},
		{"window", map[string]any{
			"width": cfg.Window.Width, "height": cfg.Window.Height, "fullscreen": cfg.Window.Fullscreen, "position": cfg.Window.Position,
			"transparency": cfg.Window.Transparency, "opacity": cfg.Window.Opacity, "blur": cfg.Window.Blur,
		}},
	} {
		fields := document
		if update.section != "" {
			fields = make(map[string]json.RawMessage)
		}
		if raw, ok := document[update.section]; ok && update.section != "" {
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
		}
		if fields == nil {
			fields = make(map[string]json.RawMessage)
		}
		for key, value := range update.values {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			fields[key] = raw
		}
		if update.section != "" {
			document[update.section], err = json.Marshal(fields)
			if err != nil {
				return err
			}
		}
	}
	return writeJSON(path, document)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".gon-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(append(data, '\n'))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
