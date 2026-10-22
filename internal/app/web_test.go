package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
	"wpapp/internal/wpk"
)

type webTestRenderer struct {
	*fakeR
	lock       sync.Mutex
	item       render.Item
	properties string
}

func (r *webTestRenderer) Load(it render.Item, s desktop.Surface, p bool) error {
	r.lock.Lock()
	r.item = it
	r.lock.Unlock()
	return r.fakeR.Load(it, s, p)
}
func (r *webTestRenderer) UpdateProperties(v string) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.properties = v
	return nil
}
func (r *webTestRenderer) Capabilities() render.Capabilities { return render.Capabilities{FPS: true} }
func TestWebLivePropertiesRevokeAndPower(t *testing.T) {
	var mu sync.Mutex
	var rs []*webTestRenderer
	current := func() *webTestRenderer {
		mu.Lock()
		defer mu.Unlock()
		if len(rs) == 0 {
			return nil
		}
		return rs[len(rs)-1]
	}
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		it.Type = "web"
		it.NetworkGranted = true
		it.PropertyDefs = []wpk.Prop{{Key: "caption", Type: "text", Default: json.RawMessage(`"hello"`)}}
		d.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer {
			r := &webTestRenderer{fakeR: &fakeR{done: make(chan error, 1)}}
			mu.Lock()
			rs = append(rs, r)
			mu.Unlock()
			return r
		}
	})
	eventually(t, "web loaded", func() bool { return current() != nil && current().isLoaded() })
	old := current()
	e.a.SetItemProps("x", config.ItemProps{WebValues: `{"caption":"changed"}`})
	eventually(t, "live property", func() bool {
		old.lock.Lock()
		defer old.lock.Unlock()
		return old.properties == `{"caption":"changed"}`
	})
	if current() != old {
		t.Fatal("property update restarted page")
	}
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.BatteryPolicy = "fps15" })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true}
	eventually(t, "battery FPS", func() bool { return current().lastCap() == 15 })
	e.a.SetPausedAll(true)
	eventually(t, "web suspended", func() bool { return current().last() >= visibility.L1 })
	e.a.RevokeNetwork("x")
	eventually(t, "revoked host replaced", func() bool { return current() != old && current().isLoaded() })
	fresh := current()
	fresh.lock.Lock()
	granted := fresh.item.NetworkGranted
	fresh.lock.Unlock()
	if granted {
		t.Fatal("new host retained grant")
	}
	old.mu.Lock()
	closed := old.closed
	old.mu.Unlock()
	if !closed {
		t.Fatal("old host still running")
	}
	if e.a.View().Library[0].NetworkGranted {
		t.Fatal("library still granted")
	}
	if err := e.store.LibrarySaver.Flush(); err != nil {
		t.Fatal(err)
	}
}
func TestWebConsentCancelAndExplicitAnswer(t *testing.T) {
	e := setup(t, 0, false)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() { result <- e.a.RequestNetworkConsent(ctx, "web", []string{"example.com"}) }()
	eventually(t, "consent pending", func() bool { return e.a.View().NetworkConsent != nil })
	cancel()
	if <-result {
		t.Fatal("cancel granted permission")
	}
	eventually(t, "cancel clears dialog", func() bool { return e.a.View().NetworkConsent == nil })
	go func() { result <- e.a.RequestNetworkConsent(context.Background(), "web", nil) }()
	eventually(t, "second consent", func() bool { return e.a.View().NetworkConsent != nil })
	e.a.ResolveNetworkConsent(true)
	if !<-result {
		t.Fatal("explicit allow lost")
	}
}

func TestWebRendererLifetimeFollowsAssignment(t *testing.T) {
	var mu sync.Mutex
	var web *fakeR
	var calls []string
	e := setupWith(t, 1, false, func(d *Deps) {
		for _, kind := range []string{"web", "shader", "video", "image"} {
			d.Store.Library.Upsert(config.Item{ID: kind, Type: kind, Dir: t.TempDir(), Entry: "content/source"})
		}
		d.NewRenderer = func(kind string, _ config.Config, _ desktop.DisplayID) render.Renderer {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, kind)
			r := &fakeR{done: make(chan error, 1)}
			if kind == "web" {
				web = r
			}
			return r
		}
	})
	e.sync()
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 0 {
		t.Fatal("renderer created without assignment")
	}
	e.a.AssignAll("web")
	eventually(t, "web shown", func() bool { mu.Lock(); defer mu.Unlock(); return web != nil && web.isLoaded() })
	mu.Lock()
	old := web
	mu.Unlock()
	e.a.AssignAll("shader")
	eventually(t, "web closed on shader assignment", func() bool { old.mu.Lock(); defer old.mu.Unlock(); return old.closed })
	for _, kind := range []string{"video", "image"} {
		e.a.AssignAll(kind)
		e.sync()
	}
	e.a.AssignAll("")
	e.sync()
	mu.Lock()
	defer mu.Unlock()
	n = 0
	for _, kind := range calls {
		if kind == "web" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("unexpected web renderer creations", calls)
	}
}
