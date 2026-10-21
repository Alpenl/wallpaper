//go:build windows

package mf

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/ipc"
	"wpapp/internal/platform/win32"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

// Renderer 是主进程一侧：启动 `<本程序> -mfhost -pipe <名字>` 子进程，用 IPC v1 控制。
type Renderer struct {
	Exe     string // 子进程可执行文件（通常是 os.Executable()）
	LogPath string
	Swap    string

	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *ipc.Conn
	killing *atomic.Bool
	done    chan error
	stats   atomic.Value // render.Stats
	level   visibility.Level
}

// New 创建 MF 渲染器。
func New(exe, logPath string) *Renderer {
	r := &Renderer{Exe: exe, LogPath: logPath, Swap: "flip", done: make(chan error, 1)}
	r.stats.Store(render.Stats{Backend: "mf"})
	return r
}

func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	r.Close()
	ps, err := win32.NewPipeServer()
	if err != nil {
		return err
	}
	args := []string{"-mfhost", "-pipe", ps.Name, "-swap", r.Swap}
	if r.LogPath != "" {
		args = append(args, "-log", r.LogPath)
	}
	cmd := exec.Command(r.Exe, args...)
	if err := cmd.Start(); err != nil {
		ps.Close()
		return fmt.Errorf("mfhost start: %w", err)
	}
	f, err := ps.Accept(5 * time.Second)
	if err != nil {
		cmd.Process.Kill()
		return err
	}
	killing := &atomic.Bool{}
	conn := ipc.New(f, nil, func(m string, p json.RawMessage) {
		switch m {
		case "stats":
			var st struct {
				Dropped   uint64 `json:"dropped"`
				Loops     int64  `json:"loops"`
				Presented int64  `json:"presented"`
			}
			_ = json.Unmarshal(p, &st)
			r.stats.Store(render.Stats{Backend: "mf", HWDec: "mf", Dropped: st.Dropped, Loops: st.Loops})
		case "error":
			var e ipc.Error
			_ = json.Unmarshal(p, &e)
			s := r.Stats()
			s.LastErr = e.Error()
			r.stats.Store(s)
		}
	})
	var hello struct{ Pid int }
	if err := conn.Call("hello", map[string]any{"proto": 1, "app": "wallpaperd"}, 5*time.Second, &hello); err != nil {
		cmd.Process.Kill()
		conn.Close()
		return fmt.Errorf("mfhost hello: %w", err)
	}
	b := s.Bounds()
	load := map[string]any{"entry": it.Path, "wid": uint64(s.Native()), "bounds": map[string]int{"w": b.Dx(), "h": b.Dy()}, "paused": paused}
	if err := conn.Call("load", load, 15*time.Second, nil); err != nil {
		cmd.Process.Kill()
		conn.Close()
		return fmt.Errorf("mfhost load: %w", err)
	}
	r.mu.Lock()
	r.cmd, r.conn, r.killing = cmd, conn, killing
	r.level = visibility.L0
	if paused {
		r.level = visibility.L1
	}
	r.mu.Unlock()
	go func() {
		err := cmd.Wait()
		conn.Close()
		if !killing.Load() {
			select {
			case r.done <- fmt.Errorf("mfhost exited: %v", err):
			default:
			}
		}
	}()
	return nil
}

func (r *Renderer) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	c := r.conn
	r.level = l
	r.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mf: not running")
	}
	lv := int(l)
	if l == visibility.L2 {
		lv = 1
	}
	return c.Call("setPause", map[string]int{"level": lv}, 15*time.Second, nil)
}

func (r *Renderer) SetFPSCap(int) error   { return render.ErrUnsupported }
func (r *Renderer) Snapshot(string) error { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats   { return r.stats.Load().(render.Stats) }
func (r *Renderer) Done() <-chan error    { return r.done }

// Close 有意结束：shutdown → 1 s 内不退出就 Kill（02 章：宿主 2 s 内没退出就 TerminateProcess）。
func (r *Renderer) Close() error {
	r.mu.Lock()
	cmd, c, k := r.cmd, r.conn, r.killing
	r.cmd, r.conn = nil, nil
	r.mu.Unlock()
	if cmd == nil {
		return nil
	}
	k.Store(true)
	_ = c.Call("shutdown", nil, 500*time.Millisecond, nil)
	done := make(chan struct{})
	go func() { cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
	}
	c.Close()
	return nil
}

var _ render.Renderer = (*Renderer)(nil)

// HostMain 是 -mfhost 子进程入口。
func HostMain(pipe, swap, logPath string) int {
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			Logf = func(format string, a ...any) {
				fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339Nano), fmt.Sprintf(format, a...))
			}
		}
	}
	if err := RunHost(pipe, swap, 5*time.Second); err != nil {
		Logf("%v", err)
		return 2
	}
	return 0
}
