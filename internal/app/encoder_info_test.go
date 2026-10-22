package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/media"
	"wpapp/internal/transcode"
)

func TestEncoderCapabilitiesAreCachedAndRefreshOnlyForEncoderConfig(t *testing.T) {
	var calls atomic.Int32
	e := setupWith(t, 0, false, func(d *Deps) {
		d.EncoderInfo = func(c config.Config) transcode.Capabilities {
			calls.Add(1)
			return transcode.Capabilities{Available: true, Strict: true, GIF: true, Name: c.Render.TranscodeResolution + ":" + c.Render.FFmpegPath}
		}
	})
	e.sync()
	for range 10 {
		e.a.View()
		e.sync()
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("view/publish probed encoder %d times", got)
	}
	e.a.UpdateConfig(func(c *config.Config) { c.UI.Theme = "dark" })
	e.sync()
	if calls.Load() != 1 {
		t.Fatal("unrelated settings re-detected encoder")
	}
	e.a.UpdateConfig(func(c *config.Config) { c.Render.TranscodeResolution = "1440p" })
	e.sync()
	if got := e.a.View().Encoder.Name; got != "1440p:" {
		t.Fatal(got)
	}
	e.a.UpdateConfig(func(c *config.Config) { c.Render.FFmpegPath = "custom" })
	e.sync()
	if calls.Load() != 3 || e.a.View().Encoder.Name != "1440p:custom" {
		t.Fatal(calls.Load(), e.a.View().Encoder)
	}
}

func TestManualEncodingRefreshesMetadataAndRejectsInvalidOutput(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "invalid"}[fail], func(t *testing.T) {
			e := setupWith(t, 0, true, func(d *Deps) {
				d.TranscodeExt = ".mp4"
				d.Transcode = func(_ context.Context, _, dst string) error { return os.WriteFile(dst, []byte("encoded"), 0600) }
				d.EncoderInfo = func(config.Config) transcode.Capabilities {
					return transcode.Capabilities{Available: true, Strict: true}
				}
				d.ProbeMedia = func(ctx context.Context, path string) (*media.Info, error) {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if fail {
						return nil, errors.New("invalid output")
					}
					if _, err := os.Stat(path); err != nil {
						return nil, err
					}
					return &media.Info{Kind: "video", Codec: "h264", W: 1280, H: 720, DurationSec: 2.5}, nil
				}
			})
			e.a.Transcode("x")
			if fail {
				eventually(t, "invalid output reported", func() bool { return strings.Contains(e.a.View().Transcoding["x"], "invalid output") })
				it := e.a.View().Library[0]
				if it.Entry == "optimized.mp4" {
					t.Fatal("invalid result committed")
				}
				if _, err := os.Stat(filepath.Join(it.Dir, "optimized.mp4")); !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return
			}
			eventually(t, "updated metadata", func() bool { v := e.a.View(); return len(v.Library) > 0 && v.Library[0].Transcode == "done" })
			it := e.a.View().Library[0]
			if it.Width != 1280 || it.Height != 720 || it.DurationSec != 2.5 || it.Codec != "h264" || it.SizeBytes != 7 || !strings.Contains(it.Note, "30 fps") {
				t.Fatal(it)
			}
		})
	}
}

func TestEncodingRejectsChangedSourceWithinSameDirectory(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var source string
	e := setupWith(t, 0, true, func(d *Deps) {
		item := d.Store.Library.Find("x")
		source = filepath.Join(item.Dir, filepath.FromSlash(item.Entry))
		if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		d.Transcode = func(ctx context.Context, _, dst string) error {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return os.WriteFile(dst, []byte("old rendition"), 0600)
		}
	})
	e.a.Transcode("x")
	<-started
	if err := os.WriteFile(source, []byte("replacement source is different"), 0600); err != nil {
		t.Fatal(err)
	}
	close(release)
	eventually(t, "changed source rejected", func() bool { return strings.Contains(e.a.View().Transcoding["x"], "source media changed") })
	it := e.a.View().Library[0]
	if it.Entry != "content/a.mp4" || it.Transcode == "done" {
		t.Fatal(it)
	}
	if _, err := os.Stat(filepath.Join(it.Dir, "optimized.mp4")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
