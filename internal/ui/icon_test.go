package ui

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIconDrawsRoundedTrayBitmap(t *testing.T) {
	raw := Icon()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Icon() does not return a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("Icon() bounds = %v, want 32x32", b)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("corner alpha = %d, want transparent", a)
	}
	if _, _, _, a := img.At(16, 16).RGBA(); a == 0 {
		t.Fatal("center alpha = 0, want opaque content")
	}
	// The mountain ink sits around y=18; the card must stay light at the center.
	r, g, b, a := img.At(10, 9).RGBA()
	if a == 0 || r < 0x9000 || g < 0x9000 || b < 0x8000 {
		t.Fatalf("card pixel = %04x %04x %04x %04x, want light opaque card", r, g, b, a)
	}
	if r, g, b, _ := img.At(16, 18).RGBA(); b < r || b < g {
		t.Fatalf("mountain pixel = %04x %04x %04x, want blue ink", r, g, b)
	}
}
