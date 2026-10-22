package app

import (
	"context"
	"sync"
	"testing"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

type signalRenderer struct {
	*webTestRenderer
	blocked bool
}

func (r *signalRenderer) BlockSignals(v bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.blocked = v
	return nil
}
func TestSignalMasterRevokeAndFullscreen(t *testing.T) {
	var mu sync.Mutex
	var rs []*signalRenderer
	current := func() *signalRenderer {
		mu.Lock()
		defer mu.Unlock()
		if len(rs) == 0 {
			return nil
		}
		return rs[len(rs)-1]
	}
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		it.Type = "shader"
		it.AudioGranted = true
		it.InputGranted = true
		it.Permissions = []string{"audio", "input"}
		d.Store.Config.Pause.OnFullscreen = false
		d.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer {
			r := &signalRenderer{webTestRenderer: &webTestRenderer{fakeR: &fakeR{done: make(chan error, 1)}}}
			mu.Lock()
			rs = append(rs, r)
			mu.Unlock()
			return r
		}
	})
	eventually(t, "loaded", func() bool { return current() != nil && current().isLoaded() })
	old := current()
	old.lock.Lock()
	it := old.item
	old.lock.Unlock()
	if it.AudioEnabled || it.InputEnabled {
		t.Fatal("master default", it)
	}
	e.a.UpdateConfig(func(c *config.Config) { c.Privacy.AudioReactive = true; c.Privacy.MouseInput = true })
	eventually(t, "master reload", func() bool { return current() != old && current().isLoaded() })
	active := current()
	active.lock.Lock()
	it = active.item
	active.lock.Unlock()
	if !it.AudioEnabled || !it.InputEnabled {
		t.Fatal("granted master not passed", it)
	}
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: e.h.displays[0].ID, Reason: visibility.Fullscreen, Active: true}
	eventually(t, "privacy block even with fullscreen pause disabled", func() bool { active.lock.Lock(); defer active.lock.Unlock(); return active.blocked })
	e.a.RevokeSignal("x", "audio")
	eventually(t, "revoke reload", func() bool { return current() != active && current().isLoaded() })
	r := current()
	r.lock.Lock()
	it = r.item
	r.lock.Unlock()
	if it.AudioEnabled || !it.InputEnabled || !it.SignalsBlocked {
		t.Fatal("revocation or raw fullscreen gate", it)
	}
	e.a.UpdateConfig(func(c *config.Config) { c.Privacy.MouseInput = false })
	eventually(t, "master off reload", func() bool { return current() != r && current().isLoaded() })
	r = current()
	r.lock.Lock()
	it = r.item
	r.lock.Unlock()
	if it.InputEnabled {
		t.Fatal("master off ignored")
	}
}
func TestSignalConsentCancellation(t *testing.T) {
	e := setup(t, 0, false)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() { result <- e.a.RequestSignalConsent(ctx, "test", "audio") }()
	eventually(t, "dialog", func() bool { return e.a.View().SignalConsent != nil })
	cancel()
	if <-result {
		t.Fatal("cancel grants")
	}
	eventually(t, "cleared", func() bool { return e.a.View().SignalConsent == nil })
	go func() { result <- e.a.RequestSignalConsent(context.Background(), "test", "input") }()
	eventually(t, "dialog", func() bool { return e.a.View().SignalConsent != nil })
	e.a.ResolveSignalConsent(true)
	if !<-result {
		t.Fatal("allow lost")
	}
}
