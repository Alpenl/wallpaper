package desktop

import (
	"image"
	"testing"
)

func TestSortByLayoutAndDedupe(t *testing.T) {
	ds := []Display{
		{ID: "u", Bounds: image.Rect(1920, 0, 3840, 1080)},
		{ID: "u", Bounds: image.Rect(0, 0, 1920, 1080)},
		{ID: "below", Bounds: image.Rect(0, 1080, 1920, 2160)},
		{ID: "left", Bounds: image.Rect(-2560, 0, 0, 1440)},
	}
	SortByLayout(ds)
	Dedupe(ds)
	got := []DisplayID{ds[0].ID, ds[1].ID, ds[2].ID, ds[3].ID}
	want := []DisplayID{"left", "u", "below", "u#2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
	if ds[1].Bounds.Min.X != 0 || ds[3].Bounds.Min.X != 1920 {
		t.Fatal("#2 应给右边那块")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{" DELL U2720Q ": "DELL U2720Q", "Generic PnP Monitor": "", "通用即插即用监视器": "", "": ""} {
		if g := CleanName(in); g != want {
			t.Errorf("%q → %q", in, g)
		}
	}
}

func TestPixelSizeSeparatesRasterFromDesktopAndDPIScale(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Display
		want image.Point
	}{
		{"Retina", Display{Bounds: image.Rect(-1512, 0, 0, 982), Pixels: image.Pt(3024, 1964), Scale: 2}, image.Pt(3024, 1964)},
		{"scaled Retina mode", Display{Bounds: image.Rect(0, 0, 1800, 1169), Pixels: image.Pt(3600, 2338), Scale: 2}, image.Pt(3600, 2338)},
		{"Windows DPI is not raster scale", Display{Bounds: image.Rect(0, 0, 3840, 2160), Scale: 1.5}, image.Pt(3840, 2160)},
		{"X11", Display{Bounds: image.Rect(1920, 0, 3840, 1080), Scale: 1}, image.Pt(1920, 1080)},
		{"partial pixel size falls back together", Display{Bounds: image.Rect(0, 0, 1920, 1080), Pixels: image.Pt(3840, 0)}, image.Pt(1920, 1080)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.PixelSize(); got != tc.want {
				t.Fatalf("PixelSize() = %v, want %v", got, tc.want)
			}
		})
	}
}
