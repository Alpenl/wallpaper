package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGIFUsesCapableEncoderAndCorrectExtension(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			o := opts(t, platform)
			o.TranscodeExt = ".mp4"
			o.CanTranscodeGIF = true
			called := false
			o.Transcode = func(ctx context.Context, src, dst string) error {
				called = true
				b, err := os.ReadFile(fixture("h264_audio.mp4"))
				if err != nil {
					return err
				}
				return os.WriteFile(dst, b, 0600)
			}
			result, err := Import(context.Background(), fixture("anim.gif"), o)
			if err != nil {
				t.Fatal(err)
			}
			if !called || result.Item.Codec != "h264" || result.Item.Transcode != "done" || !strings.HasSuffix(result.Item.Entry, ".mp4") {
				t.Fatal(result.Item)
			}
		})
	}
}

func TestRequiredTranscodeRejectsInvalidOutputAndExtension(t *testing.T) {
	for _, ext := range []string{".mp4", "/../bad"} {
		t.Run(filepath.Base(ext), func(t *testing.T) {
			o := opts(t, "darwin")
			o.TranscodeExt = ext
			o.Transcode = func(ctx context.Context, src, dst string) error { return os.WriteFile(dst, []byte("bad media"), 0600) }
			if _, err := Import(context.Background(), fixture("vp9.webm"), o); err == nil {
				t.Fatal("invalid result accepted")
			}
			entries, err := os.ReadDir(o.Library)
			if err != nil || len(entries) != 0 {
				t.Fatal(entries, err)
			}
		})
	}
}

func TestGIFWithoutEncoderRetainsCompatiblePlayback(t *testing.T) {
	for _, platform := range []string{"windows", "linux"} {
		result, err := Import(context.Background(), fixture("anim.gif"), opts(t, platform))
		if err != nil || result.Item.Codec != "gif" || result.Item.Transcode != "recommended" {
			t.Fatal(result, err)
		}
	}
}
