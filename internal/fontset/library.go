package fontset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	localPrefix  = "app:"
	presetsDir   = ".presets"
	nerdFonts    = "https://raw.githubusercontent.com/ryanoasis/nerd-fonts/v3.5.1/"
	jetBrainsOFL = "https://raw.githubusercontent.com/JetBrains/JetBrainsMono/master/OFL.txt"
)

// Choice is a font in the application's own directory or a downloadable preset.
type Choice struct {
	Value, Label string
	Download     bool
}

type fontAsset struct{ name, url string }

type preset struct {
	id, label string
	assets    []fontAsset // the regular face comes first
}

func nerdPreset(id, label, directory, name string, styles []string, license string) preset {
	assets := make([]fontAsset, 0, len(styles)+2)
	for _, style := range styles {
		file := name + "-" + style + ".ttf"
		assets = append(assets, fontAsset{file, nerdFonts + "patched-fonts/" + directory + "/" + file})
	}
	assets = append(assets, fontAsset{"LICENSE.txt", license}, fontAsset{"NERD-FONTS-LICENSE.txt", nerdFonts + "LICENSE"})
	return preset{id, label, assets}
}

var standardStyles = []string{"Regular", "Bold", "Italic", "BoldItalic"}

var presets = []preset{
	nerdPreset("jetbrains-mono-nerd", "JetBrains Mono Nerd Font", "JetBrainsMono/Ligatures", "JetBrainsMonoNerdFontMono", standardStyles, jetBrainsOFL),
	{"meowsans-nerd", "MeowSans Nerd Font", []fontAsset{
		{"MeowSansNerd.ttf", "https://github.com/AirLongDian/MeowSans_Font/releases/latest/download/MeowSansNerd.ttf"},
		{"README.md", "https://raw.githubusercontent.com/AirLongDian/MeowSans_Font/main/README.md"},
		{"JETBRAINS-OFL.txt", jetBrainsOFL},
		{"SOURCE-HAN-LICENSE.txt", "https://raw.githubusercontent.com/adobe-fonts/source-han-sans/release/LICENSE.txt"},
	}},
	nerdPreset("caskaydia-cove-nerd", "Caskaydia Cove Nerd Font", "CascadiaCode", "CaskaydiaCoveNerdFontMono", standardStyles, nerdFonts+"patched-fonts/CascadiaCode/LICENSE"),
	nerdPreset("fira-code-nerd", "Fira Code Nerd Font", "FiraCode", "FiraCodeNerdFontMono", []string{"Regular", "Bold"}, nerdFonts+"patched-fonts/FiraCode/LICENSE"),
	nerdPreset("hack-nerd", "Hack Nerd Font", "Hack", "HackNerdFontMono", standardStyles, nerdFonts+"patched-fonts/Hack/LICENSE.md"),
	{"source-code-pro", "Source Code Pro", []fontAsset{
		{"SourceCodePro-Regular.ttf", "https://raw.githubusercontent.com/adobe-fonts/source-code-pro/release/TTF/SourceCodePro-Regular.ttf"},
		{"SourceCodePro-Bold.ttf", "https://raw.githubusercontent.com/adobe-fonts/source-code-pro/release/TTF/SourceCodePro-Bold.ttf"},
		{"SourceCodePro-It.ttf", "https://raw.githubusercontent.com/adobe-fonts/source-code-pro/release/TTF/SourceCodePro-It.ttf"},
		{"SourceCodePro-BoldIt.ttf", "https://raw.githubusercontent.com/adobe-fonts/source-code-pro/release/TTF/SourceCodePro-BoldIt.ttf"},
		{"LICENSE.md", "https://raw.githubusercontent.com/adobe-fonts/source-code-pro/release/LICENSE.md"},
	}},
}

func (p preset) value() string {
	return localPrefix + presetsDir + "/" + p.id + "/" + p.assets[0].name
}

// ChoiceName displays the human-readable name for a stored font choice.
func ChoiceName(value string) string {
	for _, p := range presets {
		if value == p.value() {
			return p.label
		}
	}
	if path, ok := strings.CutPrefix(value, localPrefix); ok {
		return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return value
}

// ResolveChoice converts a library-relative selection to a filename. Legacy
// system family names and absolute font paths keep their existing meaning.
func ResolveChoice(root, value string) (string, error) {
	relative, ok := strings.CutPrefix(value, localPrefix)
	if !ok {
		return value, nil
	}
	relative = filepath.FromSlash(relative)
	if !filepath.IsLocal(relative) || !isFont(relative) {
		return "", errors.New("无效的应用字体路径")
	}
	root, err := filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err == nil {
		var path string
		path, err = filepath.EvalSymlinks(filepath.Join(root, relative))
		if err == nil {
			if relative, err := filepath.Rel(root, path); err == nil && filepath.IsLocal(relative) {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("找不到应用字体 %s", ChoiceName(value))
}

// Choices returns installed fonts first and the remaining presets below them.
func Choices(root string) ([]Choice, error) {
	var installed, downloadable []Choice
	for _, p := range presets {
		choice := Choice{Value: p.value(), Label: p.label, Download: !p.installed(root)}
		if choice.Download {
			downloadable = append(downloadable, choice)
		} else {
			installed = append(installed, choice)
		}
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() || !isFont(entry.Name()) || !validFontHeader(path) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := localPrefix + filepath.ToSlash(relative)
		installed = append(installed, Choice{Value: value, Label: ChoiceName(value)})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return append(installed, downloadable...), err
}

func isFont(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc":
		return true
	}
	return false
}

func validFontHeader(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var header [4]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false
	}
	switch string(header[:]) {
	case "\x00\x01\x00\x00", "OTTO", "true", "ttcf":
		return true
	}
	return false
}

func (p preset) installed(root string) bool {
	for _, asset := range p.assets {
		path := filepath.Join(root, presetsDir, p.id, asset.name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || (isFont(path) && !validFontHeader(path)) {
			return false
		}
	}
	return true
}

var downloadClient = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	host := req.URL.Hostname()
	if len(via) >= 8 || req.URL.Scheme != "https" ||
		(host != "github.com" && host != "raw.githubusercontent.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
		return errors.New("字体下载地址无效")
	}
	return nil
}}

// InstallChoice downloads a preset and its licenses directly to the library.
func InstallChoice(ctx context.Context, root, value string) error {
	for _, p := range presets {
		if value == p.value() {
			return installPreset(ctx, downloadClient, root, p)
		}
	}
	return errors.New("未知的预设字体")
}

func installPreset(ctx context.Context, client *http.Client, root string, p preset) error {
	if p.installed(root) {
		return nil
	}
	managed := filepath.Join(root, presetsDir)
	if err := os.MkdirAll(managed, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(managed, ".download-")
	if err != nil {
		return err
	}
	defer func() {
		for _, asset := range p.assets {
			_ = os.Remove(filepath.Join(stage, asset.name))
		}
		_ = os.Remove(stage)
	}()
	for _, asset := range p.assets {
		if err := downloadAsset(ctx, client, asset, filepath.Join(stage, asset.name)); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := filepath.Join(managed, p.id)
	if err := os.Rename(stage, destination); err == nil || p.installed(root) {
		return nil
	}
	// Repair an incomplete installation, publishing its regular face last.
	if info, err := os.Lstat(destination); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("无法写入字体目录 %s", destination)
	}
	for i := len(p.assets) - 1; i >= 0; i-- {
		name := p.assets[i].name
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(destination, name)); err != nil {
			if p.installed(root) {
				return nil
			}
			return err
		}
	}
	return nil
}

func downloadAsset(ctx context.Context, client *http.Client, asset fontAsset, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gon-font-library")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 下载失败（HTTP %d）", asset.name, res.StatusCode)
	}
	limit := int64(1 << 20)
	if isFont(path) {
		limit = 64 << 20
	}
	if res.ContentLength > limit {
		return fmt.Errorf("%s 超出下载大小限制", asset.name)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	size, copyErr := io.Copy(file, io.LimitReader(res.Body, limit+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return err
	}
	if size == 0 || size > limit {
		return fmt.Errorf("%s 的下载大小无效", asset.name)
	}
	if isFont(path) {
		face, err := LoadFace(path)
		if err != nil {
			return fmt.Errorf("无法读取下载的字体 %s：%w", asset.name, err)
		}
		face.close()
	}
	return nil
}
