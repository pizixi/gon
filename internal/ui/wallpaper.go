package ui

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"golang.org/x/image/draw"
)

var wallpaperBlurStops = [...]float32{0, 8, 24, 48, 72, 96}

const wallpaperBlurDuration = 230 * time.Millisecond

type wallpaper struct {
	path   string
	op     paint.ImageOp
	source *image.RGBA
	levels [len(wallpaperBlurStops)]paint.ImageOp
	err    error

	blurFrom, blurTo float32
	blurStart        time.Time
}

// load keeps only a display texture and a small source for the blur previews.
func (w *wallpaper) load(path string) error {
	if w.path == path {
		return w.err
	}
	*w = wallpaper{path: path}
	f, err := os.Open(path)
	if err != nil {
		w.err = err
		return err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		w.err = err
		return err
	}
	w.source = downsampleWallpaper(src, 640)
	if size := src.Bounds().Size(); max(size.X, size.Y) > 1920 {
		src = downsampleWallpaper(src, 1920)
	}
	w.op = paint.NewImageOp(src)
	return nil
}

func (w *wallpaper) blurLevel(index int) paint.ImageOp {
	if index == 0 {
		return w.op
	}
	if w.levels[index].Size().X == 0 {
		img := image.NewRGBA(w.source.Bounds())
		copy(img.Pix, w.source.Pix)
		radius := max(1, int(wallpaperBlurStops[index])/5)
		boxBlur(img, radius)
		boxBlur(img, radius)
		w.levels[index] = paint.NewImageOp(img)
	}
	return w.levels[index]
}

func (w *wallpaper) blurAt(now time.Time, target float32) float32 {
	target = max(0, min(96, target))
	if w.blurStart.IsZero() {
		w.blurStart = now
		w.blurFrom, w.blurTo = target, target
	}
	t := max(0, min(1, float32(now.Sub(w.blurStart))/float32(wallpaperBlurDuration)))
	eased := t * (2 - t)
	value := w.blurFrom + (w.blurTo-w.blurFrom)*eased
	if target != w.blurTo {
		w.blurFrom, w.blurTo, w.blurStart = value, target, now
	}
	return value
}

func downsampleWallpaper(src image.Image, limit int) *image.RGBA {
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	w, h := b.Dx(), b.Dy()
	if longest := max(w, h); longest > limit {
		w = max(1, int(int64(w)*int64(limit)/int64(longest)))
		h = max(1, int(int64(h)*int64(limit)/int64(longest)))
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// boxBlur applies a separable box blur in place.
func boxBlur(img *image.RGBA, radius int) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if radius <= 0 || w < 2 || h < 2 {
		return
	}
	tmp := make([]uint8, len(img.Pix))
	// Horizontal pass.
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for c := 0; c < 3; c++ {
			var sum int
			// Initialise the window with clamped edges.
			for i := -radius; i <= radius; i++ {
				sum += int(row[clampIdx(i, w)*4+c])
			}
			for x := 0; x < w; x++ {
				tmp[y*img.Stride+x*4+c] = uint8(sum / (radius*2 + 1))
				sum -= int(row[clampIdx(x-radius, w)*4+c])
				sum += int(row[clampIdx(x+radius+1, w)*4+c])
			}
		}
		for x := 0; x < w; x++ {
			tmp[y*img.Stride+x*4+3] = 0xff
		}
	}
	// Vertical pass.
	for x := 0; x < w; x++ {
		for c := 0; c < 3; c++ {
			var sum int
			for i := -radius; i <= radius; i++ {
				sum += int(tmp[clampIdx(i, h)*img.Stride+x*4+c])
			}
			for y := 0; y < h; y++ {
				img.Pix[y*img.Stride+x*4+c] = uint8(sum / (radius*2 + 1))
				sum -= int(tmp[clampIdx(y-radius, h)*img.Stride+x*4+c])
				sum += int(tmp[clampIdx(y+radius+1, h)*img.Stride+x*4+c])
			}
		}
	}
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// Layout draws the cover image and animates between cached blur levels.
func (w *wallpaper) Layout(gtx layout.Context, size image.Point, target float32) {
	now := gtx.Now
	if now.IsZero() {
		now = time.Now()
	}
	blur := w.blurAt(now, target)
	if blur != w.blurTo {
		gtx.Execute(op.InvalidateCmd{At: now.Add(time.Second / 60)})
	}
	for hi := 1; hi < len(wallpaperBlurStops); hi++ {
		if blur <= wallpaperBlurStops[hi] {
			lo := hi - 1
			alpha := (blur - wallpaperBlurStops[lo]) / (wallpaperBlurStops[hi] - wallpaperBlurStops[lo])
			if alpha < 1 {
				drawWallpaperCover(gtx, w.blurLevel(lo), size)
			}
			if alpha > 0 {
				upper := paint.PushOpacity(gtx.Ops, alpha)
				drawWallpaperCover(gtx, w.blurLevel(hi), size)
				upper.Pop()
			}
			return
		}
	}
}

func drawWallpaperCover(gtx layout.Context, tex paint.ImageOp, size image.Point) {
	src := tex.Size()
	if src.X <= 0 || src.Y <= 0 || size.X <= 0 || size.Y <= 0 {
		return
	}
	scale := max(float32(size.X)/float32(src.X), float32(size.Y)/float32(src.Y))
	pos := f32.Pt((float32(size.X)-float32(src.X)*scale)/2, (float32(size.Y)-float32(src.Y)*scale)/2)
	stack := op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(scale, scale)).Offset(pos)).Push(gtx.Ops)
	tex.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	stack.Pop()
}

// expandPath resolves ~ and makes a path absolute.
func expandPath(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
