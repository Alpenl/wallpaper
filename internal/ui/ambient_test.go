package ui

import (
	"image"
	"image/color"
	"testing"
)

func TestBlurSmallCoversAndKeepsFlatColor(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 640, 200)) // 比 16:9 宽，按 cover 裁中间
	for y := 0; y < 200; y++ {
		for x := 0; x < 640; x++ {
			src.Set(x, y, color.NRGBA{40, 120, 200, 255})
		}
	}
	out := blurSmall(src)
	if b := out.Bounds(); b.Dx() != ambientW || b.Dy() != ambientH {
		t.Fatal(b)
	}
	r, g, b, _ := out.At(ambientW/2, ambientH/2).RGBA()
	if r>>8 != 40 || g>>8 != 120 || b>>8 != 200 {
		t.Fatal(r>>8, g>>8, b>>8)
	}
	if out := blurSmall(image.NewNRGBA(image.Rect(0, 0, 0, 0))); out.Bounds().Dx() != 1 {
		t.Fatal("empty image")
	}
}
