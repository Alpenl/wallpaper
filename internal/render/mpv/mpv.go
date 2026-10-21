package mpv

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

// Options 是 mpv 渲染器参数。
type Options struct {
	Path       string        // mpv 可执行文件
	Hwdec      string        // 空 = 平台默认（Win d3d11va，Linux auto-safe）
	LogFile    string        // --log-file
	StatsEvery time.Duration // 卡住检测采样间隔（2.4：5 s）
	ExtraArgs  []string
	Display    string // 用于管道/套接字名
}

// Renderer 实现 render.Renderer。
type Renderer struct {
	o       Options
	mu      sync.Mutex
	cmd     *exec.Cmd
	c       *client
	stop    chan struct{}
	killing *atomic.Bool
	level   atomic.Int32
	cap     atomic.Int32
	item    render.Item
	pos     float64
	done    chan error
	stats   atomic.Value
}

// New 创建 mpv 渲染器。
func New(o Options) *Renderer {
	if o.StatsEvery == 0 {
		o.StatsEvery = 5 * time.Second
	}
	r := &Renderer{o: o, done: make(chan error, 1)}
	r.stats.Store(render.Stats{Backend: "mpv"})
	return r
}

var seq atomic.Int64

func (r *Renderer) ipcName() string {
	n := seq.Add(1)
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\wpd-%d-mpv-%s-%d`, os.Getpid(), r.o.Display, n)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("wpd-%d-mpv-%d.sock", os.Getpid(), n))
}

// Args 返回 06 章 6.5 的播放参数（导出以便测试）。
func Args(o Options, wid uintptr, ipcName, file string, paused bool, start float64) []string {
	hw := hwdecFor(o.Hwdec)
	a := []string{"--no-config", "--idle=yes", "--force-window=yes", "--wid=" + strconv.FormatUint(uint64(wid), 10),
		"--input-ipc-server=" + ipcName, "--hwdec=" + hw, "--loop-file=inf", "--keep-open=yes", "--no-audio",
		"--no-input-default-bindings", "--input-vo-keyboard=no", "--input-cursor=no", "--cursor-autohide=no", "--osc=no", "--osd-level=0",
		"--no-terminal", "--msg-level=all=warn", "--panscan=1.0", "--scale=bilinear", "--dscale=bilinear", "--cscale=bilinear",
		"--pause=" + yesno(paused)}
	if runtime.GOOS == "windows" {
		a = append(a, "--vo=gpu-next", "--gpu-api=d3d11")
	}
	if o.LogFile != "" {
		a = append(a, "--log-file="+o.LogFile)
	}
	if start > 0 {
		a = append(a, "--start="+strconv.FormatFloat(start, 'f', 3, 64))
	}
	a = append(a, o.ExtraArgs...)
	return append(a, "--", file)
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Load 启动 mpv 并等待 IPC 就绪。
func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	r.Close()
	name := r.ipcName()
	cmd := exec.Command(r.o.Path, Args(r.o, s.Native(), name, it.Path, paused, it.Start)...)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mpv start: %w", err)
	}
	rw, err := dial(name, 5*time.Second)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	c := newClient(rw)
	killing := &atomic.Bool{}
	stop := make(chan struct{})
	r.mu.Lock()
	r.cmd, r.c, r.stop, r.killing, r.item, r.pos = cmd, c, stop, killing, it, it.Start
	r.mu.Unlock()
	r.cap.Store(0)
	if paused {
		r.level.Store(1)
	} else {
		r.level.Store(0)
	}
	go r.statsLoop(c, stop, cmd)
	go func() {
		err := cmd.Wait()
		c.Close()
		if !killing.Load() {
			r.fail(fmt.Errorf("mpv exited: %v", err))
		}
	}()
	return nil
}

func (r *Renderer) fail(err error) {
	select {
	case r.done <- err:
	default:
	}
}

func (r *Renderer) statsLoop(c *client, stop <-chan struct{}, cmd *exec.Cmd) {
	t := time.NewTicker(r.o.StatsEvery)
	defer t.Stop()
	var det stallDetector
	for {
		select {
		case <-stop:
			return
		case <-c.closed:
			return
		case <-t.C:
		}
		paused := r.level.Load() > 0
		if paused { // 暂停期间不查询，减少唤醒（07 章 7.6）
			det.observe(true, 0, 0)
			continue
		}
		pos, err := c.getFloat("time-pos")
		if err != nil {
			if err == errClosed {
				return
			}
			s := r.Stats()
			s.LastErr = err.Error()
			r.stats.Store(s)
			cmd.Process.Kill() // 不置 killing：按意外退出上报，交给 supervisor
			return
		}
		hw, _ := c.getString("hwdec-current")
		drop, _ := c.getFloat("frame-drop-count")
		r.mu.Lock()
		r.pos = pos
		r.mu.Unlock()
		r.stats.Store(render.Stats{Backend: "mpv", HWDec: hw, Dropped: uint64(drop), Pos: pos, Loops: c.loops.Load()})
		if det.observe(false, c.loops.Load(), pos) {
			s := r.Stats()
			s.LastErr = "stall"
			r.stats.Store(s)
			cmd.Process.Kill()
			return
		}
	}
}

// SetLevel：L0 播放；L1/L2 暂停（视频没有 L2，07 章 v1.1）；L3 stop 释放解码器（进程保留，--idle），恢复时 loadfile + start。
func (r *Renderer) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	c, it := r.c, r.item
	r.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mpv: not running")
	}
	prev := visibility.Level(r.level.Load())
	var err error
	switch {
	case l >= visibility.L3 && prev < visibility.L3:
		if p, e := c.getFloat("time-pos"); e == nil {
			r.mu.Lock()
			r.pos = p
			r.mu.Unlock()
		}
		_, err = c.cmd("stop")
	case l < visibility.L3 && prev >= visibility.L3:
		r.mu.Lock()
		pos := r.pos
		r.mu.Unlock()
		_, err = c.cmd("loadfile", it.Path, "replace", -1, "start="+strconv.FormatFloat(pos, 'f', 3, 64))
		if err == nil {
			err = c.setPause(l > visibility.L0)
		}
	default:
		err = c.setPause(l > visibility.L0)
	}
	r.level.Store(int32(l))
	return err
}

// SetFPSCap 限帧（F-05 电池 15 fps 档、过热、省电性能档）：插入带标签的 lavfi fps 滤镜 `@wpcap:fps=N`。
// 代价：软件滤镜需要系统内存里的帧，所以限帧期间把 hwdec 切到对应的 *-copy 模式（d3d11va-copy / auto-copy）；
// 解码仍是硬件，只多一次显存→内存拷贝，换来呈现/合成次数减半。是否比全帧率零拷贝更省电【推断】，
// 需要 TEST_PLAN PB-battery 实测（Q-PB1）。fps=0 恢复原状。
func (r *Renderer) SetFPSCap(fps int) error {
	r.mu.Lock()
	c := r.c
	r.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mpv: not running")
	}
	if int(r.cap.Load()) == fps {
		return nil
	}
	hw := hwdecFor(r.o.Hwdec)
	_, _ = c.cmd("vf", "remove", "@wpcap")
	if fps > 0 {
		if _, err := c.cmd("set_property", "hwdec", copyHwdec(hw)); err != nil {
			return err
		}
		if _, err := c.cmd("vf", "add", "@wpcap:fps="+strconv.Itoa(fps)); err != nil {
			return err
		}
	} else if _, err := c.cmd("set_property", "hwdec", hw); err != nil {
		return err
	}
	r.cap.Store(int32(fps))
	return nil
}

// FPSCap 返回当前生效的限帧（0 = 不限）。
func (r *Renderer) FPSCap() int { return int(r.cap.Load()) }

func hwdecFor(h string) string {
	if h != "" {
		return h
	}
	if runtime.GOOS == "windows" {
		return "d3d11va"
	}
	return "auto-safe"
}

// copyHwdec 返回 hw 对应的拷贝模式（软件滤镜可用）。
func copyHwdec(hw string) string {
	switch {
	case hw == "no", strings.HasSuffix(hw, "-copy"):
		return hw
	case hw == "auto", hw == "auto-safe", hw == "yes":
		return "auto-copy"
	default:
		return hw + "-copy"
	}
}

// Snapshot 截一帧到 path（screenshot-to-file … video）。
func (r *Renderer) Snapshot(path string) error {
	r.mu.Lock()
	c := r.c
	r.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mpv: not running")
	}
	_, err := c.cmd("screenshot-to-file", path, "video")
	return err
}

func (r *Renderer) Stats() render.Stats { return r.stats.Load().(render.Stats) }
func (r *Renderer) Done() <-chan error  { return r.done }

// Position 返回最近一次已知的播放位置（续播用）。
func (r *Renderer) Position() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos
}

// WaitFirstFrame 等待第一次 playback-restart（测试与诊断用）。
func (r *Renderer) WaitFirstFrame(d time.Duration) bool {
	r.mu.Lock()
	c := r.c
	r.mu.Unlock()
	if c == nil {
		return false
	}
	select {
	case <-c.first:
		return true
	case <-time.After(d):
		return false
	}
}

// Close 有意结束 mpv。
func (r *Renderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil {
		return nil
	}
	r.killing.Store(true)
	close(r.stop)
	_, _ = r.c.cmd("quit")
	r.c.Close()
	cmd := r.cmd
	r.cmd, r.c = nil, nil
	done := make(chan struct{})
	go func() { cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		cmd.Process.Kill()
	}
	return nil
}

var _ render.Renderer = (*Renderer)(nil)
var _ io.Closer = (*client)(nil)
