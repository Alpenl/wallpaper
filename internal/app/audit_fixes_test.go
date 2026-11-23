package app

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/transcode"
	"wpapp/internal/visibility"
)

// H-23: App.New publishes the initial View snapshot immediately before App.Run starts.
func TestNewPublishesInitialViewImmediately(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{
		Config:  filepath.Join(dir, "cfg"),
		Library: filepath.Join(dir, "lib"),
		Cache:   filepath.Join(dir, "cache"),
		Logs:    filepath.Join(dir, "logs"),
	}
	store, err := config.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	store.Library.Upsert(config.Item{ID: "w1", Title: "Wave", Type: "video", Dir: filepath.Join(paths.Library, "w1"), Entry: "a.mp4"})
	store.Assign.Default = "w1"
	a := New(Deps{
		Host:     newHost(1),
		Store:    store,
		SafeMode: true,
	})
	v := a.View()
	if !v.SafeMode {
		t.Fatal("expected SafeMode=true immediately after New()")
	}
	if v.Config.SchemaVersion == 0 {
		t.Fatal("expected non-zero Config.SchemaVersion immediately after New()")
	}
	if len(v.Library) != 1 || v.Library[0].ID != "w1" || v.Default != "w1" {
		t.Fatalf("unexpected initial library/default: lib=%+v default=%q", v.Library, v.Default)
	}
}

type blockingCloseRenderer struct {
	*fakeR
	closeEntered chan struct{}
	closeRelease chan struct{}
	once         sync.Once
}

func (r *blockingCloseRenderer) Close() error {
	r.once.Do(func() {
		close(r.closeEntered)
		<-r.closeRelease
	})
	return r.fakeR.Close()
}

// H-24: Switching wallpaper away from item X and immediately calling Remove("x")
// waits for X's asynchronous renderer Close() barrier before deleting X's directory.
func TestRemoveWaitsForRetiringSwitchedScreen(t *testing.T) {
	closeEntered := make(chan struct{})
	closeRelease := make(chan struct{})
	var count atomic.Int32
	e := setupWith(t, 1, true, func(d *Deps) {
		itX := d.Store.Library.Find("x")
		_ = os.WriteFile(filepath.Join(itX.Dir, itX.Entry), []byte("x"), 0o600)
		dirY := filepath.Join(d.Store.Paths.Library, "y")
		_ = os.MkdirAll(filepath.Join(dirY, "content"), 0o755)
		_ = os.WriteFile(filepath.Join(dirY, "content", "b.mp4"), []byte("y"), 0o600)
		d.Store.Library.Upsert(config.Item{ID: "y", Title: "Y", Type: "video", Dir: dirY, Entry: "content/b.mp4"})
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			if count.Add(1) == 1 {
				return &blockingCloseRenderer{
					fakeR:        &fakeR{display: id, done: make(chan error, 1)},
					closeEntered: closeEntered,
					closeRelease: closeRelease,
				}
			}
			return &fakeR{display: id, done: make(chan error, 1)}
		}
	})
	eventually(t, "initial x loaded", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].ItemID == "x" && v.Displays[0].State == "active"
	})

	// Switch display A from x -> y; x's renderer Close() will block on closeRelease.
	e.a.Assign("A", "y")
	select {
	case <-closeEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("retiring x renderer did not enter Close()")
	}

	// Immediately Remove("x") while screen A now shows y and x's Close() is still blocked.
	e.a.Remove("x")
	e.sync()
	if _, err := os.Stat(filepath.Join(e.store.Paths.Library, "x")); err != nil {
		t.Fatalf("item x directory was deleted before retiring renderer closed: %v", err)
	}
	if e.a.View().Library == nil || len(e.a.View().Library) != 2 {
		t.Fatal("item x was removed from library before retiring renderer closed")
	}

	close(closeRelease)
	eventually(t, "item x removed after retiring close completed", func() bool {
		return len(e.a.View().Library) == 1 && e.a.View().Library[0].ID == "y"
	})
}

// H-24: ClearCache is mutually excluded with active rendition transcoding so it
// cannot delete in-flight .rendition-*.mp4 staging files.
func TestClearCacheBlockedDuringActiveRendition(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var stagingPath atomic.Pointer[string]
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		_ = os.WriteFile(filepath.Join(it.Dir, it.Entry), []byte("source"), 0o600)
		d.EncodeRendition = func(ctx context.Context, src, dst string, target transcode.Target) error {
			stagingPath.Store(&dst)
			close(started)
			<-release
			return errors.New("stop")
		}
	})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("rendition did not start")
	}
	p := *stagingPath.Load()
	e.a.ClearCache()
	e.sync()
	if e.a.View().LastError == "" {
		t.Fatal("expected ClearCache to reject while rendition is active")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("in-flight rendition staging file was deleted: %v", err)
	}
	close(release)
	eventually(t, "rendition finished", func() bool {
		v := e.a.View()
		return len(v.Renditions) == 1 && v.Renditions[0].Status == "优化失败"
	})
}

// H-25 + M-13: Pause config updates preserve StateFailed in place, while changing
// decoder/backend config (Hwdec / WindowsBackend) triggers recovery of StateFailed screens.
func TestPauseConfigPreservesFailedAndHwdecChangeRetries(t *testing.T) {
	var failLoad atomic.Bool
	failLoad.Store(true)
	var starts atomic.Int32
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			starts.Add(1)
			return &fakeR{display: id, done: make(chan error, 1), failLd: failLoad.Load()}
		}
	})
	// Force screen into StateFailed quickly via the visibility state machine.
	e.a.Do(func() {
		s := e.a.screens["A"]
		m := e.a.ctl.Display(s.n)
		for i := 0; i < 6 && m.State() != visibility.StateFailed; i++ {
			m.RendererDone(e.a.now())
		}
	})
	eventually(t, "screen failed", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].State == "failed"
	})

	// Changing pause config must preserve StateFailed (H-25).
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.OnCovered = !c.Pause.OnCovered })
	e.sync()
	if st := e.a.View().Displays[0].State; st != "failed" {
		t.Fatalf("pause config change cleared StateFailed: got %q", st)
	}

	// Changing Hwdec (decoder config) must trigger retry/recovery (M-13).
	failLoad.Store(false)
	e.a.UpdateConfig(func(c *config.Config) { c.Render.Hwdec = "no" })
	eventually(t, "recovered after hwdec change", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].State == "active" && v.Displays[0].Level == "L0"
	})
}

// M-13: Unplugging a display clears transient perDisp signal bits so reconnecting
// starts clean instead of stuck paused.
func TestDisplayUnplugClearsStalePerDispSignals(t *testing.T) {
	e := setup(t, 2, true)
	eventually(t, "two active screens", func() bool {
		v := e.a.View()
		return len(v.Displays) == 2 && v.Displays[1].State == "active"
	})
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "B", Reason: visibility.Fullscreen, Active: true}
	eventually(t, "B paused by fullscreen", func() bool {
		v := e.a.View()
		return len(v.Displays) == 2 && v.Displays[1].Level == "L1"
	})

	// Unplug display B.
	e.h.mu.Lock()
	savedB := e.h.displays[1]
	e.h.displays = e.h.displays[:1]
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "B removed", func() bool { return len(e.a.View().Displays) == 1 })

	// Reconnect display B -> must start at L0 (no stale Fullscreen bit).
	e.h.mu.Lock()
	e.h.displays = append(e.h.displays, savedB)
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "B reconnected clean at L0", func() bool {
		v := e.a.View()
		return len(v.Displays) == 2 && v.Displays[1].State == "active" && v.Displays[1].Level == "L0"
	})
}

// M-12: Entering L4 during crash backoff cancels the pending spawn instead of
// launching a renderer with stale pre-backoff L0 state.
func TestBackoffReevaluatesL4BeforeSpawn(t *testing.T) {
	var created atomic.Int32
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			created.Add(1)
			return &fakeR{display: id, done: make(chan error, 1)}
		}
	})
	eventually(t, "initial active", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].State == "active"
	})
	before := created.Load()

	// Schedule a delayed startScreen (simulating crash backoff) and immediately enter L4.
	e.a.Do(func() {
		s := e.a.screens["A"]
		e.a.closeScreen(s, false)
		e.a.startScreen(s, 30*time.Millisecond)
		e.a.signal(visibility.LowBattery, "", true, "test")
	})
	eventually(t, "settled at L4 active", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].State == "active" && v.Displays[0].Level == "L4"
	})
	// Wait past the 30ms backoff timer to ensure no delayed spawn fires.
	done := make(chan struct{})
	time.AfterFunc(60*time.Millisecond, func() {
		e.a.Do(func() { close(done) })
	})
	<-done
	if created.Load() != before {
		t.Fatalf("renderer spawned despite entering L4 during backoff: before=%d after=%d", before, created.Load())
	}
}

// M-14: At policy L4, item changes, display hotplug, and ExitSafeMode hide surfaces,
// never spawn child renderers, and report active L4 (not stuck in "recovering").
func TestL4UniformAcrossHotplugItemChangeAndExitSafeMode(t *testing.T) {
	var created atomic.Int32
	e := setupWith(t, 1, true, func(d *Deps) {
		d.SafeMode = true
		dirY := filepath.Join(d.Store.Paths.Library, "y")
		_ = os.MkdirAll(filepath.Join(dirY, "content"), 0o755)
		d.Store.Library.Upsert(config.Item{ID: "y", Title: "Y", Type: "video", Dir: dirY, Entry: "content/b.mp4"})
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			created.Add(1)
			return &fakeR{display: id, done: make(chan error, 1)}
		}
	})
	e.sync()

	// Activate global LowBattery (L4) while in safe mode, then exit safe mode.
	e.a.Do(func() { e.a.signal(visibility.LowBattery, "", true, "test") })
	e.a.ExitSafeMode()
	eventually(t, "A active at L4 after ExitSafeMode", func() bool {
		v := e.a.View()
		return !v.SafeMode && len(v.Displays) == 1 && v.Displays[0].State == "active" && v.Displays[0].Level == "L4"
	})
	if created.Load() != 0 {
		t.Fatalf("ExitSafeMode spawned %d renderers at L4", created.Load())
	}

	// Switch wallpaper at L4 -> must stay active at L4 with 0 renderers spawned.
	e.a.Assign("A", "y")
	eventually(t, "A switched to y at L4", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].ItemID == "y" && v.Displays[0].State == "active" && v.Displays[0].Level == "L4"
	})
	if created.Load() != 0 {
		t.Fatalf("Assign spawned %d renderers at L4", created.Load())
	}

	// Hotplug display B while at L4 -> must be active at L4 with 0 renderers spawned.
	e.h.mu.Lock()
	e.h.displays = append(e.h.displays, desktop.Display{ID: "B", Name: "B", Bounds: image.Rect(100, 0, 200, 100)})
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "B hotplugged active at L4", func() bool {
		v := e.a.View()
		return len(v.Displays) == 2 && v.Displays[1].State == "active" && v.Displays[1].Level == "L4"
	})
	if created.Load() != 0 {
		t.Fatalf("hotplug spawned %d renderers at L4", created.Load())
	}
}

// M-18: Multi-display wake / burst shell restarts deduplicate reattachAll triggers.
func TestReattachAllDeduplicatedOnMultiDisplayWake(t *testing.T) {
	e := setup(t, 3, true)
	eventually(t, "three displays active", func() bool {
		v := e.a.View()
		return len(v.Displays) == 3 && v.Displays[2].State == "active"
	})
	e.h.mu.Lock()
	beforeSurfs := len(e.h.surfs)
	e.h.healthy = errors.New("workerw lost")
	e.h.mu.Unlock()

	// Suspend and wake all 3 displays; ActHealthCheck fires on each display,
	// but reattachAll must run only once (creating 3 surfaces, not 9).
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Suspend, Active: true, Source: "test"}
	eventually(t, "all suspended at L3", func() bool {
		v := e.a.View()
		return len(v.Displays) == 3 && v.Displays[0].Level == "L3" && v.Displays[2].Level == "L3"
	})
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Suspend, Active: false, Source: "test"}
	eventually(t, "single reattach rebuilt 3 surfaces", func() bool {
		e.h.mu.Lock()
		n := len(e.h.surfs) - beforeSurfs
		e.h.mu.Unlock()
		return n == 3
	})
	e.sync()
	e.h.mu.Lock()
	rebuilt := len(e.h.surfs) - beforeSurfs
	e.h.mu.Unlock()
	if rebuilt != 3 {
		t.Fatalf("expected 3 rebuilt surfaces (1 reattach cycle), got %d", rebuilt)
	}
}
