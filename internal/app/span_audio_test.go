package app

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
)

func TestSpanningAudioPrimaryNotLeftmost(t *testing.T) {
	var mu sync.Mutex
	rs := map[desktop.DisplayID]*optionRenderer{}
	setupWith(t, 2, true, func(d *Deps) {
		h := &canvasHost{fakeHost: d.Host.(*fakeHost), canvases: map[desktop.DisplayID]image.Rectangle{}}
		h.displays[0].Primary = false
		h.displays[1].Primary = true
		d.Host = h
		d.Store.Assign.Layout = "span"
		d.Store.Assign.SpanItem = "x"
		d.Store.Config.Render.Muted = false
		d.Store.Library.Items[0].Snapshot = ""
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			r := &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}}
			mu.Lock()
			rs[id] = r
			mu.Unlock()
			return r
		}
	})
	eventually(t, "both configured", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return rs["A"] != nil && rs["B"] != nil && rs["A"].isLoaded() && rs["B"].isLoaded()
	})
	mu.Lock()
	defer mu.Unlock()
	a, b := rs["A"], rs["B"]
	a.lock.Lock()
	defer a.lock.Unlock()
	b.lock.Lock()
	defer b.lock.Unlock()
	if !a.options.Muted || b.options.Muted {
		t.Fatalf("secondary A muted=%v, primary B muted=%v", a.options.Muted, b.options.Muted)
	}
}
func TestSpanTeardownPreservesPrimaryResume(t *testing.T) {
	for trial := 0; trial < 24; trial++ {
		t.Run(fmt.Sprint(trial), func(t *testing.T) {
			var mu sync.Mutex
			rs := map[desktop.DisplayID]*optionRenderer{}
			e := setupWith(t, 2, true, func(d *Deps) {
				h := &canvasHost{fakeHost: d.Host.(*fakeHost), canvases: map[desktop.DisplayID]image.Rectangle{}}
				d.Host = h
				d.Store.Assign.Layout = "span"
				d.Store.Assign.SpanItem = "x"
				d.Store.Library.Items[0].Snapshot = ""
				d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
					pos := 40.
					if id == "B" {
						pos = 10
					}
					r := &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}, pos: pos}
					mu.Lock()
					rs[id] = r
					mu.Unlock()
					return r
				}
			})
			eventually(t, "both loaded", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return rs["A"] != nil && rs["B"] != nil && rs["A"].isLoaded() && rs["B"].isLoaded()
			})
			barrier := make(chan struct{})
			e.a.Do(func() { close(barrier) })
			<-barrier
			e.a.SetLayout("independent", "")
			eventually(t, "independent", func() bool { return e.a.View().Layout == "independent" })
			got := make(chan float64, 1)
			e.a.Do(func() { got <- e.store.State.Positions["@span|x"] })
			if p := <-got; p != 40 {
				t.Fatalf("primary position 40 replaced by secondary %v", p)
			}
		})
	}
}

func TestSpanOwnerRemovalWithUnchangedCanvasRefreshesAudioAndResume(t *testing.T) {
	var mu sync.Mutex
	rs := map[desktop.DisplayID]*optionRenderer{}
	var h *canvasHost
	e := setupWith(t, 2, true, func(d *Deps) {
		h = &canvasHost{fakeHost: d.Host.(*fakeHost), canvases: map[desktop.DisplayID]image.Rectangle{}}
		h.displays[0].Bounds = image.Rect(0, 0, 100, 100)
		h.displays[1].Bounds = image.Rect(0, 0, 200, 100)
		h.displays[0].Primary, h.displays[1].Primary = true, false
		d.Host = h
		d.Store.Assign.Layout, d.Store.Assign.SpanItem = "span", "x"
		d.Store.Config.Render.Muted = false
		d.Store.Library.Items[0].Snapshot = ""
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			pos := 10.0
			if id == "A" {
				pos = 40
			}
			r := &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}, pos: pos}
			mu.Lock()
			rs[id] = r
			mu.Unlock()
			return r
		}
	})
	current := func(id desktop.DisplayID) *optionRenderer { mu.Lock(); defer mu.Unlock(); return rs[id] }
	eventually(t, "initial span loaded", func() bool {
		return current("A") != nil && current("B") != nil && current("A").isLoaded() && current("B").isLoaded()
	})
	old := current("B")
	h.mu.Lock()
	h.displays = h.displays[1:]
	h.mu.Unlock()
	h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "remaining owner rebuilt", func() bool { return len(e.a.View().Displays) == 1 && current("B") != old && current("B").isLoaded() })
	r := current("B")
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.options.Muted || r.start != 40 {
		t.Fatalf("new owner muted=%v, resume=%v", r.options.Muted, r.start)
	}
}

// M-16: View's Effective.Muted reflects forced mute on secondary spanning screens,
// and spanning static system wallpaper respects ItemProps.Fit and refreshes on SetItemProps.
func TestSpanningEffectiveMuteAndItemPropsFitRefresh(t *testing.T) {
	e := setupWith(t, 2, true, func(d *Deps) {
		h := &canvasHost{fakeHost: d.Host.(*fakeHost), canvases: map[desktop.DisplayID]image.Rectangle{}}
		h.displays[0].Primary = true
		h.displays[1].Primary = false
		d.Host = h
		d.Store.Assign.Layout = "span"
		d.Store.Assign.SpanItem = "x"
		d.Store.Config.Render.Muted = false
		d.Store.Library.Items[0].Snapshot = ""
	})
	eventually(t, "span view ready", func() bool {
		v := e.a.View()
		return v.Layout == "span" && len(v.Displays) == 2
	})
	v := e.a.View()
	var primaryMuted, secondaryMuted bool
	for _, d := range v.Displays {
		if d.Primary {
			primaryMuted = d.Effective.Muted
		} else {
			secondaryMuted = d.Effective.Muted
		}
	}
	if primaryMuted || !secondaryMuted {
		t.Fatalf("expected primary muted=false, secondary muted=true; got primary=%v secondary=%v", primaryMuted, secondaryMuted)
	}

	// Verify SetItemProps changing Fit updates effective Fit and refreshes spanning system wallpaper.
	snapPath := writeSpanTestPNG(t, e.store.Paths.Cache)
	e.a.Do(func() {
		it := e.store.Library.Find("x")
		it.Snapshot = snapPath
		for _, s := range e.a.screens {
			if s.item != nil && s.item.ID == "x" {
				s.item.Snapshot = snapPath
			}
		}
	})
	e.sync()
	h := e.a.d.Host.(*canvasHost)
	e.a.SetItemProps("x", config.ItemProps{Fit: "fit"})
	eventually(t, "spanning system wallpaper refreshed with item Fit", func() bool {
		v := e.a.View()
		if len(v.Displays) != 2 || v.Displays[0].Effective.Fit != "fit" {
			return false
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.wall["A"] != "" && h.wall["B"] != ""
	})
}

func writeSpanTestPNG(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "span-snap.png")
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, im); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
