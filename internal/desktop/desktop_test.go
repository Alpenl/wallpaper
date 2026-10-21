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
