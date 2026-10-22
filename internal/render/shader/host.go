package shader

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"wpapp/internal/ipc"
	"wpapp/internal/reactive"
	"wpapp/internal/render"
	"wpapp/internal/wpk"
)

// gpu is owned exclusively by the renderer event thread. Fakes exercise policy
// and lifecycle without touching platform APIs.
type gpu interface {
	Load(loadRequest, string) error
	Frame(*Uniforms, float64) (float64, error)
	Pump()
	Close()
}
type hostSession struct {
	signals        *reactive.Session
	signalsBlocked bool
	gpu            gpu
	loaded         bool
	defs           []wpk.Prop
	uniforms       Uniforms
	timeline       Timeline
	adaptive       Adaptive
	fps            int
	width, height  int
	paused         bool
}

func readSource(x loadRequest) (*wpk.Manifest, string, error) {
	if x.Item.FPSCap < 0 || x.Item.FPSCap > 240 {
		return nil, "", fmt.Errorf("shader: invalid FPS cap")
	}
	if x.Item.Kind != "shader" || !filepath.IsAbs(x.Item.PackageDir) || x.Bounds.Dx() < 1 || x.Bounds.Dy() < 1 || x.Bounds.Dx() > 16384 || x.Bounds.Dy() > 16384 {
		return nil, "", fmt.Errorf("shader: invalid load request")
	}
	m, e := wpk.LoadDir(x.Item.PackageDir)
	if e != nil {
		return nil, "", e
	}
	if m.Type != "shader" || m.ID != x.Item.ID || filepath.Clean(x.Item.Path) != filepath.Join(x.Item.PackageDir, filepath.FromSlash(m.Entry)) {
		return nil, "", fmt.Errorf("shader: package identity mismatch")
	}
	root, e := os.OpenRoot(x.Item.PackageDir)
	if e != nil {
		return nil, "", e
	}
	defer root.Close()
	f, e := root.Open(filepath.FromSlash(m.Entry))
	if e != nil {
		return nil, "", e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return nil, "", e
	}
	if len(b) > 1<<20 {
		return nil, "", fmt.Errorf("shader line 1: source exceeds 1 MiB")
	}
	return m, string(b), nil
}
func (h *hostSession) control(method string, p json.RawMessage) error {
	if method == "load" {
		if h.loaded {
			return fmt.Errorf("shader: already loaded")
		}
		var x loadRequest
		if e := json.Unmarshal(p, &x); e != nil {
			return e
		}
		if math.IsNaN(x.Item.Start) || math.IsInf(x.Item.Start, 0) || x.Item.Start < 0 || x.Item.Start > 1e9 {
			return fmt.Errorf("shader: invalid timeline position")
		}
		m, source, e := readSource(x)
		if e != nil {
			return e
		}
		target := MSL
		if runtime.GOOS == "windows" {
			target = HLSL
		}
		code, e := TranslateProperties(source, target, m.Properties)
		if e != nil {
			return e
		}
		if e = h.uniforms.Properties(m.Properties, x.Item.Properties); e != nil {
			return e
		}
		if x.Scale == 0 {
			x.Scale = .75
		}
		if x.Scale < .5 || x.Scale > 1 {
			return fmt.Errorf("shader: scale out of range")
		}
		if e = h.gpu.Load(x, code); e != nil {
			return e
		}
		h.signals = reactive.NewSession()
		h.signals.Audio = !x.Preview && x.Item.AudioEnabled && m.Declares("audio")
		h.signals.Input = !x.Preview && x.Item.InputEnabled && m.Declares("input")
		h.signals.Canvas = x.Bounds
		h.signals.Viewport = x.Item.Viewport
		h.signalsBlocked = x.Item.SignalsBlocked
		h.signals.SetPaused(x.Paused || h.signalsBlocked)
		h.loaded = true
		h.defs = m.Properties
		h.adaptive = NewAdaptive(x.Scale)
		h.width = x.Bounds.Dx()
		h.height = x.Bounds.Dy()
		if size, ok := h.gpu.(interface{ RasterSize() (int, int) }); ok {
			h.width, h.height = size.RasterSize()
		}
		h.paused = x.Paused
		h.timeline.elapsed = time.Duration(x.Item.Start * float64(time.Second))
		h.timeline.Pause(x.Paused)
		h.fps = effectiveFPS(x.Item.FPSCap)
		return nil
	}
	if !h.loaded {
		return fmt.Errorf("shader: load required")
	}
	switch method {
	case "signals-blocked":
		var blocked bool
		if e := json.Unmarshal(p, &blocked); e != nil {
			return e
		}
		h.signalsBlocked = blocked
		if h.signals != nil {
			h.signals.SetPaused(h.paused || blocked)
		}
	case "pause":
		var l int
		if e := json.Unmarshal(p, &l); e != nil {
			return e
		}
		if l < 0 || l > 3 {
			return fmt.Errorf("shader: invalid pause level")
		}
		h.paused = l > 0
		h.timeline.Pause(h.paused)
		if h.signals != nil {
			h.signals.SetPaused(h.paused || h.signalsBlocked)
		}
	case "fps":
		var fps int
		if e := json.Unmarshal(p, &fps); e != nil {
			return e
		}
		if fps < 0 || fps > 240 {
			return fmt.Errorf("shader: invalid FPS")
		}
		h.fps = effectiveFPS(fps)
	case "scale":
		var scale float64
		if e := json.Unmarshal(p, &scale); e != nil {
			return e
		}
		if scale < .5 || scale > 1 {
			return fmt.Errorf("shader: invalid scale")
		}
		h.adaptive = NewAdaptive(scale)
	case "properties":
		var raw string
		if e := json.Unmarshal(p, &raw); e != nil {
			return e
		}
		return h.uniforms.Properties(h.defs, raw)
	default:
		return fmt.Errorf("shader: unknown method %s", method)
	}
	return nil
}
func effectiveFPS(cap int) int {
	if cap <= 0 {
		return 30
	}
	return min(cap, 60)
}
func (h *hostSession) frame(now time.Time) error {
	if !h.loaded || h.paused {
		return nil
	}
	seconds, delta := h.timeline.Tick(now)
	h.uniforms.Frame(max(1, int(float64(h.width)*h.adaptive.Scale)), max(1, int(float64(h.height)*h.adaptive.Scale)), seconds, delta, h.timeline.Frame-1, now)
	if h.signals != nil {
		spectrum, mouse := h.signals.Frame(now, h.fps, image.Pt(max(1, int(float64(h.width)*h.adaptive.Scale)), max(1, int(float64(h.height)*h.adaptive.Scale))))
		h.uniforms.Signals(spectrum, mouse)
	}
	ms, e := h.gpu.Frame(&h.uniforms, h.adaptive.Scale)
	if e == nil {
		h.adaptive.Sample(ms)
	}
	return e
}
func HostMain() int {
	if e := runHost(pipeIO{os.Stdin, os.Stdout}); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	return 0
}
func serve(p pipeIO, g gpu) error {
	defer g.Close()
	h := hostSession{gpu: g, fps: 30}
	defer func() {
		if h.signals != nil {
			h.signals.Close()
		}
	}()
	jobs := make(chan func())
	c := ipc.New(p, func(method string, b json.RawMessage) (any, *ipc.Error) {
		done := make(chan struct{})
		var e error
		select {
		case jobs <- func() { e = h.control(method, b); close(done) }:
		case <-time.After(10 * time.Second):
			return nil, &ipc.Error{Code: "E_TIMEOUT", Msg: "shader event thread unavailable", Fatal: true}
		}
		<-done
		if e != nil {
			return nil, &ipc.Error{Code: "E_GPU", Msg: e.Error()}
		}
		return controlReply(method), nil
	}, nil)
	defer c.Close()
	timer := time.NewTimer(time.Second / 30)
	defer timer.Stop()
	statsTicker := time.NewTicker(time.Second)
	defer statsTicker.Stop()
	for {
		select {
		case <-c.Done():
			return nil
		case job := <-jobs:
			job()
		case <-statsTicker.C:
			if h.loaded {
				status := ""
				if h.signals != nil {
					status = h.signals.Status
				}
				_ = c.Emit("stats", render.Stats{Backend: "shader", Pos: h.timeline.elapsed.Seconds(), LastErr: status})
			}
		case <-timer.C:
			now := time.Now()
			g.Pump()
			if e := h.frame(now); e != nil {
				_ = c.Emit("error", e.Error())
				return e
			}
			interval := time.Second / time.Duration(h.fps)
			if !h.loaded || h.paused {
				interval = time.Second / 10
			}
			timer.Reset(max(time.Millisecond, interval-time.Since(now)))
		}
	}
}

func controlReply(method string) any {
	if method == "load" {
		return render.Identity()
	}
	return struct{}{}
}
