package importer

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"wpapp/internal/config"
)

func TestPreviewColors(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			c := color.NRGBA{0, 0, 255, 255}
			if x >= 48 {
				c = color.NRGBA{255, 0, 0, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	path := filepath.Join(t.TempDir(), "preview.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	want := []string{"blue", "蓝色", "red", "红色"}
	if got := PreviewColors(path); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := PreviewColors(path + ".missing"); got != nil {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := PreviewColors(path); got != nil {
		t.Fatal(got)
	}
}
func TestColorClassification(t *testing.T) {
	for _, tc := range []struct {
		c    color.NRGBA
		name string
	}{
		{color.NRGBA{0, 0, 0, 255}, "black"}, {color.NRGBA{255, 255, 255, 255}, "white"},
		{color.NRGBA{128, 128, 128, 255}, "gray"}, {color.NRGBA{0, 255, 0, 255}, "green"},
		{color.NRGBA{255, 255, 0, 255}, "yellow"}, {color.NRGBA{0, 255, 255, 255}, "cyan"},
	} {
		if got := colorNames[colorBucket(tc.c)][0]; got != tc.name {
			t.Fatalf("%v: got %s want %s", tc.c, got, tc.name)
		}
	}
	if got := imageColors(image.NewNRGBA(image.Rect(10, 20, 20, 30))); got != nil {
		t.Fatal("transparent image must have no color", got)
	}
}
func TestOversizedPreviewSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "oversized.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((16 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := PreviewColors(p); got != nil {
		t.Fatal(got)
	}
}

func TestReimportReplacesImagePreviewAndColors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.png")
	writeImage := func(c color.NRGBA) {
		t.Helper()
		img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		for y := 0; y < 2; y++ {
			for x := 0; x < 2; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
		f, err := os.Create(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		c    color.NRGBA
		want []string
	}{
		{color.NRGBA{255, 0, 0, 255}, []string{"red", "红色"}},
		{color.NRGBA{0, 0, 255, 255}, []string{"blue", "蓝色"}},
	} {
		writeImage(tc.c)
		it := config.Item{ID: "same-id", Type: "image"}
		res := Result{}
		snapshotItem(Options{Cache: filepath.Join(dir, "cache")}, &it, src, &res)
		if it.Snapshot == "" || len(res.Warnings) != 0 {
			t.Fatalf("snapshot=%q warnings=%v", it.Snapshot, res.Warnings)
		}
		if !reflect.DeepEqual(it.Colors, tc.want) {
			t.Fatalf("colors=%v want=%v", it.Colors, tc.want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "cache", "same-id"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "first.png" {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestPreviewReplacementFailureKeepsPreviousBytes(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "first.png")
	if err := os.WriteFile(dst, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replacePreview(filepath.Join(dir, "missing.png"), dst); err == nil {
		t.Fatal("expected missing-source error")
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "previous" {
		t.Fatalf("previous cache lost: %q, %v", got, err)
	}
}
