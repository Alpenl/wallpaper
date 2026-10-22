package app

import (
	"encoding/json"
	"sync"
	"testing"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
	"wpapp/internal/wpk"
)

type shaderTestRenderer struct {
	*optionRenderer
	properties string
}

func (r *shaderTestRenderer) UpdatePlayback(o render.PlaybackOptions) error { return r.Configure(o) }
func (r *shaderTestRenderer) UpdateProperties(raw string) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.properties = raw
	return nil
}
func TestShaderScaleFPSPropertiesAndBattery(t *testing.T) {
	var mu sync.Mutex
	var current *shaderTestRenderer
	creates := 0
	e := setupWith(t, 1, true, func(d *Deps) {
		it := d.Store.Library.Find("x")
		it.Type = "shader"
		it.PropertyDefs = []wpk.Prop{{Key: "enabled", Type: "bool", Default: json.RawMessage(`true`)}}
		d.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer {
			mu.Lock()
			defer mu.Unlock()
			creates++
			current = &shaderTestRenderer{optionRenderer: &optionRenderer{fakeR: &fakeR{done: make(chan error, 1)}}}
			return current
		}
	})
	get := func() *shaderTestRenderer { mu.Lock(); defer mu.Unlock(); return current }
	eventually(t, "shader started", func() bool { return get() != nil && get().isLoaded() })
	r := get()
	r.lock.Lock()
	scale := r.options.Scale
	r.lock.Unlock()
	if scale != .75 {
		t.Fatal("wrong default scale", scale)
	}
	e.a.SetItemProps("x", config.ItemProps{ShaderScale: .6, ShaderFPS: 60, WebValues: `{"enabled":false}`})
	eventually(t, "shader live options", func() bool {
		r.lock.Lock()
		defer r.lock.Unlock()
		return r.options.Scale == .6 && r.properties == `{"enabled":false}`
	})
	eventually(t, "item FPS override", func() bool { return r.lastCap() == 60 })
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.BatteryPolicy = "fps15" })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true}
	eventually(t, "battery caps shader", func() bool { return get().lastCap() == 15 })
	e.a.SetPausedAll(true)
	eventually(t, "shader paused", func() bool { return get().last() >= visibility.L1 })
	mu.Lock()
	n := creates
	mu.Unlock()
	if n != 1 {
		t.Fatal("live controls restarted shader", n)
	}
}
