package app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/visibility"
)

type canvasHost struct {
	*fakeHost
	cmu      sync.Mutex
	canvases map[desktop.DisplayID]image.Rectangle
	created  int
}

func (h *canvasHost) Caps() desktop.Caps {
	return desktop.Caps{Video: true, PerDisplay: true, Spanning: true}
}
func (h *canvasHost) CreateCanvasSurface(id desktop.DisplayID, canvas image.Rectangle) (desktop.Surface, error) {
	s, e := h.fakeHost.CreateSurface(id)
	if e != nil {
		return nil, e
	}
	h.cmu.Lock()
	h.canvases[id] = canvas
	h.created++
	h.cmu.Unlock()
	return s, nil
}
func (h *canvasHost) count() int { h.cmu.Lock(); defer h.cmu.Unlock(); return h.created }
func spanningEnv(t *testing.T) (*env, *canvasHost) {
	var h *canvasHost
	e := setupWith(t, 2, true, func(d *Deps) {
		h = &canvasHost{fakeHost: d.Host.(*fakeHost), canvases: map[desktop.DisplayID]image.Rectangle{}}
		h.displays[0].Bounds = image.Rect(-160, -40, 0, 60)
		h.displays[1].Bounds = image.Rect(0, 0, 200, 140)
		d.Host = h
		d.Store.Assign.Displays = map[string]string{"A": "x", "B": "y"}
		d.Store.Assign.Options = map[string]config.DisplayOptions{"A": {Fit: "fit", Speed: 2}, "B": {Fit: "stretch", Speed: .5}}
		d.Store.Library.Upsert(config.Item{ID: "y", Type: "video", Dir: t.TempDir(), Entry: "b.mp4"})
		// The test renderers don't read media; omit nonexistent snapshot fixtures.
		d.Store.Library.Items[0].Snapshot = ""
	})
	eventually(t, "two initialized screens", func() bool { return len(e.a.View().Displays) == 2 && e.cur("B") != nil && e.cur("B").isLoaded() })
	return e, h
}
func TestSpanningPreservesIndependentAssignmentsAndPause(t *testing.T) {
	e, h := spanningEnv(t)
	e.a.SetLayout("span", "x")
	eventually(t, "canvas active", func() bool { return e.a.View().Layout == "span" && h.count() == 2 && e.cur("B").isLoaded() })
	v := e.a.View()
	for _, d := range v.Displays {
		if d.ItemID != "x" {
			t.Fatal("not common item", v)
		}
	}
	h.cmu.Lock()
	for _, canvas := range h.canvases {
		if canvas != image.Rect(-160, -40, 200, 140) {
			t.Error(canvas)
		}
	}
	h.cmu.Unlock()
	options := v.Displays[0].Options
	options.Paused = true
	options.Fit = "stretch"
	options.Speed = 1.5
	e.a.SetDisplayOptions("A", options)
	eventually(t, "shared options but per-screen pause", func() bool {
		v := e.a.View()
		return v.Displays[0].Options.Paused && v.Displays[1].Options.Fit == "stretch" && v.Displays[1].Options.Speed == 1.5 && v.Displays[0].Level == "L1"
	})
	if e.a.View().Displays[1].Level != "L0" {
		t.Fatal("span paused unrelated screen")
	}
	e.a.Assign("B", "y")
	eventually(t, "span assignment affects both", func() bool { v := e.a.View(); return v.Displays[0].ItemID == "y" && v.Displays[1].ItemID == "y" })
	e.a.SetLayout("independent", "")
	eventually(t, "independent restored", func() bool {
		v := e.a.View()
		return v.Layout == "independent" && v.Displays[0].ItemID == "x" && v.Displays[1].ItemID == "y"
	})
	v = e.a.View()
	if v.Displays[0].Options.Fit != "fit" || v.Displays[0].Options.Speed != 2 || v.Displays[1].Options.Speed != .5 || !v.Displays[0].Options.Paused {
		t.Fatalf("lost independent options: %+v", v.Displays)
	}
}
func TestSpanningHotplugRebuildsEveryCanvasAndReattaches(t *testing.T) {
	e, h := spanningEnv(t)
	e.a.SetLayout("span", "x")
	eventually(t, "span", func() bool { return h.count() == 2 && e.a.View().Layout == "span" })
	old := e.cur("A")
	h.mu.Lock()
	h.displays = append(h.displays, desktop.Display{ID: "C", Bounds: image.Rect(400, -200, 500, 0)})
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "hotplug reflows all", func() bool { return len(e.a.View().Displays) == 3 && h.count() == 5 && e.cur("A") != old })
	h.cmu.Lock()
	for _, canvas := range h.canvases {
		if canvas != image.Rect(-160, -200, 500, 140) {
			t.Error(canvas)
		}
	}
	h.cmu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "B", Reason: visibility.Fullscreen, Active: true}
	eventually(t, "independent automatic pause", func() bool { v := e.a.View(); return v.Displays[1].Level == "L1" && v.Displays[0].Level == "L0" })
	h.ev <- desktop.Event{Kind: desktop.EventShellRestarted, Source: "test"}
	eventually(t, "spanning recovery", func() bool { return h.count() >= 8 && e.cur("C") != nil && e.cur("C").isLoaded() })
	h.mu.Lock()
	h.displays = h.displays[:1]
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "unplug", func() bool { return len(e.a.View().Displays) == 1 && h.count() >= 9 })
	if e.a.View().Displays[0].ID != "A" || e.a.View().Layout != "span" {
		t.Fatal("unstable identity/mode")
	}
}
func TestSpanningUnsupportedHostKeepsSavedPreference(t *testing.T) {
	e := setupWith(t, 1, true, func(d *Deps) { d.Store.Assign.Layout = "span"; d.Store.Assign.SpanItem = "x" })
	eventually(t, "independent fallback", func() bool { return len(e.a.View().Displays) == 1 })
	v := e.a.View()
	if v.Layout != "independent" || v.LayoutNote == "" || v.Capabilities.Spanning {
		t.Fatal("unsupported host falsely advertised", v.Layout, v.LayoutNote)
	}
	e.a.SetLayout("span", "x")
	eventually(t, "reject unsupported request", func() bool { return e.a.View().LastError != "" })
	ch := make(chan string, 1)
	e.a.Do(func() { ch <- e.store.Assign.Layout })
	if <-ch != "span" {
		t.Fatal("saved preference lost")
	}
}
func TestSpanningAssignmentPersistenceAndNext(t *testing.T) {
	e, _ := spanningEnv(t)
	e.a.SetLayout("span", "x")
	eventually(t, "span", func() bool { return e.a.View().Layout == "span" })
	e.a.Next()
	eventually(t, "next span once", func() bool {
		v := e.a.View()
		return v.Default == "y" && v.Displays[0].ItemID == "y" && v.Displays[1].ItemID == "y"
	})
	done := make(chan error, 1)
	e.a.Do(func() { done <- e.store.AssignSaver.Flush() })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var saved config.Assignments
	if err := config.Load(e.store.AssignSaver.Path, config.AssignmentsVersion, nil, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Layout != "span" || saved.SpanItem != "y" || saved.Displays["A"] != "x" || saved.Displays["B"] != "y" {
		t.Fatalf("bad persisted assignments: %+v", saved)
	}
}

func TestSpanningOwnsResumeAndAudioWithoutChangingIndependentHistory(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Muted = false
	a := New(Deps{Store: &config.Store{Config: cfg, Assign: config.Assignments{}, State: config.State{Positions: map[string]float64{"A|x": 12, "B|x": 29}}}})
	canvas := image.Rect(-100, 0, 100, 100)
	for i, id := range []desktop.DisplayID{"A", "B"} {
		a.screens[id] = &screen{worker: &screenWorker{}, d: desktop.Display{ID: id, Primary: i == 0}, canvas: canvas, item: &config.Item{ID: "x", Type: "video"}, started: true, r: &optionRenderer{fakeR: &fakeR{}, pos: float64(40 + i)}}
	}
	primary, secondary := a.screens["A"], a.screens["B"]
	p, _ := a.playbackOptions(primary, primary.r)
	q, _ := a.playbackOptions(secondary, secondary.r)
	if p.Muted || !q.Muted {
		t.Fatal("span audio must play on primary only")
	}
	a.capturePosition(primary)
	a.capturePosition(secondary)
	if a.d.Store.State.Positions["@span|x"] != 40 || a.d.Store.State.Positions["A|x"] != 12 || a.d.Store.State.Positions["B|x"] != 29 {
		t.Fatal("span polluted independent resume history")
	}
	if a.positionKey(primary) != a.positionKey(secondary) {
		t.Fatal("spanning startup offsets differ")
	}
}
func TestSpanningStaticWallpaperUsesDistinctCachedCrops(t *testing.T) {
	e, h := spanningEnv(t)
	h.mu.Lock()
	h.displays[1].Pixels = image.Pt(400, 280) // Retina: logical 200x140, physical 400x280.
	h.mu.Unlock()
	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	src := image.NewRGBA(image.Rect(0, 0, 360, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 360; x++ {
			src.Set(x, y, color.RGBA{uint8(x % 256), uint8(y), 0, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()
	e.a.Do(func() { e.store.Library.Upsert(config.Item{ID: "photo", Type: "image", Dir: dir, Entry: "image.png"}) })
	e.a.SetLayout("span", "photo")
	eventually(t, "static crops installed", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.wall["A"] != "" && h.wall["B"] != "" && h.wall["A"] != h.wall["B"]
	})
	for _, id := range []desktop.DisplayID{"A", "B"} {
		h.mu.Lock()
		file := h.wall[id]
		h.mu.Unlock()
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		wantW, wantH := 160, 100
		if id == "B" {
			wantW, wantH = 400, 280
		}
		if cfg.Width != wantW || cfg.Height != wantH {
			t.Fatalf("%s crop geometry: %+v", id, cfg)
		}
	}
	eventually(t, "static panorama geometry and fit published", func() bool {
		v := e.a.View()
		return len(v.Displays) == 2 && v.Displays[1].W == 200 && v.Displays[1].H == 140 && v.Displays[1].PixelW == 400 && v.Displays[1].PixelH == 280 && v.Displays[0].Playback.Fit
	})
}
func TestCanceledCanvasWorkerDoesNotChangeSystemWallpaper(t *testing.T) {
	host := newHost(1)
	st := &config.Store{Config: config.Defaults(), Assign: config.Assignments{}}
	a := New(Deps{Store: st, Host: host})
	s := &screen{worker: &screenWorker{}, d: host.displays[0], canvas: image.Rect(0, 0, 100, 100), item: &config.Item{ID: "x", Type: "image", Dir: "missing", Entry: "a.png"}, ops: make(chan func(), 1), quit: make(chan struct{})}
	a.installSystemWallpaper(s)
	close(s.quit)
	(<-s.ops)()
	if len(host.wall) != 0 {
		t.Fatal("retired screen changed system wallpaper")
	}
}

func TestSpanningScaleChangeRecreatesNativeSurface(t *testing.T) {
	e, h := spanningEnv(t)
	e.a.SetLayout("span", "x")
	eventually(t, "span", func() bool { return h.count() == 2 && e.a.View().Layout == "span" })
	h.mu.Lock()
	h.displays[0].Scale = 2
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "scale-only rebuild", func() bool { return h.count() == 3 })
	if len(e.a.View().Displays) != 2 {
		t.Fatal("scale change altered identity")
	}
	h.mu.Lock()
	h.displays[0].Primary = false
	h.displays[1].Primary = true
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "primary-only audio-owner rebuild", func() bool { return h.count() == 5 })
}

func TestSpanningBackingPixelOnlyChangeRecreatesSurface(t *testing.T) {
	e, h := spanningEnv(t)
	e.a.SetLayout("span", "x")
	eventually(t, "span", func() bool { return h.count() == 2 && e.a.View().Layout == "span" })
	h.mu.Lock()
	h.displays[0].Pixels = image.Pt(320, 200)
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "backing-only rebuild", func() bool { return h.count() == 3 && e.a.View().Displays[0].PixelW == 320 })
	if e.a.View().Displays[0].W != 160 {
		t.Fatal("backing change shifted logical topology")
	}
}
