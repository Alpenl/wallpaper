package shader

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/ipc"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

type pipeIO struct {
	io.ReadCloser
	io.WriteCloser
}

func (p pipeIO) Close() error { _ = p.ReadCloser.Close(); return p.WriteCloser.Close() }

type loadRequest struct {
	Preview bool
	Item    render.Item
	Native  uintptr
	Bounds  image.Rectangle
	Scale   float64
	Paused  bool
}
type Renderer struct {
	statsMu   sync.Mutex
	stats     render.Stats
	heartbeat time.Time
	mu        sync.Mutex
	exe       string
	scale     float64
	cmd       *exec.Cmd
	conn      *ipc.Conn
	exited    chan struct{}
	closing   bool
	done      chan error
}

func New(exe string) *Renderer { return &Renderer{exe: exe, done: make(chan error, 1)} }
func (r *Renderer) Capabilities() render.Capabilities {
	return render.Capabilities{FPS: runtime.GOOS != "linux", Scale: runtime.GOOS != "linux", Resume: runtime.GOOS != "linux"}
}
func (r *Renderer) Configure(o render.PlaybackOptions) error {
	if o.Scale == 0 {
		o.Scale = .75
	}
	o, e := render.NormalizePlaybackOptions(o)
	if e != nil {
		return e
	}
	if e := r.Capabilities().ValidateOptions(o); e != nil {
		return e
	}
	if o.Scale < .5 {
		return fmt.Errorf("shader: scale must be 0.5–1.0")
	}
	r.scale = o.Scale
	return nil
}
func (r *Renderer) UpdatePlayback(o render.PlaybackOptions) error {
	if e := r.Configure(o); e != nil {
		return e
	}
	return r.call("scale", o.Scale)
}
func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	if it.Kind != "shader" {
		return fmt.Errorf("shader: wrong item type %q", it.Kind)
	}
	if runtime.GOOS == "linux" {
		return fmt.Errorf("%w: shader wallpapers are unavailable on this platform", render.ErrUnsupported)
	}
	_ = r.Close()
	cmd := exec.Command(r.exe, "-shaderhost")
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return e
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return e
	}
	r.statsMu.Lock()
	r.stats = render.Stats{Backend: "shader", Pos: it.Start}
	r.heartbeat = time.Now()
	r.statsMu.Unlock()
	conn := ipc.New(pipeIO{out, in}, nil, func(method string, b json.RawMessage) {
		if method == "error" {
			var msg string
			if json.Unmarshal(b, &msg) == nil {
				r.statsMu.Lock()
				r.stats.LastErr = msg
				r.statsMu.Unlock()
				select {
				case r.done <- fmt.Errorf("shader: %s", msg):
				default:
				}
			}
		}
		if method == "stats" {
			var stats render.Stats
			if json.Unmarshal(b, &stats) == nil {
				r.statsMu.Lock()
				r.stats = stats
				r.heartbeat = time.Now()
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
		conn.Close()
		r.mu.Lock()
		intentional := r.closing
		r.mu.Unlock()
		if !intentional {
			select {
			case r.done <- fmt.Errorf("shader host exited: %v", err):
			default:
			}
		}
		close(exited)
	}()
	if e = r.call("load", loadRequest{Item: it, Native: s.Native(), Bounds: s.Bounds(), Paused: paused, Scale: r.scale}); e != nil {
		_ = r.Close()
		return e
	}
	r.statsMu.Lock()
	r.heartbeat = time.Now()
	r.statsMu.Unlock()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-exited:
				return
			case <-ticker.C:
				r.statsMu.Lock()
				last := r.heartbeat
				r.statsMu.Unlock()
				if time.Since(last) > 5*time.Second {
					select {
					case r.done <- fmt.Errorf("shader: GPU host stopped responding"):
					default:
					}
					_ = cmd.Process.Kill()
					return
				}
			}
		}
	}()
	return nil
}
func (r *Renderer) call(method string, v any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("shader: host not running")
	}
	e := r.conn.Call(method, v, 20*time.Second, nil)
	if e != nil {
		_ = r.cmd.Process.Kill()
	}
	return e
}
func (r *Renderer) SetLevel(l visibility.Level) error {
	if l < visibility.L0 || l > visibility.L3 {
		return fmt.Errorf("shader: invalid level")
	}
	return r.call("pause", int(l))
}
func (r *Renderer) SetFPSCap(fps int) error {
	if fps < 0 || fps > 240 {
		return fmt.Errorf("shader: invalid FPS cap")
	}
	return r.call("fps", fps)
}
func (r *Renderer) UpdateProperties(v string) error { return r.call("properties", v) }
func (r *Renderer) Snapshot(string) error           { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats             { r.statsMu.Lock(); defer r.statsMu.Unlock(); return r.stats }
func (r *Renderer) Done() <-chan error              { return r.done }
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

func (r *Renderer) BlockSignals(blocked bool) error { return r.call("signals-blocked", blocked) }
