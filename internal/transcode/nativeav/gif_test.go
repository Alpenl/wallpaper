package nativeav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

var testPalette = color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}, color.Transparent}

func pal(r image.Rectangle, c byte) *image.Paletted {
	p := image.NewPaletted(r, testPalette[:4])
	for i := range p.Pix {
		p.Pix[i] = c
	}
	return p
}
func fixture(images []*image.Paletted, delays []int, disposal []byte) *gif.GIF {
	return &gif.GIF{Image: images, Delay: delays, Disposal: disposal, Config: image.Config{ColorModel: testPalette, Width: 4, Height: 4}, BackgroundIndex: 0}
}
func assertPixel(t *testing.T, im *image.RGBA, x, y int, c color.Color) {
	t.Helper()
	want := color.RGBAModel.Convert(c).(color.RGBA)
	if got := im.RGBAAt(x, y); got != want {
		t.Fatalf("(%d,%d)=%v, want %v", x, y, got, want)
	}
}

func TestGIFVariableDelaysAndSubframeSampling(t *testing.T) {
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), pal(image.Rect(0, 0, 4, 4), 2), pal(image.Rect(0, 0, 4, 4), 3)}, []int{1, 2, 7}, nil)
	s, e := newGIFFrames(g)
	if e != nil {
		t.Fatal(e)
	}
	im, e := s.at(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, testPalette[1])
	// Both sub-33 ms frames have elapsed at t=1/30; no drift or rounding to ms.
	im, e = s.at(context.Background(), 1)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, testPalette[3])
	if n, e := frameCount(s.duration(), 100); e != nil || n != 3 {
		t.Fatalf("frame count=%d %v", n, e)
	}
}
func TestGIFDisposalBackground(t *testing.T) {
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), pal(image.Rect(0, 0, 2, 4), 2), pal(image.Rect(0, 0, 1, 1), 3)}, []int{10, 10, 10}, []byte{1, 2, 1})
	s, _ := newGIFFrames(g)
	im, e := s.at(context.Background(), 3)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 3, testPalette[2])
	assertPixel(t, im, 3, 3, testPalette[1])
	im, e = s.at(context.Background(), 6)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, testPalette[3])
	assertPixel(t, im, 0, 3, testPalette[0])
	assertPixel(t, im, 3, 3, testPalette[1])
}
func TestGIFDisposalPrevious(t *testing.T) {
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), pal(image.Rect(0, 0, 2, 4), 2), pal(image.Rect(0, 0, 1, 1), 3)}, []int{10, 10, 10}, []byte{1, 3, 1})
	s, _ := newGIFFrames(g)
	im, e := s.at(context.Background(), 6)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, testPalette[3])
	assertPixel(t, im, 0, 3, testPalette[1])
	assertPixel(t, im, 3, 3, testPalette[1])
}
func TestGIFTransparencyAndZeroDelay(t *testing.T) {
	transparent := image.NewPaletted(image.Rect(0, 0, 4, 4), testPalette)
	for i := range transparent.Pix {
		transparent.Pix[i] = 4
	}
	transparent.SetColorIndex(1, 1, 2)
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), transparent}, []int{0, 5}, nil)
	s, _ := newGIFFrames(g)
	im, e := s.at(context.Background(), 3)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 1, 1, testPalette[2])
	assertPixel(t, im, 2, 2, testPalette[1])
	if s.duration() != 15 {
		t.Fatalf("zero delay policy: %d", s.duration())
	}
	// With transparent global background, restore-to-background clears the area.
	g = fixture([]*image.Paletted{pal(image.Rect(0, 0, 2, 4), 1), pal(image.Rect(3, 0, 4, 4), 2)}, []int{10, 10}, []byte{2, 1})
	g.BackgroundIndex = 4
	s, _ = newGIFFrames(g)
	im, e = s.at(context.Background(), 3)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, color.Transparent)
	assertPixel(t, im, 3, 0, testPalette[2])
}
func TestGIFDecodeAndPreflight(t *testing.T) {
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), pal(image.Rect(0, 0, 2, 2), 2)}, []int{3, 7}, []byte{1, 3})
	var b bytes.Buffer
	if e := gif.EncodeAll(&b, g); e != nil {
		t.Fatal(e)
	}
	data := b.Bytes()
	if e := preflightGIF(context.Background(), data); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "input.gif")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := loadGIF(context.Background(), path)
	if e != nil || len(got.Image) != 2 {
		t.Fatalf("load = %v %v", got, e)
	}
	for i := 0; i < len(data); i++ {
		if e := preflightGIF(context.Background(), data[:i]); e == nil {
			t.Fatalf("truncation %d accepted", i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := preflightGIF(ctx, data); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel = %v", e)
	}
	if _, e := loadGIF(ctx, path); !errors.Is(e, context.Canceled) {
		t.Fatalf("load cancel = %v", e)
	}
	s, _ := newGIFFrames(g)
	if _, e := s.at(ctx, 0); !errors.Is(e, context.Canceled) {
		t.Fatalf("composite cancel = %v", e)
	}
}

// Synthetic blocks exercise limits BEFORE a LZW decoder allocates frame pixels.
func syntheticGIF(w, h, frames, delay int) []byte {
	b := append([]byte("GIF89a"), make([]byte, 7)...)
	binary.LittleEndian.PutUint16(b[6:], uint16(w))
	binary.LittleEndian.PutUint16(b[8:], uint16(h))
	for i := 0; i < frames; i++ {
		b = append(b, 0x21, 0xf9, 4, 0, byte(delay), byte(delay>>8), 0, 0, 0x2c, 0, 0, 0, 0, byte(w), byte(w>>8), byte(h), byte(h>>8), 0, 2, 0)
	}
	return append(b, 0x3b)
}
func TestGIFPreflightBudgets(t *testing.T) {
	for name, b := range map[string][]byte{"canvas": syntheticGIF(8192, 8192, 1, 1), "decoded pixels": syntheticGIF(4096, 4096, 9, 1), "frames": syntheticGIF(2, 2, maxGIFFrames+1, 1), "duration": syntheticGIF(2, 2, 4, 65535), "empty": syntheticGIF(2, 2, 0, 1)} {
		t.Run(name, func(t *testing.T) {
			if e := preflightGIF(context.Background(), b); e == nil {
				t.Fatal("accepted oversized/empty GIF")
			}
		})
	}
	b := syntheticGIF(2, 2, 1, 1)
	b[12] = 1
	if e := preflightGIF(context.Background(), b); e == nil {
		t.Fatal("accepted non-square GIF pixels")
	}
	b = syntheticGIF(2, 2, 1, 1)
	b[16] = 4 << 2
	if e := preflightGIF(context.Background(), b); e == nil {
		t.Fatal("accepted reserved disposal")
	}
	b = syntheticGIF(2, 2, 1, 1)
	b[14] = 0x01
	if e := preflightGIF(context.Background(), b); e == nil {
		t.Fatal("accepted unsupported text rendering")
	}
}
func FuzzGIFPreflight(f *testing.F) {
	f.Add(syntheticGIF(2, 2, 1, 1))
	f.Add([]byte("GIF89a"))
	f.Fuzz(func(t *testing.T, b []byte) { _ = preflightGIF(context.Background(), b) })
}

func TestGIFDisposalPreviousThenBackground(t *testing.T) {
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), pal(image.Rect(0, 0, 2, 4), 2), pal(image.Rect(2, 0, 4, 4), 3), pal(image.Rect(0, 0, 1, 1), 2)}, []int{10, 10, 10, 10}, []byte{1, 3, 2, 1})
	s, _ := newGIFFrames(g)
	im, e := s.at(context.Background(), 6)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 3, testPalette[1])
	assertPixel(t, im, 3, 3, testPalette[3])
	im, e = s.at(context.Background(), 9)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, testPalette[2])
	assertPixel(t, im, 0, 3, testPalette[1])
	assertPixel(t, im, 3, 3, testPalette[0])
}
func TestGIFLocalPaletteDoesNotRedefineGlobalBackground(t *testing.T) {
	local := image.NewPaletted(image.Rect(0, 0, 2, 4), color.Palette{color.White, color.RGBA{0, 255, 0, 255}})
	for i := range local.Pix {
		local.Pix[i] = 1
	}
	g := fixture([]*image.Paletted{pal(image.Rect(0, 0, 4, 4), 1), local, pal(image.Rect(3, 0, 4, 4), 2)}, []int{10, 10, 10}, []byte{1, 2, 1})
	s, _ := newGIFFrames(g)
	im, e := s.at(context.Background(), 6)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, color.Black) // global background index0, not local white
	// A transparent local palette restores clear regardless of which local index
	// holds alpha; that index must not be looked up in the global palette.
	local.Palette = append(local.Palette, color.Transparent)
	s, _ = newGIFFrames(g)
	im, e = s.at(context.Background(), 6)
	if e != nil {
		t.Fatal(e)
	}
	assertPixel(t, im, 0, 0, color.Transparent)
}
