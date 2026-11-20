// Package fontset loads monospace faces from the system font directory and
// provides glyph lookup with style and CJK fallback.
package fontset

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/mattn/go-runewidth"
)

// Family is a monospace family with optional style faces and fallbacks.
type Family struct {
	Name     string
	Reg      *Face
	variants [3]lazyFace
	falls    []lazyFace

	mu    sync.Mutex
	cache map[cacheKey]*Face
}

type lazyFace struct {
	once sync.Once
	load func() *Face
	face *Face
}

func (f *lazyFace) get() *Face {
	f.once.Do(func() { f.face = f.load() })
	return f.face
}

// Close releases the loaded faces. Unused style and fallback faces stay unloaded.
func (f *Family) Close() {
	if f == nil {
		return
	}
	f.Reg.close()
	for i := range f.variants {
		f.variants[i].face.close()
	}
	for i := range f.falls {
		f.falls[i].face.close()
	}
}

type cacheKey struct {
	r    rune
	b, i bool
}

// Nominal is the primary face used for metrics.
func (f *Family) Nominal() *Face { return f.Reg }

// Pick returns the face that should render r with the requested style.
func (f *Family) Pick(r rune, bold, italic bool) *Face {
	f.mu.Lock()
	k := cacheKey{r: r, b: bold, i: italic}
	if v, ok := f.cache[k]; ok {
		f.mu.Unlock()
		return v
	}
	f.mu.Unlock()

	face := pickFaceOf(f, bold, italic, r)
	if face == nil {
		// Fall back to a face that actually has the glyph (CJK, symbols).
		for i := range f.falls {
			fl := f.falls[i].get()
			if fl == nil {
				continue
			}
			if _, ok := fl.NominalGlyph(r); ok {
				face = fl
				break
			}
		}
	}
	if face == nil {
		face = f.Reg
	}
	f.mu.Lock()
	if len(f.cache) >= 4096 {
		clear(f.cache)
	}
	f.cache[k] = face
	f.mu.Unlock()
	return face
}

// ShapeCluster chooses a face that can render the entire grapheme.
func (f *Family) ShapeCluster(text string, bold, italic bool) (*Face, Cluster) {
	r, _ := utf8.DecodeRuneInString(text)
	base := f.Pick(r, bold, italic)
	if cluster := base.ShapeCluster(text); len(cluster.Glyphs) != 0 {
		return base, cluster
	}
	for i := range f.falls {
		face := f.falls[i].get()
		if face == nil || face == base {
			continue
		}
		if cluster := face.ShapeCluster(text); len(cluster.Glyphs) != 0 {
			return face, cluster
		}
	}
	return base, Cluster{}
}

func pickFaceOf(f *Family, bold, italic bool, r rune) *Face {
	switch {
	case bold && italic:
		for _, i := range [...]int{2, 0, 1} {
			if face := f.styledFace(i, r); face != nil {
				return face
			}
		}
	case bold:
		if face := f.styledFace(0, r); face != nil {
			return face
		}
	case italic:
		if face := f.styledFace(1, r); face != nil {
			return face
		}
	}
	if _, ok := f.Reg.NominalGlyph(r); ok {
		return f.Reg
	}
	return nil
}

func (f *Family) styledFace(i int, r rune) *Face {
	variant := &f.variants[i]
	if variant.load != nil {
		face := variant.get()
		if _, ok := face.NominalGlyph(r); ok {
			return face
		}
	}
	return nil
}

// IsWide reports whether r occupies two cells.
func IsWide(r rune) bool { return runewidth.RuneWidth(r) == 2 }

// systemFontDirs lists directories scanned for fonts.
func systemFontDirs() []string {
	var dirs []string
	if windir := os.Getenv("WINDIR"); windir != "" {
		dirs = append(dirs, filepath.Join(windir, "Fonts"))
	} else {
		dirs = append(dirs, `C:\Windows\Fonts`)
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		dirs = append(dirs, filepath.Join(la, "Microsoft", "Windows", "Fonts"))
	}
	return dirs
}

var (
	indexOnce sync.Once
	indexMap  map[string]string
)

func buildIndex() {
	indexMap = make(map[string]string, 512)
	for _, dir := range systemFontDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if !strings.HasSuffix(name, ".ttf") && !strings.HasSuffix(name, ".otf") && !strings.HasSuffix(name, ".ttc") {
				continue
			}
			if _, ok := indexMap[name]; !ok {
				indexMap[name] = filepath.Join(dir, e.Name())
			}
		}
	}
}

// FontPath resolves a font file name or family name to an absolute path.
func FontPath(name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			return name
		}
		return ""
	}
	indexOnce.Do(buildIndex)
	base := strings.ToLower(filepath.Base(name))
	if p, ok := indexMap[base]; ok {
		return p
	}
	for _, cand := range familyCandidates(base) {
		if p, ok := indexMap[cand]; ok {
			return p
		}
	}
	return ""
}

func familyCandidates(fam string) []string {
	fam = strings.TrimSuffix(fam, filepath.Ext(fam))
	compact := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(fam)
	return []string{
		compact + ".ttf",
		compact + ".otf",
		compact + ".ttc",
		compact + "regular.ttf",
		compact + "mono.ttf",
	}
}

type installedFace struct {
	id    font.FontID
	score float64
}

// Build a compact family-name index without loading glyphs or Unicode coverage.
var installedFaces = sync.OnceValue(func() map[string]installedFace {
	indexOnce.Do(buildIndex)
	names := make([]string, 0, len(indexMap))
	for name := range indexMap {
		names = append(names, name)
	}
	sort.Strings(names)
	faces := make(map[string]installedFace)
	var buffer []byte
	for _, name := range names {
		path := indexMap[name]
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		if loaders, err := opentype.NewLoaders(file); err == nil {
			for i, loader := range loaders {
				var desc font.Description
				desc, buffer = font.Describe(loader, buffer)
				family := font.NormalizeFamily(desc.Family)
				if family == "" {
					continue
				}
				score := math.Abs(float64(desc.Aspect.Weight-font.WeightNormal)) +
					100*math.Abs(float64(desc.Aspect.Stretch-font.StretchNormal))
				if desc.Aspect.Style != font.StyleNormal {
					score += 1000
				}
				if previous, ok := faces[family]; !ok || score < previous.score {
					faces[family] = installedFace{id: font.FontID{File: path, Index: uint16(i)}, score: score}
				}
			}
		}
		file.Close()
	}
	return faces
})

func namedFace(name string) font.FontID {
	if path := FontPath(name); path != "" {
		return font.FontID{File: path}
	}
	if name == "" || strings.ContainsAny(name, `/\`) {
		return font.FontID{}
	}
	// GDI may return a localized family name from the system font dialog.
	switch name {
	case "微软雅黑":
		name = "Microsoft YaHei"
	case "微软雅黑 UI":
		name = "Microsoft YaHei UI"
	case "宋体":
		name = "SimSun"
	case "新宋体":
		name = "NSimSun"
	case "黑体":
		name = "SimHei"
	case "等线":
		name = "DengXian"
	case "仿宋":
		name = "FangSong"
	case "楷体":
		name = "KaiTi"
	}
	return installedFaces()[font.NormalizeFamily(name)].id
}

// OpenFamily resolves a selected font without falling back to another family.
func OpenFamily(name string, cjk []string) (*Family, error) {
	id := namedFace(name)
	if id.File == "" {
		return nil, fmt.Errorf("找不到字体 %s", name)
	}
	reg, err := loadFace(id)
	if err != nil {
		return nil, err
	}
	return completeFamily(name, cjk, reg), nil
}

// LoadFamily loads the named monospace family (or the first available
// built-in monospace font) plus CJK fallbacks and style variants.
func LoadFamily(name string, cjk []string) *Family {
	if fam, err := OpenFamily(name, cjk); err == nil {
		return fam
	}
	for _, cand := range []string{"cascadiamono.ttf", "consola.ttf", "lucon.ttf", "cour.ttf", "segoeui.ttf"} {
		if p := FontPath(cand); p != "" {
			if reg, err := LoadFace(p); err == nil {
				return completeFamily(name, cjk, reg)
			}
		}
	}

	// Last resort: any font we can parse.
	names := make([]string, 0, len(indexMap))
	for n := range indexMap {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if reg, err := LoadFace(indexMap[n]); err == nil {
			return completeFamily(name, cjk, reg)
		}
	}
	return completeFamily(name, cjk, nil)
}

func completeFamily(name string, cjk []string, reg *Face) *Family {
	fam := &Family{Name: name, Reg: reg, cache: make(map[cacheKey]*Face, 512)}
	if reg == nil {
		return fam
	}
	// Style variants live next to the regular face under similar names.
	for i, kind := range [...]string{"b", "i", "z"} {
		fam.variants[i].load = func() *Face { return loadSibling(reg, kind) }
	}

	var falls []string
	for _, n := range cjk {
		if p := FontPath(n); p != "" {
			falls = append(falls, p)
		}
	}
	for _, n := range []string{"msyh.ttc", "simhei.ttf", "simsun.ttc", "deng.ttf", "seguisym.ttf", "seguiemj.ttf"} {
		if p := FontPath(n); p != "" {
			falls = append(falls, p)
		}
	}
	paths := dedupe(falls)
	fam.falls = make([]lazyFace, 0, len(paths))
	for _, p := range paths {
		if p == fam.Reg.Path && fam.Reg.Index == 0 {
			continue
		}
		fam.falls = append(fam.falls, lazyFace{load: func() *Face {
			face, _ := LoadFace(p)
			return face
		}})
	}
	return fam
}

// loadSibling finds a bold ("b"), italic ("i") or bold-italic ("z", "bi")
// variant of path in the same directory.
func loadSibling(reg *Face, kind string) *Face {
	dir := filepath.Dir(reg.Path)
	ext := filepath.Ext(reg.Path)
	base := strings.TrimSuffix(filepath.Base(reg.Path), ext)
	if strings.HasSuffix(strings.ToLower(base), "-regular") {
		base = base[:len(base)-len("-regular")]
	}

	var suffixes []string
	switch kind {
	case "b":
		suffixes = []string{"-Bold", "b", "bd", " bold"}
	case "i":
		suffixes = []string{"-Italic", "-It", "i", " it"}
	case "z":
		suffixes = []string{"-BoldItalic", "-BoldIt", "z", "bi"}
	}
	for _, suf := range suffixes {
		p := filepath.Join(dir, base+suf+ext)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if f, err := LoadFace(p); err == nil {
				return f
			}
		}
	}
	return nil
}

func dedupe(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
