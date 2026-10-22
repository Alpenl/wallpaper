//go:build darwin

package nativeav

import (
	"context"
	"image"
	"image/color"
	"image/gif"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wpapp/internal/media"
)

// Explicitly opt in on a real Mac: this launches the system H.264 encoder. Linux
// cross-compilation is not evidence that Objective-C calls or codecs executed.
func TestNativeAVIntegration(t *testing.T) {
	if os.Getenv("WP_NATIVEAV_INTEGRATION") != "1" {
		t.Skip("set WP_NATIVEAV_INTEGRATION=1 on macOS to run native codec integration")
	}
	if !Available() {
		t.Fatalf("AVFoundation unavailable: %v", loadNative())
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "source.gif")
	mp4 := filepath.Join(dir, "gif.mp4")
	roundtrip := filepath.Join(dir, "resized.mp4")
	palette := color.Palette{color.Black, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}, color.Transparent}
	a := image.NewPaletted(image.Rect(0, 0, 320, 180), palette)
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			if y < 90 {
				a.SetColorIndex(x, y, 1)
			} else {
				a.SetColorIndex(x, y, 2)
			}
		}
	}
	b := image.NewPaletted(image.Rect(40, 40, 120, 120), palette)
	for i := range b.Pix {
		b.Pix[i] = 3
	}
	g := &gif.GIF{Image: []*image.Paletted{a, b, a}, Delay: []int{50, 25, 75}, Disposal: []byte{1, 3, 1}, Config: image.Config{ColorModel: palette, Width: 320, Height: 180}}
	f, e := os.Create(src)
	if e != nil {
		t.Fatal(e)
	}
	e = gif.EncodeAll(f, g)
	closeErr := f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if e = Encode(ctx, src, mp4, 1920, 1080); e != nil {
		t.Fatal(e)
	}
	verify := func(t *testing.T, path string, w, h int, frames uint64) {
		t.Helper()
		info, e := media.ProbeFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if info.Container != "mp4" || info.Codec != "h264" || info.W != w || info.H != h || info.VideoTracks != 1 || info.HasAudio || math.Abs(info.FPS-30) > 0.001 || !info.ConstantFrameRate || info.FrameCount != frames || !info.StartsWithKeyframe || info.MaxKeyframeInterval < 1 || info.MaxKeyframeInterval > 30 {
			t.Fatalf("unexpected native output: %+v", info)
		}
	}
	verify(t, mp4, 320, 180, 45)
	if e = Encode(ctx, mp4, roundtrip, 160, 90); e != nil {
		t.Fatal(e)
	}
	verify(t, roundtrip, 160, 90, 45)
	for _, name := range []string{"h264_audio.mp4", "hevc.mov"} {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join("..", "..", "media", "testdata", name)
			original, err := media.ProbeFile(input)
			if err != nil {
				t.Fatal(err)
			}
			if name == "h264_audio.mp4" && !original.HasAudio {
				t.Fatal("fixture must contain audio")
			}
			if name == "hevc.mov" && math.Abs(original.FPS-24) > 0.01 {
				t.Fatalf("fixture must be 24 fps, got %v", original.FPS)
			}
			output := filepath.Join(dir, name+".mp4")
			if err = Encode(ctx, input, output, 320, 180); err != nil {
				t.Fatal(err)
			}
			w, h, err := outputSize(original.W, original.H, 320, 180)
			if err != nil {
				t.Fatal(err)
			}
			verify(t, output, w, h, uint64(math.Ceil(original.DurationSec*FPS-1e-6)))
		})
	}
	// No helper should overwrite an arbitrary existing destination, even valid
	// media. This also verifies the source is not removed on a rejected encode.
	before, e := os.ReadFile(mp4)
	if e != nil {
		t.Fatal(e)
	}
	if e = Encode(ctx, src, mp4, 160, 90); e == nil {
		t.Fatal("overwrote existing output")
	}
	after, e := os.ReadFile(mp4)
	if e != nil || string(before) != string(after) {
		t.Fatal("existing output changed")
	}
}
