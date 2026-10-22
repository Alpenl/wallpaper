package app

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/media"
	"wpapp/internal/transcode"
)

func TestRenditionSelectionFallbackAndCleanup(t *testing.T) {
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0600)
	})
	e.sync()
	done := make(chan struct{})
	e.a.Do(func() {
		defer close(done)
		s := e.a.screens[desktop.DisplayID("A")]
		src := filepath.Join(s.item.Dir, s.item.Entry)
		r, ok := e.a.renditionFor(s)
		if !ok {
			t.Error("no target")
			return
		}
		oldPixels, oldScale := s.d.Pixels, s.d.Scale
		s.d.Pixels = image.Pt(200, 100)
		s.d.Scale = 2
		backing, yes := e.a.renditionFor(s)
		if !yes || backing.Target.Width != 200 || backing.Target.Height != 100 || backing.Key() == r.Key() {
			t.Error("backing size ignored")
		}
		s.d.Scale = 3
		scaled, _ := e.a.renditionFor(s)
		if scaled.Key() != backing.Key() {
			t.Error("DPI scale incorrectly changed pixel target")
		}
		s.d.Pixels, s.d.Scale = oldPixels, oldScale
		if got := e.a.renditionPath(s, false); got != src {
			t.Error("missing rendition must fall back", got)
		}
		tmp := filepath.Join(e.store.Paths.Cache, "staged.mp4")
		os.MkdirAll(e.store.Paths.Cache, 0700)
		os.WriteFile(tmp, []byte("verified"), 0600)
		if err := r.Publish(e.store.Paths.Cache, "x", tmp); err != nil {
			t.Error(err)
			return
		}
		if got := e.a.renditionPath(s, false); got != r.Path(e.store.Paths.Cache, "x") {
			t.Error("did not select exact rendition", got)
		}
		if err := e.a.cleanRenditions(); err != nil {
			t.Error(err)
		}
		if !r.Ready(e.store.Paths.Cache, "x") {
			t.Error("desired rendition deleted")
		}
		// The fake renderer does not support fit; use item policy with no renderer.
		oldRenderer := s.r
		defer func() { s.r = oldRenderer }()
		s.r = nil
		s.item.Props.Fit = "fit"
		if got := e.a.renditionPath(s, false); got != src {
			t.Error("fit used cropped rendition", got)
		}
		s.mediaPath = r.Path(e.store.Paths.Cache, "x")
		if err := e.a.cleanRenditions(); err != nil {
			t.Error(err)
		}
		if !r.Ready(e.store.Paths.Cache, "x") {
			t.Error("in-use rendition deleted")
		}
		s.mediaPath = src
		if err := e.a.cleanRenditions(); err != nil {
			t.Error(err)
		}
		if r.Ready(e.store.Paths.Cache, "x") {
			t.Error("unreferenced rendition retained")
		}
	})
	<-done
}

func TestRenditionQueueDeduplicatesAndSerializesSharedCancel(t *testing.T) {
	started := make(chan struct{}, 2)
	shared := make(chan struct{}, 1)
	var active, maxActive, calls atomic.Int32
	e := setupWith(t, 2, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0600)
		d.EncodeRendition = func(ctx context.Context, src, dst string, target transcode.Target) error {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			maxActive.Store(max(n, maxActive.Load()))
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		d.Transcode = func(ctx context.Context, src, dst string) error {
			n := active.Add(1)
			defer active.Add(-1)
			maxActive.Store(max(n, maxActive.Load()))
			shared <- struct{}{}
			return os.WriteFile(dst, []byte("shared"), 0600)
		}
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("rendition not started")
	}
	e.a.OptimizeDisplays("x")
	e.a.Transcode("x")
	e.sync()
	if calls.Load() != 1 {
		t.Fatal("equal display targets not deduplicated")
	}
	select {
	case <-shared:
		t.Fatal("shared encoder overlapped rendition")
	default:
	}
	e.a.CancelRenditions("x")
	select {
	case <-shared:
	case <-time.After(time.Second):
		t.Fatal("shared job did not advance after cancel")
	}
	if maxActive.Load() != 1 {
		t.Fatal("multiple active encoders")
	}
}

func TestRenditionVerificationAndDeletion(t *testing.T) {
	var encoded atomic.Int32
	e := setupWith(t, 2, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0600)
		d.EncodeRendition = func(ctx context.Context, src, dst string, target transcode.Target) error {
			encoded.Add(1)
			return os.WriteFile(dst, []byte("verified"), 0600)
		}
		d.ProbeMedia = func(context.Context, string) (*media.Info, error) {
			return &media.Info{Kind: "video", Container: "mp4", Codec: "h264", VideoTracks: 1, W: 100, H: 100, FPS: 30, ConstantFrameRate: true, DurationSec: 1, FrameCount: 30, StartsWithKeyframe: true, MaxKeyframeInterval: 30}, nil
		}
	})
	eventually(t, "both ready", func() bool {
		v := e.a.View()
		return len(v.Renditions) == 2 && v.Renditions[0].Status == "已优化" && v.Renditions[1].Status == "已优化"
	})
	if encoded.Load() != 1 {
		t.Fatal("duplicate encode", encoded.Load())
	}
	e.a.RefreshStorage()
	eventually(t, "cache counted", func() bool { return e.a.View().Storage.CacheBytes > 8 })
	e.a.Remove("x")
	eventually(t, "item removed", func() bool { return len(e.a.View().Library) == 0 })
	if _, err := os.Stat(filepath.Join(e.store.Paths.Cache, "x")); !os.IsNotExist(err) {
		t.Fatal("item cache remains", err)
	}
}

func TestRenditionRejectsWrongSize(t *testing.T) {
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0600)
		d.EncodeRendition = func(ctx context.Context, src, dst string, target transcode.Target) error {
			return os.WriteFile(dst, []byte("wrong output"), 0600)
		}
		d.ProbeMedia = func(context.Context, string) (*media.Info, error) {
			return &media.Info{Kind: "video", Container: "mp4", Codec: "h264", VideoTracks: 1, W: 50, H: 50, FPS: 30, ConstantFrameRate: true, DurationSec: 1, FrameCount: 30, StartsWithKeyframe: true, MaxKeyframeInterval: 30}, nil
		}
	})
	eventually(t, "verification failure", func() bool {
		v := e.a.View()
		return len(v.Renditions) == 1 && v.Renditions[0].Status == "优化失败"
	})
	paths, _ := filepath.Glob(filepath.Join(e.store.Paths.Cache, "x", "renditions", "*", "manifest.json"))
	if len(paths) != 0 {
		t.Fatal("bad output published")
	}
}

// Cancellation is only a request. Remove must wait for the encoder (and its
// deferred file closes), even when it takes time to observe cancellation.
func TestRemoveWaitsForRenditionHandles(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		if err := os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
		d.EncodeRendition = func(ctx context.Context, src, dst string, target transcode.Target) error {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return ctx.Err()
		}
	})
	<-started
	e.a.Remove("x")
	<-cancelled
	e.sync()
	if _, err := os.Stat(filepath.Join(e.store.Paths.Library, "x", "content", "a.mp4")); err != nil {
		t.Fatal("source removed before encoder returned", err)
	}
	if len(e.a.View().Library) != 1 {
		t.Fatal("library removed before encoder returned")
	}
	close(release)
	eventually(t, "removed after handles released", func() bool { return len(e.a.View().Library) == 0 })
}
