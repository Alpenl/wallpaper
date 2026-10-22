package panorama

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func sourceImage() *image.RGBA {
	s := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			if x < 4 {
				s.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				s.Set(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	return s
}
func TestSpanningCropsDifferFromDuplication(t *testing.T) {
	src := sourceImage()
	canvas := image.Rect(-4, -2, 4, 2)
	left, err := Render(src, canvas, image.Rect(-4, -2, 0, 2), "stretch")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Render(src, canvas, image.Rect(0, -2, 4, 2), "stretch")
	if err != nil {
		t.Fatal(err)
	}
	if left.RGBAAt(1, 1) != (color.RGBA{255, 0, 0, 255}) || right.RGBAAt(2, 1) != (color.RGBA{0, 0, 255, 255}) {
		t.Fatal("outputs are not distinct crops")
	}
}
func TestPanoramaMatchesSingleCanvasAcrossSeam(t *testing.T) {
	src := sourceImage()
	canvas := image.Rect(-7, -4, 11, 8)
	for _, fit := range []string{"fill", "fit", "stretch"} {
		whole, err := Render(src, canvas, canvas, fit)
		if err != nil {
			t.Fatal(err)
		}
		for _, viewport := range []image.Rectangle{image.Rect(-7, -4, 1, 8), image.Rect(1, -4, 11, 8)} {
			tile, err := Render(src, canvas, viewport, fit)
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < tile.Bounds().Dy(); y++ {
				for x := 0; x < tile.Bounds().Dx(); x++ {
					want := whole.RGBAAt(x+viewport.Min.X-canvas.Min.X, y+viewport.Min.Y-canvas.Min.Y)
					if got := tile.RGBAAt(x, y); got != want {
						t.Fatalf("%s seam (%d,%d): got %v want %v", fit, x, y, got, want)
					}
				}
			}
		}
	}
}
func TestPanoramaFitLetterboxes(t *testing.T) {
	out, err := Render(sourceImage(), image.Rect(0, 0, 8, 8), image.Rect(0, 0, 8, 8), "fit")
	if err != nil {
		t.Fatal(err)
	}
	if out.RGBAAt(1, 0) != (color.RGBA{0, 0, 0, 255}) || out.RGBAAt(1, 3).R == 0 {
		t.Fatal("fit mapping lacks letterbox or content")
	}
	if _, err := Render(sourceImage(), image.Rect(0, 0, 8, 8), image.Rect(0, 0, 8, 8), "unknown"); err == nil {
		t.Fatal("accepted invalid fit")
	}
}
func TestCropCachesImmutableSourceAndGeometry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, sourceImage()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	canvas := image.Rect(-4, 0, 4, 4)
	left := image.Rect(-4, 0, 0, 4)
	p, err := Crop(path, filepath.Join(dir, "cache"), canvas, left, "stretch")
	if err != nil {
		t.Fatal(err)
	}
	q, err := Crop(path, filepath.Join(dir, "cache"), canvas, left, "stretch")
	if err != nil || p != q {
		t.Fatalf("cache missed %q %q %v", p, q, err)
	}
	q, err = Crop(path, filepath.Join(dir, "cache"), canvas, image.Rect(0, 0, 4, 4), "stretch")
	if err != nil || p == q {
		t.Fatal("wrong viewport reused")
	}
	files, _ := os.ReadDir(filepath.Join(dir, "cache"))
	if len(files) != 2 {
		t.Fatalf("temporary files left: %d", len(files))
	}
}

func TestRetinaCropPreservesBackingPixelDetail(t *testing.T) {
	// The source has one column per backing pixel, twice as many as logical
	// desktop units. A logical-size crop followed by upscaling loses stripes.
	src := image.NewRGBA(image.Rect(3, -2, 19, 6))
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		for x := src.Rect.Min.X; x < src.Rect.Max.X; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x%2 == 1 {
				c = color.RGBA{0, 255, 0, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	canvas := image.Rect(-4, -2, 4, 2)
	viewport := image.Rect(-4, -2, 0, 2)
	out, err := RenderSized(src, canvas, viewport, image.Pt(8, 8), "stretch")
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Size() != image.Pt(8, 8) {
		t.Fatalf("Retina crop has logical dimensions: %v", out.Bounds())
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if got, want := out.RGBAAt(x, y), src.RGBAAt(x+src.Rect.Min.X, y+src.Rect.Min.Y); got != want {
				t.Fatalf("Retina backing detail (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestMixedDensityCropsUseSameLogicalCanvas(t *testing.T) {
	// Nonzero source and desktop origins exercise both sides of the affine
	// translation. Compare each tile with the same source on a full canvas
	// rasterized at that tile's density, not at a common guessed DPI scale.
	src := image.NewRGBA(image.Rect(3, -2, 15, 6))
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		for x := src.Rect.Min.X; x < src.Rect.Max.X; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8((x - 3) * 20), uint8((y + 2) * 30), 100, 255})
		}
	}
	canvas := image.Rect(-8, -4, 12, 8)
	for _, fit := range []string{"fill", "fit", "stretch"} {
		for _, tc := range []struct {
			viewport image.Rectangle
			density  float64
		}{
			{image.Rect(-8, -4, 0, 8), 1},
			{image.Rect(0, -4, 12, 8), 2},
			{image.Rect(0, -4, 12, 8), 1.5},
		} {
			scale := func(p image.Point) image.Point {
				return image.Pt(int(float64(p.X)*tc.density), int(float64(p.Y)*tc.density))
			}
			whole, err := RenderSized(src, canvas, canvas, scale(canvas.Size()), fit)
			if err != nil {
				t.Fatal(err)
			}
			tile, err := RenderSized(src, canvas, tc.viewport, scale(tc.viewport.Size()), fit)
			if err != nil {
				t.Fatal(err)
			}
			offset := scale(tc.viewport.Min.Sub(canvas.Min))
			for y := 0; y < tile.Rect.Dy(); y++ {
				for x := 0; x < tile.Rect.Dx(); x++ {
					got, want := tile.RGBAAt(x, y), whole.RGBAAt(x+offset.X, y+offset.Y)
					if got != want {
						t.Fatalf("%s density %v pixel (%d,%d) = %v, want %v", fit, tc.density, x, y, got, want)
					}
				}
			}
		}
	}
}

func TestSizedCropCacheIncludesBackingDimensions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, sourceImage()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	canvas, viewport := image.Rect(-4, 0, 4, 4), image.Rect(-4, 0, 0, 4)
	cache := filepath.Join(dir, "cache")
	paths := map[string]bool{}
	for _, size := range []image.Point{image.Pt(4, 4), image.Pt(8, 8), image.Pt(8, 4)} {
		p, err := CropSized(path, cache, canvas, viewport, size, "fit")
		if err != nil {
			t.Fatal(err)
		}
		if paths[p] {
			t.Fatal("different backing raster reused cached crop")
		}
		paths[p] = true
		q, err := CropSized(path, cache, canvas, viewport, size, "fit")
		if err != nil || p != q {
			t.Fatalf("same size cache missed: %q %q %v", p, q, err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil || image.Pt(cfg.Width, cfg.Height) != size {
			t.Fatalf("cached crop size = %dx%d, want %v: %v", cfg.Width, cfg.Height, size, err)
		}
	}
	legacy, err := Crop(path, cache, canvas, viewport, "fit")
	if err != nil || !paths[legacy] {
		t.Fatalf("compatibility wrapper did not use logical-sized cache: %s %v", legacy, err)
	}
	files, err := os.ReadDir(cache)
	if err != nil || len(files) != 3 {
		t.Fatalf("cache count = %d, want 3: %v", len(files), err)
	}
}

func TestSizedCropBoundsPhysicalAllocation(t *testing.T) {
	canvas := image.Rect(-16000, -16000, 16000, 16000)
	viewport := image.Rect(0, 0, 4, 4)
	for _, size := range []image.Point{{}, image.Pt(-2, 2), image.Pt(8192, 8192), image.Pt(int(^uint(0)>>1), 2)} {
		if _, err := RenderSized(sourceImage(), canvas, viewport, size, "fill"); err == nil {
			t.Fatalf("accepted invalid or excessive physical crop %v", size)
		}
		if _, err := CropSized("missing source", t.TempDir(), canvas, viewport, size, "fill"); err == nil || os.IsNotExist(err) {
			t.Fatalf("must validate physical size before source access: %v", err)
		}
	}
	// The billion-unit canvas must not be allocated to make this tiny crop.
	if out, err := RenderSized(sourceImage(), canvas, viewport, image.Pt(8, 8), "fill"); err != nil || out.Bounds().Size() != image.Pt(8, 8) {
		t.Fatalf("bounded output on large virtual canvas = %v, %v", out, err)
	}
}
