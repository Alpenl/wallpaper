package main

import (
	"testing"
	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/transcode"
)

func TestEncodingTargetUsesPhysicalPrimaryAndExplicitBounds(t *testing.T) {
	cfg := config.Defaults()
	displays := []app.DisplayView{{ID: "left", W: 1920, H: 1080}, {ID: "primary", W: 1800, H: 3200, Primary: true}}
	if got := encodingTarget(cfg, displays); got != (transcode.Target{Width: 1800, Height: 3200}) {
		t.Fatal(got)
	}
	for value, want := range map[string]transcode.Target{"1080p": {Width: 1920, Height: 1080}, "1440p": {Width: 2560, Height: 1440}, "2160p": {Width: 3840, Height: 2160}} {
		cfg.Render.TranscodeResolution = value
		if got := encodingTarget(cfg, displays); got != want {
			t.Fatal(value, got, want)
		}
	}
	cfg.Render.TranscodeResolution = "display"
	if got := encodingTarget(cfg, nil); got != (transcode.Target{Width: 3840, Height: 2160}) {
		t.Fatal(got)
	}
	if got := encodingTarget(cfg, displays[:1]); got != (transcode.Target{Width: 1920, Height: 1080}) {
		t.Fatal(got)
	}
}

func TestEncodingTargetUsesRetinaBackingPixelsWithoutChangingTopology(t *testing.T) {
	cfg := config.Defaults()
	d := app.DisplayView{W: 1440, H: 900, PixelW: 2880, PixelH: 1800, Primary: true}
	if got := encodingTarget(cfg, []app.DisplayView{d}); got != (transcode.Target{Width: 2880, Height: 1800}) {
		t.Fatal(got)
	}
	d.W, d.H, d.PixelW, d.PixelH = 900, 1440, 1800, 2880
	if got := encodingTarget(cfg, []app.DisplayView{d}); got != (transcode.Target{Width: 1800, Height: 2880}) {
		t.Fatal(got)
	}
}

func TestTranscodeHelperFlagsAndBounds(t *testing.T) {
	f := parseFlags([]string{"-helper", "transcode", "-in", "source.gif", "-out", "output.mp4", "-width", "1080", "-height", "1920"})
	if f.helper != "transcode" || f.in != "source.gif" || f.out != "output.mp4" || f.width != 1080 || f.height != 1920 {
		t.Fatal(f)
	}
	for _, invalid := range []*flags{{}, {in: "in", out: "out", width: 1, height: 2}, {in: "in", out: "out", width: 2, height: 8193}} {
		if code := runTranscodeHelper(invalid); code != 2 {
			t.Fatal(code)
		}
	}
}
