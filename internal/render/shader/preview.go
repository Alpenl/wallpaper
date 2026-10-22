package shader

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"wpapp/internal/render"
	"wpapp/internal/wpk"
)

// Preview executes untrusted shader code only in a bounded renderer child.
func Preview(exe, dir, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-helper", "shader-preview", "-in", dir, "-out", dst)
	b, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("shader preview: %w: %s", e, b)
	}
	return nil
}
func PreviewMain(dir, dst string) error {
	m, e := wpk.LoadDir(dir)
	if e != nil {
		return e
	}
	x := loadRequest{Item: render.Item{ID: m.ID, Kind: "shader", PackageDir: dir, Path: filepath.Join(dir, filepath.FromSlash(m.Entry))}, Bounds: image.Rect(0, 0, 320, 180), Scale: 1, Preview: true}
	return platformPreview(x, dst)
}
func renderPreview(g gpu, x loadRequest, dst string) error {
	defer g.Close()
	m, source, e := readSource(x)
	if e != nil {
		return e
	}
	target := MSL
	if nativeTarget() == HLSL {
		target = HLSL
	}
	code, e := TranslateProperties(source, target, m.Properties)
	if e != nil {
		return e
	}
	if e = g.Load(x, code); e != nil {
		return e
	}
	var u Uniforms
	if e = u.Properties(m.Properties, ""); e != nil {
		return e
	}
	u.Frame(320, 180, 0, 0, 0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, e = g.Frame(&u, 1); e != nil {
		return e
	}
	s, ok := g.(interface{ Snapshot(string) error })
	if !ok {
		return render.ErrUnsupported
	}
	return s.Snapshot(dst)
}
func writePreview(path string, pixels []byte, w, h, stride int, bgra bool) error {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(im.Pix[y*im.Stride:y*im.Stride+w*4], pixels[y*stride:y*stride+w*4])
	}
	if bgra {
		for i := 0; i < len(im.Pix); i += 4 {
			im.Pix[i], im.Pix[i+2] = im.Pix[i+2], im.Pix[i]
		}
	}
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	e = png.Encode(f, im)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
