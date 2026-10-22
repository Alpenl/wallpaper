package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/ipc"
	"wpapp/internal/reactive"
	"wpapp/internal/render"
	"wpapp/internal/telemetry"
	"wpapp/internal/visibility"
)

type pipeIO struct {
	io.ReadCloser
	io.WriteCloser
}

func (p pipeIO) Close() error { _ = p.ReadCloser.Close(); return p.WriteCloser.Close() }

type loadRequest struct {
	Token  string `json:"-"`
	Item   render.Item
	Native uintptr
	Bounds image.Rectangle
	Paused bool
}
type Renderer struct {
	processPID          atomic.Int64
	Log                 render.LogFunc
	Display, StderrPath string
	statsMu             sync.Mutex
	signalStatus        string
	mu                  sync.Mutex
	exe                 string
	command             func() *exec.Cmd
	cmd                 *exec.Cmd
	conn                *ipc.Conn
	exited              chan struct{}
	closing             bool
	done                chan error
}

func New(exe string) *Renderer { return &Renderer{exe: exe, done: make(chan error, 1)} }
func (r *Renderer) Capabilities() render.Capabilities {
	return render.Capabilities{FPS: runtime.GOOS != "linux"}
}
func (r *Renderer) Configure(o render.PlaybackOptions) error {
	o, e := render.NormalizePlaybackOptions(o)
	if e != nil {
		return e
	}
	return r.Capabilities().ValidateOptions(o)
}
func (r *Renderer) UpdatePlayback(o render.PlaybackOptions) error { return r.Configure(o) }
func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	if it.Kind != "web" {
		return fmt.Errorf("web: wrong item type %q", it.Kind)
	}
	if runtime.GOOS == "linux" {
		return fmt.Errorf("%w: web wallpapers are unavailable on Linux", render.ErrUnsupported)
	}
	_ = r.Close()
	cmd := exec.Command(r.exe, "-webhost")
	if r.command != nil {
		cmd = r.command()
	}
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return e
	}
	var stderr *telemetry.ChildLog
	if r.StderrPath != "" {
		stderr, e = telemetry.NewChildLog(r.StderrPath)
		if e != nil {
			in.Close()
			out.Close()
			return e
		}
		cmd.Stderr = stderr
	}
	finish := func(err error) {
		if stderr != nil {
			logErr := stderr.Close()
			r.Log.Log("child_log", "display", r.Display, "err", logErr, "dropped", stderr.Dropped())
		}
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		r.Log.Log("child_exit", "role", "web", "display", r.Display, "child_pid", cmd.Process.Pid, "code", code, "err", err)
	}
	if e = cmd.Start(); e != nil {
		if stderr != nil {
			_ = stderr.Close()
		}
		in.Close()
		out.Close()
		return e
	}
	r.processPID.Store(int64(cmd.Process.Pid))
	r.Log.Log("child_start", "role", "web", "display", r.Display, "child_pid", cmd.Process.Pid)
	r.statsMu.Lock()
	r.signalStatus = ""
	r.statsMu.Unlock()
	conn := ipc.New(pipeIO{out, in}, nil, func(method string, b json.RawMessage) {
		if method == "signal-status" {
			var status string
			if json.Unmarshal(b, &status) == nil {
				r.statsMu.Lock()
				r.signalStatus = status
				r.statsMu.Unlock()
			}
		}
	})
	r.mu.Lock()
	r.cmd = cmd
	r.conn = conn
	r.closing = false
	r.exited = make(chan struct{})
	exited := r.exited
	r.mu.Unlock()
	go func() {
		err := cmd.Wait()
		finish(err)
		conn.Close()
		r.mu.Lock()
		intentional := r.closing
		r.mu.Unlock()
		if !intentional {
			select {
			case r.done <- fmt.Errorf("web host exited: %v", err):
			default:
			}
		}
		close(exited)
	}()
	if e = r.call("load", loadRequest{Item: it, Native: s.Native(), Bounds: s.Bounds(), Paused: paused}); e != nil {
		_ = r.Close()
		return e
	}
	return nil
}
func (r *Renderer) call(method string, v any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("web: host not running")
	}
	var reply render.HelperIdentity
	var out any
	if method == "load" {
		out = &reply
	}
	e := r.conn.Call(method, v, 20*time.Second, out)
	if method == "load" && e == nil {
		r.Log.Log("helper_identity", "display", r.Display, "child_pid", reply.PID, "build", reply.Build)
	}
	if e != nil {
		_ = r.cmd.Process.Kill()
	}
	return e
}
func (r *Renderer) SetLevel(l visibility.Level) error {
	if l < visibility.L0 || l > visibility.L3 {
		return fmt.Errorf("web: invalid level")
	}
	return r.call("pause", int(l))
}
func (r *Renderer) SetFPSCap(fps int) error {
	if _, e := FPSScript(fps); e != nil {
		return e
	}
	return r.call("fps", fps)
}
func (r *Renderer) UpdateProperties(v string) error { return r.call("properties", v) }
func (r *Renderer) Snapshot(string) error           { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	return render.Stats{Backend: "web", HWDec: "unknown", LastErr: r.signalStatus}
}
func (r *Renderer) Done() <-chan error { return r.done }
func (r *Renderer) Close() error {
	r.mu.Lock()
	cmd, c, exited := r.cmd, r.conn, r.exited
	r.closing = true
	r.cmd = nil
	r.conn = nil
	r.mu.Unlock()
	if cmd == nil {
		return nil
	}
	_ = c.Close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}
	return nil
}

// Native APIs are confined to the host's event thread behind this small seam.
type nativeView interface {
	Load(loadRequest) error
	Pause(bool) error
	Script(string) error
	Properties(string) error
	FPS(int) error
	Close()
}
type hostSession struct {
	reportedStatus  string
	signals         *reactive.Session
	paused, blocked bool
	fps             int
	limiter         reactive.Limiter
	raster          image.Point
	view            nativeView
	manifestFPS     int
	defsScript      func(string) (string, error)
	loaded          bool
}

func (h *hostSession) handle(method string, p json.RawMessage) (any, *ipc.Error) {
	err := h.control(method, p)
	if err != nil {
		return nil, &ipc.Error{Code: "E_LOAD", Msg: err.Error()}
	}
	if method == "load" {
		return render.Identity(), nil
	}
	return struct{}{}, nil
}
func (h *hostSession) control(method string, p json.RawMessage) error {
	if method == "load" {
		if h.loaded {
			return fmt.Errorf("already loaded")
		}
		var x loadRequest
		if e := json.Unmarshal(p, &x); e != nil {
			return e
		}
		m, e := validateLoad(x)
		if e != nil {
			return e
		}
		var token [32]byte
		if _, e := rand.Read(token[:]); e != nil {
			return e
		}
		x.Token = hex.EncodeToString(token[:])
		h.manifestFPS = m.Web.FPSCap
		h.defsScript = func(raw string) (string, error) { return PropertyScript(m.Properties, raw) }
		if e = h.view.Load(x); e != nil {
			return e
		}
		h.signals = reactive.NewSession()
		h.signals.Audio = x.Item.AudioEnabled && m.Declares("audio")
		h.signals.Input = x.Item.InputEnabled && m.Declares("input")
		h.signals.Canvas, h.signals.Viewport = x.Bounds, x.Item.Viewport
		h.raster = x.Bounds.Size()
		h.fps = EffectiveFPS(m.Web.FPSCap, x.Item.FPSCap)
		h.paused, h.blocked = x.Paused, x.Item.SignalsBlocked
		h.signals.SetPaused(h.paused || h.blocked)
		h.loaded = true
		return nil
	}
	if !h.loaded {
		return fmt.Errorf("web: load required")
	}
	switch method {
	case "signals-blocked":
		if e := json.Unmarshal(p, &h.blocked); e != nil {
			return e
		}
		if h.signals != nil {
			h.signals.SetPaused(h.paused || h.blocked)
		}
		return nil
	case "pause":
		var l int
		if e := json.Unmarshal(p, &l); e != nil {
			return e
		}
		if l < 0 || l > 3 {
			return fmt.Errorf("invalid level")
		}
		h.paused = l > 0
		if h.signals != nil {
			h.signals.SetPaused(h.paused || h.blocked)
		}
		return h.view.Pause(l > 0)
	case "fps":
		var fps int
		if e := json.Unmarshal(p, &fps); e != nil {
			return e
		}
		if fps < 0 || fps > 240 {
			return fmt.Errorf("invalid FPS")
		}
		h.fps = EffectiveFPS(h.manifestFPS, fps)
		return h.view.FPS(h.fps)
	case "properties":
		var raw string
		if e := json.Unmarshal(p, &raw); e != nil {
			return e
		}
		s, e := h.defsScript(raw)
		if e != nil {
			return e
		}
		return h.view.Properties(s)
	default:
		return fmt.Errorf("web: unknown method %q", method)
	}
}

func (r *Renderer) ChildPID() int { return int(r.processPID.Load()) }
