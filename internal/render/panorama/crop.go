// Package panorama prepares per-output system-wallpaper crops without allocating
// a full virtual-desktop bitmap. Video uses native clipped canvas views instead.
package panorama

import (
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	_ "golang.org/x/image/webp"
	"wpapp/internal/desktop"
)

// Bound peak decoder memory across displays; cache hits do not take this slot.
var decodeSlot = make(chan struct{}, 1)

// Crop is the one-pixel-per-desktop-unit compatibility wrapper for CropSized.
func Crop(source, cache string, canvas, viewport image.Rectangle, fit string) (string, error) {
	return CropSized(source, cache, canvas, viewport, viewport.Size(), fit)
}

// CropSized caches the viewport's part of one fitted virtual canvas at output
// backing-pixel resolution. Canvas and viewport remain in the host's desktop
// coordinates, which are logical points on macOS. Source dimensions and output
// allocation are bounded before decoding; empty desktop gaps remain real gaps.
func CropSized(source, cache string, canvas, viewport image.Rectangle, output image.Point, fit string) (string, error) {
	if err := validateOutput(canvas, viewport, output); err != nil {
		return "", err
	}
	f, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("sized-v1|%s|%d|%d|%v|%v|%v|%s", source, st.Size(), st.ModTime().UnixNano(), canvas, viewport, output, fit)))
	dst := filepath.Join(cache, fmt.Sprintf("span-%x.png", sum[:12]))
	if stat, err := os.Stat(dst); err == nil && stat.Size() > 0 {
		return dst, nil
	}
	decodeSlot <- struct{}{}
	defer func() { <-decodeSlot }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", err
	}
	if !boundedPixels(image.Pt(cfg.Width, cfg.Height), 64*1024*1024) {
		return "", fmt.Errorf("panorama: source exceeds 64 megapixels")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}
	out, err := RenderSized(src, canvas, viewport, output, fit)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(cache, 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(cache, ".span-*.png")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err = png.Encode(tmp, out); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		// A mirrored output may have generated the same cache entry concurrently.
		if stat, e := os.Stat(dst); e != nil || stat.Size() == 0 {
			return "", err
		}
	}
	return dst, nil
}

// Render is the one-pixel-per-desktop-unit compatibility wrapper for RenderSized.
func Render(src image.Image, canvas, viewport image.Rectangle, fit string) (*image.RGBA, error) {
	return RenderSized(src, canvas, viewport, viewport.Size(), fit)
}

// RenderSized maps the source once into the logical canvas, then clips and
// samples that viewport at output backing-pixel resolution. It allocates only
// the physical output, never the potentially much larger full desktop bitmap.
func RenderSized(src image.Image, canvas, viewport image.Rectangle, output image.Point, fit string) (*image.RGBA, error) {
	if src == nil || src.Bounds().Empty() {
		return nil, fmt.Errorf("panorama: empty source")
	}
	if err := validateOutput(canvas, viewport, output); err != nil {
		return nil, err
	}
	cw, ch := float64(canvas.Dx()), float64(canvas.Dy())
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	w, h := cw, ch
	switch fit {
	case "fill", "":
		scale := math.Max(cw/sw, ch/sh)
		w, h = sw*scale, sh*scale
	case "fit":
		scale := math.Min(cw/sw, ch/sh)
		w, h = sw*scale, sh*scale
	case "stretch":
	default:
		return nil, fmt.Errorf("panorama: invalid fit %q", fit)
	}
	// Round the shared canvas destination before translating into each viewport,
	// ensuring adjacent outputs use exactly the same sampling transform.
	left, top := canvas.Min.X+int(math.Round((cw-w)/2)), canvas.Min.Y+int(math.Round((ch-h)/2))
	target := image.Rect(left, top, left+int(math.Round(w)), top+int(math.Round(h))).Sub(viewport.Min)
	out := image.NewRGBA(image.Rectangle{Max: output})
	draw.Draw(out, out.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if target.Empty() {
		return out, nil
	}
	// ApproxBiLinear clips before scaling and does not allocate a kernel table
	// or intermediate bitmap for the potentially much larger logical canvas.
	if output == viewport.Size() {
		xdraw.ApproxBiLinear.Scale(out, target, src, src.Bounds(), draw.Over, nil)
	} else {
		// Use an affine transform rather than rounding each output's scaled
		// rectangle. Fractional/mixed densities must preserve the same logical
		// source mapping, including negative origins and letterbox offsets.
		sx, sy := float64(output.X)/float64(viewport.Dx()), float64(output.Y)/float64(viewport.Dy())
		ax, ay := float64(target.Dx())/sw*sx, float64(target.Dy())/sh*sy
		tx := float64(target.Min.X)*sx - float64(src.Bounds().Min.X)*ax
		ty := float64(target.Min.Y)*sy - float64(src.Bounds().Min.Y)*ay
		if ax == 1 && ay == 1 && tx == math.Trunc(tx) && ty == math.Trunc(ty) {
			// Copy exact backing pixels, including subimages with unequal X/Y
			// origins; no interpolator or temporary raster is needed.
			draw.Draw(out, src.Bounds().Add(image.Pt(int(tx), int(ty))), src, src.Bounds().Min, draw.Over)
		} else {
			xdraw.ApproxBiLinear.Transform(out, f64.Aff3{ax, 0, tx, 0, ay, ty}, src, src.Bounds(), draw.Over, nil)
		}
	}
	return out, nil
}

func validateOutput(canvas, viewport image.Rectangle, output image.Point) error {
	if _, _, _, _, err := desktop.CanvasFrame(canvas, viewport, float64(output.X), float64(output.Y), true); err != nil {
		return err
	}
	if !boundedPixels(output, 32*1024*1024) {
		return fmt.Errorf("panorama: display crop exceeds 32 megapixels")
	}
	return nil
}

// Division bounds dimensions without overflowing a product from untrusted image
// headers or requested raster sizes.
func boundedPixels(size image.Point, limit uint64) bool {
	return size.X > 0 && size.Y > 0 && uint64(size.X) <= limit/uint64(size.Y)
}
