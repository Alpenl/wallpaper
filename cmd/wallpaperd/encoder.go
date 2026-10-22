package main

import (
	"context"
	"fmt"
	"runtime"

	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/transcode"
	"wpapp/internal/transcode/nativeav"
)

// encodingTarget uses physical display pixels, never logical UI points. A
// shared fallback keeps aspect ratio; cover renditions use each display separately.
func encodingTarget(cfg config.Config, displays []app.DisplayView) transcode.Target {
	switch cfg.Render.TranscodeResolution {
	case "1080p":
		return transcode.Target{Width: 1920, Height: 1080}
	case "1440p":
		return transcode.Target{Width: 2560, Height: 1440}
	case "2160p":
		return transcode.Target{Width: 3840, Height: 2160}
	}
	for _, d := range displays {
		if d.Primary {
			if target, ok := displayEncodingTarget(d); ok {
				return target
			}
		}
	}
	for _, d := range displays {
		if target, ok := displayEncodingTarget(d); ok {
			return target
		}
	}
	return transcode.Target{Width: 3840, Height: 2160}
}

func displayEncodingTarget(d app.DisplayView) (transcode.Target, bool) {
	if d.PixelW > 0 && d.PixelH > 0 {
		return transcode.Target{Width: d.PixelW, Height: d.PixelH}, true
	}
	// Older callers/tests without separate backing size use their topology size.
	// Never multiply Windows physical bounds by a DPI scale.
	return transcode.Target{Width: d.W, Height: d.H}, d.W > 0 && d.H > 0
}

func (e *env) nativeEncoderHelper() string {
	if runtime.GOOS == "darwin" && nativeav.Available() {
		return e.exe
	}
	return ""
}

func (e *env) encoderCapabilities(cfg config.Config) transcode.Capabilities {
	return transcode.CapabilitiesForWithHelper(runtime.GOOS, cfg.Render.FFmpegPath, e.nativeEncoderHelper())
}

func (e *env) encoder(cfg config.Config) (func(context.Context, string, string) error, string, string) {
	var displays []app.DisplayView
	if e.app != nil {
		displays = e.app.View().Displays
	}
	helper := e.nativeEncoderHelper()
	fn, ext, name := transcode.EncoderForTargetWithHelper(runtime.GOOS, cfg.Render.FFmpegPath, helper, encodingTarget(cfg, displays))
	if fn == nil {
		return nil, ext, name
	}
	// Resolve physical target again when the worker actually starts, so hotplug
	// changes do not leave an initialization-time default as the output target.
	return func(ctx context.Context, src, dst string) error {
		var current []app.DisplayView
		if e.app != nil {
			current = e.app.View().Displays
		}
		encode, currentExt, currentName := transcode.EncoderForTargetWithHelper(runtime.GOOS, cfg.Render.FFmpegPath, helper, encodingTarget(cfg, current))
		if encode == nil {
			return transcode.ErrNoEncoder
		}
		if currentExt != ext || currentName != name {
			return fmt.Errorf("encoder changed before job started; retry after checking encoder settings")
		}
		return encode(ctx, src, dst)
	}, ext, name
}
