package mf

import (
	"math"
	"testing"
	"unsafe"
)

func TestFillSourceRect(t *testing.T) {
	cases := []struct {
		name                               string
		aspectW, aspectH, outputW, outputH uint32
		want                               normalizedRect
	}{
		{"same aspect", 16, 9, 1920, 1080, normalizedRect{0, 0, 1, 1}},
		{"wide panorama", 16, 9, 3840, 1080, normalizedRect{0, .25, 1, .75}},
		{"wide source", 32, 9, 1920, 1080, normalizedRect{.25, 0, .75, 1}},
		{"portrait source", 9, 16, 1920, 1080, normalizedRect{0, .341796875, 1, .658203125}},
		{"portrait output", 16, 9, 1080, 1920, normalizedRect{.341796875, 0, .658203125, 1}},
		{"non-square pixels use picture aspect", 4, 3, 1600, 900, normalizedRect{0, .125, 1, .875}},
		{"uint32 multiplication stays exact", math.MaxUint32, math.MaxUint32, math.MaxUint32, math.MaxUint32, normalizedRect{0, 0, 1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := fillSourceRect(c.aspectW, c.aspectH, c.outputW, c.outputH)
			if !ok || got != c.want {
				t.Fatalf("fill crop = %+v, %v; want %+v", got, ok, c.want)
			}
			cropAspect := float64(c.aspectW) / float64(c.aspectH) * float64(got.Right-got.Left) / float64(got.Bottom-got.Top)
			wantAspect := float64(c.outputW) / float64(c.outputH)
			if math.Abs(cropAspect/wantAspect-1) > 1e-6 {
				t.Fatalf("selected source would letterbox: aspect=%g output=%g", cropAspect, wantAspect)
			}
		})
	}
}

func TestFillSourceRectRejectsInvalidDimensions(t *testing.T) {
	for _, c := range [][4]uint32{{0, 9, 1920, 1080}, {16, 0, 1920, 1080}, {16, 9, 0, 1080}, {16, 9, 1920, 0}, {1, math.MaxUint32, math.MaxUint32, 1}} {
		if r, ok := fillSourceRect(c[0], c[1], c[2], c[3]); ok {
			t.Fatalf("invalid geometry %v produced %+v", c, r)
		}
	}
}

func TestNormalizedRectMatchesMediaFoundationABI(t *testing.T) {
	var r normalizedRect
	if unsafe.Sizeof(r) != 16 || unsafe.Offsetof(r.Left) != 0 || unsafe.Offsetof(r.Top) != 4 || unsafe.Offsetof(r.Right) != 8 || unsafe.Offsetof(r.Bottom) != 12 {
		t.Fatal("MFVideoNormalizedRect must contain four consecutive float32 coordinates")
	}
}

func TestFrameGeometry(t *testing.T) {
	full := normalizedRect{Right: 1, Bottom: 1}
	for _, c := range []struct {
		name, mode     string
		aw, ah, cw, ch uint32
		want           videoGeometry
	}{
		{"fill crops panorama", "fill", 16, 9, 3840, 1080, videoGeometry{normalizedRect{0, .25, 1, .75}, 3840, 1080}},
		{"fit letterboxes panorama", "fit", 16, 9, 3840, 1080, videoGeometry{full, 3840, 1080}},
		{"stretch retains whole picture", "stretch", 16, 9, 3840, 1080, videoGeometry{full, 1920, 1080}},
		{"stretch tall source", "stretch", 9, 16, 1920, 1080, videoGeometry{full, 603, 1072}},
		{"stretch square", "stretch", 1, 1, 1920, 1080, videoGeometry{full, 1080, 1080}},
		{"non-square pixels", "stretch", 4, 3, 1920, 1080, videoGeometry{full, 1440, 1080}},
		{"reduced native aspect", "stretch", 3840, 2160, 1920, 1080, videoGeometry{full, 1920, 1080}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := frameGeometry(c.mode, c.aw, c.ah, c.cw, c.ch)
			if !ok || got != c.want {
				t.Fatalf("got %+v %v, want %+v", got, ok, c.want)
			}
			if c.mode == "stretch" {
				if uint64(got.width)*uint64(c.ah) != uint64(got.height)*uint64(c.aw) {
					t.Fatal("backbuffer would letterbox instead of stretching")
				}
				if got.width > c.cw || got.height > c.ch {
					t.Fatal("backbuffer unexpectedly larger than canvas")
				}
			}
		})
	}
}

func TestFrameGeometryRejectsInvalidAndRoundsUnusualAspect(t *testing.T) {
	for _, mode := range []string{"fill", "fit", "stretch"} {
		for _, dims := range [][4]uint32{{0, 9, 1920, 1080}, {16, 0, 1920, 1080}, {16, 9, 0, 1080}, {16, 9, 1920, 0}, {16, 9, math.MaxUint32, 1}} {
			if _, ok := frameGeometry(mode, dims[0], dims[1], dims[2], dims[3]); ok {
				t.Fatalf("accepted %s %v", mode, dims)
			}
		}
	}
	if _, ok := frameGeometry("fake", 16, 9, 1920, 1080); ok {
		t.Fatal("accepted invalid mode")
	}
	if _, ok := frameGeometry("stretch", 1, math.MaxUint32, 1920, 1080); ok {
		t.Fatal("accepted collapsed backbuffer")
	}
	g, ok := frameGeometry("stretch", 100001, 100000, 1920, 1080)
	if !ok || g.width != 1080 || g.height != 1080 {
		t.Fatalf("unusual ratio not safely rounded: %+v, %v", g, ok)
	}
}
