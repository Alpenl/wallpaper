package web

import (
	"encoding/json"
	"image"
	"strings"
	"testing"
	"time"
	"wpapp/internal/reactive"
)

type audioFake struct{ starts, stops, reads int }

func (a *audioFake) Start() error { a.starts++; return nil }
func (a *audioFake) Stop()        { a.stops++ }
func (a *audioFake) Read() ([]float32, int, error) {
	a.reads++
	return make([]float32, 256), 48000, nil
}

type pointerFake struct {
	reads   int
	desktop bool
}

func (p *pointerFake) Start() error { return nil }
func (p *pointerFake) Stop()        {}
func (p *pointerFake) Sample() reactive.PointerSample {
	p.reads++
	return reactive.PointerSample{X: 10, Y: 20, Buttons: 1, Desktop: p.desktop}
}
func TestForwardingStopsOnPauseAndFullscreen(t *testing.T) {
	for _, method := range []string{"pause", "signals-blocked"} {
		t.Run(method, func(t *testing.T) {
			a, p := &audioFake{}, &pointerFake{desktop: true}
			f := &fakeView{}
			signals := &reactive.Session{Audio: true, Input: true, Capture: reactive.Capture{Source: a}, Pointer: p, Canvas: image.Rect(0, 0, 100, 100), Viewport: image.Rect(0, 0, 100, 100)}
			h := hostSession{view: f, loaded: true, signals: signals, fps: 30, raster: image.Pt(100, 100)}
			now := time.Now()
			h.signalFrame(now)
			if len(f.calls) != 2 || !strings.Contains(f.calls[0], "new Float32Array(") || !strings.Contains(f.calls[1], `{"x":10,"y":20,"buttons":1}`) {
				t.Fatal(f.calls)
			}
			h.signalFrame(now)
			if len(f.calls) != 2 {
				t.Fatal("rate limit")
			}
			param := `true`
			if method == "pause" {
				param = `1`
			}
			if e := h.control(method, json.RawMessage(param)); e != nil {
				t.Fatal(e)
			}
			n := len(f.calls)
			h.signalFrame(now.Add(time.Second))
			if len(f.calls) != n || a.reads != 1 || p.reads != 1 || a.stops != 1 {
				t.Fatal("forwarded while blocked", f.calls, a, p)
			}
			h.closeSignals()
		})
	}
}
