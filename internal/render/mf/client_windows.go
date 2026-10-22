//go:build windows

package mf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/ipc"
	"wpapp/internal/platform/win32"
	"wpapp/internal/render"
	"wpapp/internal/telemetry"
	"wpapp/internal/visibility"
)

// Renderer 是主进程一侧：启动 `<本程序> -mfhost -pipe <名字>` 子进程，用 IPC v1 控制。
type Renderer struct {
	processPID atomic.Int64
	Log        render.LogFunc
	Display    string
	Exe        string // 子进程可执行文件（通常是 os.Executable()）
	LogPath    string
	Swap       string

	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *ipc.Conn
	killing *atomic.Bool
	done    chan error
	exited  chan struct{} // closed by the sole cmd.Wait owner
	stats   atomic.Value  // render.Stats
	level   visibility.Level
	options render.PlaybackOptions
}

// New 创建 MF 渲染器。
func New(exe, logPath string) *Renderer {
	r := &Renderer{Exe: exe, LogPath: logPath, Swap: "flip", done: make(chan error, 1), options: render.DefaultPlaybackOptions()}
	r.stats.Store(render.Stats{Backend: "mf", HWDec: "unknown"})
	return r
}

// Scale remains unsupported: resizing a presentation buffer is not a video
// decode-resolution control. Hardware decoder use is also not inferred.
func (r *Renderer) Capabilities() render.Capabilities {
	return capabilities()
}

func (r *Renderer) Configure(o render.PlaybackOptions) error {
	o, err := playbackOptions(o)
	if err != nil {
		return err
	}
	if err := r.Capabilities().ValidateOptions(o); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return fmt.Errorf("mf: configure before Load")
	}
	r.options = o
	return nil
}

func (r *Renderer) UpdatePlayback(o render.PlaybackOptions) error {
	o, err := playbackOptions(o)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("mf: not running")
	}
	if r.level >= visibility.L3 {
		return fmt.Errorf("%w: live settings require an active MF engine", render.ErrUnsupported)
	}
	if err := r.callControlLocked("setPlayback", map[string]any{"options": o}, 5*time.Second, nil); err != nil {
		return err
	}
	r.options = o
	return nil
}

// Caller holds r.mu. Do not use Close here: this is an unexpected failure and
// cmd.Wait must notify Done so the existing supervisor creates a fresh host.
func (r *Renderer) callControlLocked(method string, params any, timeout time.Duration, out any) error {
	err := r.conn.Call(method, params, timeout, out)
	if !uncertainControlError(err) {
		return err
	}
	result := fmt.Errorf("mf: %s session is unsafe; terminating host: %w", method, err)
	if r.cmd != nil && r.cmd.Process != nil {
		if killErr := r.cmd.Process.Kill(); killErr != nil {
			result = errors.Join(result, fmt.Errorf("mf: terminate unresponsive host: %w", killErr))
		}
	}
	_ = r.conn.Close()
	return result
}

func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	if !validPosition(it.Start) {
		return fmt.Errorf("mf: invalid start position")
	}
	if s.Native() == 0 || s.Bounds().Dx() <= 0 || s.Bounds().Dy() <= 0 || s.Bounds().Dx() > 16384 || s.Bounds().Dy() > 16384 {
		return fmt.Errorf("mf: invalid window or canvas bounds")
	}
	r.Close()
	r.stats.Store(render.Stats{Backend: "mf", HWDec: "unknown", Pos: it.Start})
	ps, err := win32.NewPipeServer()
	if err != nil {
		return err
	}
	args := []string{"-mfhost", "-pipe", ps.Name, "-swap", r.Swap}
	if r.LogPath != "" {
		args = append(args, "-log", r.LogPath)
	}
	cmd := exec.Command(r.Exe, args...)
	var stderr *telemetry.ChildLog
	if r.LogPath != "" {
		stderr, err = telemetry.NewChildLog(strings.TrimSuffix(r.LogPath, ".log") + "-stderr.log")
		if err != nil {
			ps.Close()
			return err
		}
		cmd.Stderr = stderr
	}
	log := render.LogFunc(func(msg string, fields ...any) {
		base := []any{"display", r.Display, "role", "mfhost", "child_pid", cmd.Process.Pid}
		r.Log.Log(msg, append(base, fields...)...)
	})
	finish := func(err error) {
		if stderr != nil {
			logErr := stderr.Close()
			log.Log("child_log", "err", logErr, "dropped", stderr.Dropped())
		}
		log.Log("child_exit", "code", cmd.ProcessState.ExitCode(), "err", err)
	}
	if err := cmd.Start(); err != nil {
		if stderr != nil {
			_ = stderr.Close()
		}
		ps.Close()
		return fmt.Errorf("mfhost start: %w", err)
	}
	r.processPID.Store(int64(cmd.Process.Pid))
	log.Log("child_start")
	f, err := ps.Accept(5 * time.Second)
	if err != nil {
		cmd.Process.Kill()
		finish(cmd.Wait())
		return err
	}
	killing := &atomic.Bool{}
	conn := ipc.New(f, nil, func(m string, p json.RawMessage) {
		switch m {
		case "stats":
			var st struct {
				Dropped   int64    `json:"dropped"`
				Pos       *float64 `json:"pos"`
				Loops     int64    `json:"loops"`
				Presented int64    `json:"presented"`
				PTS100NS  int64    `json:"pts100ns"`
			}
			if err := json.Unmarshal(p, &st); err != nil {
				return
			}
			position := float64(st.PTS100NS) / 1e7
			if st.Pos != nil {
				position = *st.Pos
			}
			if !validPosition(position) {
				return
			}
			// MF's backend name is not proof that the selected decoder is hardware.
			r.stats.Store(render.Stats{Backend: "mf", HWDec: "unknown", Pos: position, Dropped: uint64(max(st.Dropped, 0)), Loops: st.Loops, LastErr: r.Stats().LastErr})
		case "error":
			var e ipc.Error
			if err := json.Unmarshal(p, &e); err != nil {
				return
			}
			s := r.Stats()
			s.LastErr = e.Error()
			r.stats.Store(s)
		}
	})
	conn.SetLifecycleLogger(log)
	var hello render.HelperIdentity
	if err := conn.Call("hello", map[string]any{"proto": 1, "app": "wallpaperd"}, 5*time.Second, &hello); err != nil {
		cmd.Process.Kill()
		conn.Close()
		finish(cmd.Wait())
		return fmt.Errorf("mfhost hello: %w", err)
	}
	log.Log("helper_identity", "helper_pid", hello.PID, "build", hello.Build)
	b := s.Bounds()
	r.mu.Lock()
	o := r.options
	r.mu.Unlock()
	load := map[string]any{"entry": it.Path, "wid": uint64(s.Native()), "bounds": map[string]int{"w": b.Dx(), "h": b.Dy()}, "paused": paused, "start": it.Start, "options": o}
	var loaded struct {
		Pos float64 `json:"pos"`
	}
	if err := conn.Call("load", load, 15*time.Second, &loaded); err != nil {
		cmd.Process.Kill()
		conn.Close()
		finish(cmd.Wait())
		return fmt.Errorf("mfhost load: %w", err)
	}
	if validPosition(loaded.Pos) {
		stats := r.Stats()
		stats.Pos = loaded.Pos
		r.stats.Store(stats)
	}
	exited := make(chan struct{})
	r.mu.Lock()
	r.cmd, r.conn, r.killing = cmd, conn, killing
	r.exited = exited
	r.level = visibility.L0
	if paused {
		r.level = visibility.L1
	}
	r.mu.Unlock()
	go func() {
		defer close(exited)
		err := cmd.Wait()
		finish(err)
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

func (r *Renderer) SetLevel(level visibility.Level) error {
	if level < visibility.L0 || level > visibility.L3 {
		return fmt.Errorf("mf: invalid level")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("mf: not running")
	}
	lv := int(level)
	if level == visibility.L2 {
		lv = 1
	}
	var result struct {
		Pos float64 `json:"pos"`
	}
	if err := r.callControlLocked("setPause", map[string]int{"level": lv}, 15*time.Second, &result); err != nil {
		return err
	}
	r.level = level
	if validPosition(result.Pos) {
		stats := r.Stats()
		stats.Pos = result.Pos
		r.stats.Store(stats)
	}
	return nil
}

// SetFPSCap 通过 IPC setFPS 让 mfhost 节流取帧/呈现（0 = 不限）。
func (r *Renderer) SetFPSCap(fps int) error {
	if fps < 0 || fps > 240 {
		return fmt.Errorf("mf: fps out of range")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("mf: not running")
	}
	return r.callControlLocked("setFPS", map[string]int{"fps": fps}, 5*time.Second, nil)
}

func (r *Renderer) Snapshot(string) error { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats   { return r.stats.Load().(render.Stats) }
func (r *Renderer) Done() <-chan error    { return r.done }

// Close 有意结束：shutdown → 1 s 内不退出就 Kill（02 章：宿主 2 s 内没退出就 TerminateProcess）。
func (r *Renderer) Close() error {
	r.mu.Lock()
	cmd, c, k, exited := r.cmd, r.conn, r.killing, r.exited
	r.cmd, r.conn = nil, nil
	r.mu.Unlock()
	if cmd == nil {
		return nil
	}
	k.Store(true)
	_ = c.Call("shutdown", nil, 500*time.Millisecond, nil)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
	}
	c.Close()
	<-exited
	return nil
}

var _ render.Renderer = (*Renderer)(nil)
var _ render.Configurable = (*Renderer)(nil)
var _ render.PlaybackUpdater = (*Renderer)(nil)
var _ render.CapabilityProvider = (*Renderer)(nil)

// HostMain 是 -mfhost 子进程入口。
func HostMain(pipe, swap, logPath string) int {
	if logPath != "" {
		if f, err := telemetry.NewChildLog(logPath); err == nil {
			defer func() {
				err := f.Close()
				if err != nil || f.Dropped() > 0 {
					fmt.Fprintf(os.Stderr, "mf log: err=%v dropped=%d\n", err, f.Dropped())
				}
			}()
			Logf = func(format string, a ...any) {
				fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339Nano), fmt.Sprintf(format, a...))
			}
		} else {
			fmt.Fprintln(os.Stderr, "mf log:", err)
		}
	}
	if err := RunHost(pipe, swap, 5*time.Second); err != nil {
		Logf("%v", err)
		return 2
	}
	return 0
}

func (r *Renderer) ChildPID() int { return int(r.processPID.Load()) }
