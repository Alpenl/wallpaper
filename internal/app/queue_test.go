package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/importer"
)

func TestImmediateAndLaterImportsQueueWithoutDropping(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan string, 4)
	var active, maxActive atomic.Int32
	e := setupWith(t, 1, false, func(d *Deps) {
		root := d.Store.Paths.Library
		d.Import = func(ctx context.Context, src string) (*importer.Result, error) {
			n := active.Add(1)
			defer active.Add(-1)
			if n > maxActive.Load() {
				maxActive.Store(n)
			}
			started <- src
			if src == "first.mp4" {
				select {
				case <-gate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &importer.Result{Item: config.Item{ID: src, Title: src, Type: "video", Dir: root, Entry: src}}, nil
		}
	})
	e.a.Import("first.mp4", "")
	e.a.Import("second.mp4", "")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first import did not start")
	}
	e.a.Import("third.mp4", "A")
	eventually(t, "queued", func() bool { return e.a.View().ImportQueued == 2 })
	close(gate)
	eventually(t, "all imports preserved", func() bool {
		v := e.a.View()
		return !v.Importing && v.ImportQueued == 0 && len(v.Library) == 3 && byID(v, "A").ItemID == "third.mp4"
	})
	if maxActive.Load() != 1 {
		t.Fatal("concurrent importers", maxActive.Load())
	}
}

func TestEncoderQueueIsSerialAndRemoveDropsPending(t *testing.T) {
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	var mu sync.Mutex
	var encoded []string
	e := setupWith(t, 0, false, func(d *Deps) {
		for i := 0; i < 5; i++ {
			id := fmt.Sprint(i)
			dir := filepath.Join(d.Store.Paths.Library, id)
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, "source.webm"), nil, 0600)
			d.Store.Library.Upsert(config.Item{ID: id, Dir: dir, Type: "video", Entry: "source.webm", Transcode: "recommended"})
		}
		d.Transcode = func(ctx context.Context, src, dst string) error {
			n := active.Add(1)
			defer active.Add(-1)
			if n > maxActive.Load() {
				maxActive.Store(n)
			}
			id := filepath.Base(filepath.Dir(src))
			mu.Lock()
			encoded = append(encoded, id)
			mu.Unlock()
			if id == "0" {
				close(firstStarted)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return os.WriteFile(dst, []byte("x"), 0600)
		}
	})
	for i := 0; i < 5; i++ {
		e.a.Transcode(fmt.Sprint(i))
	}
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("encoder not started")
	}
	eventually(t, "pending encoders", func() bool { return e.a.View().Transcoding["4"] == "等待转码…" })
	e.a.Remove("2")
	eventually(t, "queued removal", func() bool { return len(e.a.View().Library) == 4 })
	close(release)
	eventually(t, "all queued encodes complete", func() bool {
		v := e.a.View()
		if len(v.Transcoding) != 0 {
			return false
		}
		for _, it := range v.Library {
			if it.Transcode != "done" {
				return false
			}
		}
		return true
	})
	if maxActive.Load() != 1 {
		t.Fatal("unbounded encoders", maxActive.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(encoded) != 4 {
		t.Fatal(encoded)
	}
	for _, id := range encoded {
		if id == "2" {
			t.Fatal("removed queued item encoded")
		}
	}
}

func TestRemoveCancelsActiveEncoder(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	e := setupWith(t, 0, true, func(d *Deps) {
		src := filepath.Join(d.Store.Paths.Library, "x", "content", "a.mp4")
		if err := os.WriteFile(src, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
		d.Transcode = func(ctx context.Context, src, _ string) error {
			f, err := os.Open(src)
			if err != nil {
				return err
			}
			defer f.Close()
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release // Cancellation does not mean the encoder has released its handles.
			return ctx.Err()
		}
	})
	// Unblock before setup's cleanup waits for shutdown, including on Fatal.
	t.Cleanup(unblock)
	e.a.Transcode("x")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("not started")
	}
	e.a.Remove("x")
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("not cancelled")
	}
	e.sync()
	if len(e.a.View().Library) != 1 {
		t.Fatal("removed before encoder released source")
	}
	unblock()
	eventually(t, "removed status", func() bool { return len(e.a.View().Library) == 0 && len(e.a.View().Transcoding) == 0 })
	if _, err := os.Stat(filepath.Join(e.store.Paths.Library, "x")); !os.IsNotExist(err) {
		t.Fatalf("source directory remains: %v; last error: %s", err, e.a.View().LastError)
	}
}

func TestReimportAfterRemovalDiscardsOldEncoderOutput(t *testing.T) {
	importErrors := make(chan error, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	importRelease := make(chan struct{})
	var once sync.Once
	unblockImport := func() { once.Do(func() { close(importRelease) }) }
	defer unblockImport()
	e := setupWith(t, 1, true, func(d *Deps) {
		root := d.Store.Paths.Library
		d.Transcode = func(_ context.Context, _, dst string) error {
			close(started)
			<-release
			return os.WriteFile(dst, []byte("old encode"), 0600)
		}
		d.Import = func(context.Context, string) (_ *importer.Result, err error) {
			defer func() {
				if err != nil {
					importErrors <- err
				}
			}()
			<-importRelease
			dir := filepath.Join(root, "x")
			old, err := tombstone(dir)
			if err != nil {
				return nil, err
			}
			if old != "" {
				defer os.RemoveAll(old)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, "new.mp4"), []byte("new media"), 0600); err != nil {
				return nil, err
			}
			return &importer.Result{Item: config.Item{ID: "x", Title: "Replacement", Type: "video", Dir: dir, Entry: "new.mp4", Transcode: "none"}}, nil
		}
	})
	t.Cleanup(unblockImport)
	waitForStorageScan(t, e)
	eventually(t, "screen started", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })

	e.a.Transcode("x")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no encoder")
	}
	e.a.Import("replacement.wpk", "")
	eventually(t, "import waits for encoder", func() bool { return e.a.View().ImportQueued == 1 })
	e.a.Remove("x")
	close(release)
	eventually(t, "old record removed", func() bool { return len(e.a.View().Library) == 0 })
	unblockImport()
	eventually(t, "same-ID refreshed", func() bool {
		select {
		case err := <-importErrors:
			t.Fatalf("replacement import failed: %v", err)
		default:
		}
		v := e.a.View()
		return len(v.Library) == 1 && v.Library[0].Title == "Replacement"
	})
	eventually(t, "stale job discarded", func() bool {
		done := make(chan bool)
		e.a.Do(func() { done <- e.a.transcodingID == "" })
		return <-done
	})
	v := e.a.View()
	if v.Library[0].Entry != "new.mp4" || v.Library[0].Transcode != "none" {
		t.Fatal(v.Library[0])
	}
	if _, err := os.Stat(filepath.Join(e.store.Paths.Library, "x", "optimized.mp4")); !os.IsNotExist(err) {
		t.Fatal("stale output installed", err)
	}
}

func TestShutdownCancelsRunningImportAndDoesNotStartQueue(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var calls atomic.Int32
	e := setupWith(t, 0, false, func(d *Deps) {
		d.Import = func(ctx context.Context, _ string) (*importer.Result, error) {
			calls.Add(1)
			close(started)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}
	})
	e.a.Import("first.mp4", "")
	e.a.Import("second.mp4", "")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("not started")
	}
	e.cancel()
	e.a.Wait()
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("not cancelled")
	}
	if calls.Load() != 1 {
		t.Fatal("queued import ran after shutdown", calls.Load())
	}
}

func TestShutdownCancelsEncoderAndCleansTemporaryOutput(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	e := setupWith(t, 0, true, func(d *Deps) {
		d.Transcode = func(ctx context.Context, _, dst string) error {
			if err := os.WriteFile(dst, []byte("partial"), 0600); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		}
	})
	e.a.Transcode("x")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("not started")
	}
	e.cancel()
	e.a.Wait()
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("not cancelled")
	}
	eventually(t, "temporary file cleaned after shutdown", func() bool {
		files, _ := filepath.Glob(filepath.Join(e.store.Paths.Library, ".transcode-*"))
		return len(files) == 0
	})
}

func TestRemovalCannotDeleteUncommittedReimport(t *testing.T) {
	importErrors := make(chan error, 1)
	importStarted := make(chan struct{})
	diskCommitted := make(chan struct{})
	finish := make(chan struct{})
	e := setupWith(t, 0, true, func(d *Deps) {
		root := d.Store.Paths.Library
		d.Import = func(ctx context.Context, _ string) (_ *importer.Result, err error) {
			close(importStarted)
			defer func() {
				if err != nil {
					importErrors <- err
				}
			}()
			dir := filepath.Join(root, "x")
			old, err := tombstone(dir)
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(old)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, "new.mp4"), []byte("new"), 0600); err != nil {
				return nil, err
			}
			close(diskCommitted)
			select {
			case <-finish:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &importer.Result{Item: config.Item{ID: "x", Title: "New", Dir: dir, Type: "video", Entry: "new.mp4"}}, nil
		}
	})
	waitForStorageScan(t, e)
	e.a.Import("same.wpk", "")
	select {
	case <-diskCommitted:
	case err := <-importErrors:
		t.Fatalf("replacement import failed before disk commit: %v", err)
	case <-time.After(10 * time.Second):
		started := false
		select {
		case <-importStarted:
			started = true
		default:
		}
		v := e.a.View()
		t.Fatalf("disk commit not reached: started=%t importing=%t queued=%d last error=%q", started, v.Importing, v.ImportQueued, v.LastError)
	}
	e.a.Remove("x")
	eventually(t, "busy deletion reported", func() bool { return e.a.View().LastError == "导入进行中，请完成后再删除壁纸" })
	close(finish)
	eventually(t, "import committed", func() bool {
		v := e.a.View()
		return !v.Importing && len(v.Library) == 1 && v.Library[0].Title == "New"
	})
	if _, err := os.Stat(filepath.Join(e.store.Paths.Library, "x", "new.mp4")); err != nil {
		t.Fatal("uncommitted reimport deleted", err)
	}
}

func TestMissingQueuedSourceDoesNotStallLaterEncoder(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	e := setupWith(t, 0, true, func(d *Deps) {
		for _, id := range []string{"missing", "valid"} {
			dir := filepath.Join(d.Store.Paths.Library, id)
			os.MkdirAll(dir, 0755)
			d.Store.Library.Upsert(config.Item{ID: id, Dir: dir, Type: "video", Entry: "source.mp4"})
		}
		d.Transcode = func(ctx context.Context, src, dst string) error {
			if filepath.Base(src) == "a.mp4" {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return os.WriteFile(dst, []byte("ok"), 0600)
		}
	})
	e.a.Transcode("x")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("not started")
	}
	e.a.Transcode("missing")
	e.a.Transcode("valid")
	eventually(t, "queued", func() bool { return e.a.View().Transcoding["valid"] == "等待转码…" })
	os.RemoveAll(filepath.Join(e.store.Paths.Library, "missing"))
	close(release)
	eventually(t, "valid task advanced", func() bool {
		for _, it := range e.a.View().Library {
			if it.ID == "valid" {
				return it.Transcode == "done"
			}
		}
		return false
	})
}

func TestSameIDPackageUpdateRefreshesActiveRenderer(t *testing.T) {
	e := setupWith(t, 1, true, func(d *Deps) {
		root := d.Store.Paths.Library
		d.Import = func(context.Context, string) (*importer.Result, error) {
			return &importer.Result{Item: config.Item{ID: "x", Title: "Updated", Dir: filepath.Join(root, "x"), Type: "video", Entry: "new.mp4"}}, nil
		}
	})
	eventually(t, "started", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
	first := e.cur("A")
	e.a.Import("update.wpk", "")
	eventually(t, "same ID renderer replaced", func() bool {
		v := e.a.View()
		return !v.Importing && len(v.Library) == 1 && v.Library[0].Title == "Updated" && e.cur("A") != first && e.cur("A").isLoaded()
	})
}

func TestCancelImportsStopsActiveAndDropsQueue(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	e := setupWith(t, 1, false, func(d *Deps) {
		d.Import = func(ctx context.Context, src string) (*importer.Result, error) {
			calls.Add(1)
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	e.a.Import("first.mp4", "A")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("import did not start")
	}
	e.a.Import("second.mp4", "A")
	eventually(t, "queued", func() bool { return e.a.View().ImportQueued == 1 })
	e.a.CancelImports()
	e.a.CancelImports()
	eventually(t, "cancelled", func() bool {
		v := e.a.View()
		return !v.Importing && v.ImportQueued == 0 && v.LastImport == "导入已取消；已完成的项目保留在壁纸库"
	})
	if calls.Load() != 1 || len(e.a.View().Library) != 0 || byID(e.a.View(), "A").ItemID != "" {
		t.Fatal("cancelled work applied or queued import started")
	}
}

func TestCancelledBatchKeepsCompletedItemsWithoutApplying(t *testing.T) {
	sources := t.TempDir()
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(sources, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	second := make(chan struct{})
	e := setupWith(t, 1, false, func(d *Deps) {
		d.Import = func(ctx context.Context, src string) (*importer.Result, error) {
			if filepath.Base(src) == "b.mp4" {
				close(second)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &importer.Result{Item: config.Item{ID: "completed", Title: "Completed", Type: "video", Dir: d.Store.Paths.Library, Entry: "a.mp4"}}, nil
		}
	})
	e.a.Import(sources, "A")
	select {
	case <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("second source never started")
	}
	eventually(t, "first committed", func() bool { return len(e.a.View().Library) == 1 })
	e.a.CancelImports()
	eventually(t, "cancelled batch", func() bool { return !e.a.View().Importing })
	v := e.a.View()
	if len(v.Library) != 1 || v.Library[0].ID != "completed" || byID(v, "A").ItemID != "" {
		t.Fatal("cancellation removed completed item or applied partial batch", v.Library)
	}
}

// These tests exercise import/removal ordering, not concurrent storage scans.
// Run starts a WalkDir scan whose directory handles can block Rename on Windows.
func waitForStorageScan(t *testing.T, e *env) {
	t.Helper()
	eventually(t, "storage scan finished", func() bool {
		idle := make(chan bool, 1)
		// Read on the event loop: an initial zero View is not a startup barrier.
		e.a.Do(func() { idle <- !e.a.storage.Scanning && !e.a.storageDirty })
		return <-idle
	})
}
