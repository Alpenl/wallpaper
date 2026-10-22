package mpv

import (
	"errors"
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
	Display    string                  // 用于管道/套接字名
	Playback   *render.PlaybackOptions // nil = muted, fill, speed 1, scale 1
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
	p := playbackOptions(o)
	o.Playback = &p
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
	p := playbackOptions(o)
	if p.Scale < 1 {
		hw = copyHwdec(hw)
	}
	a := []string{"--no-config", "--idle=yes", "--force-window=yes", "--wid=" + strconv.FormatUint(uint64(wid), 10),
		"--input-ipc-server=" + ipcName, "--hwdec=" + hw, "--loop-file=inf", "--keep-open=yes",
		"--no-input-default-bindings", "--input-vo-keyboard=no", "--input-cursor=no", "--cursor-autohide=no", "--osc=no", "--osd-level=0",
		"--no-terminal", "--msg-level=all=warn", "--scale=bilinear", "--dscale=bilinear", "--cscale=bilinear",
		"--pause=" + yesno(paused)}
	a = append(a, playbackArgs(p)...)
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

func playbackOptions(o Options) render.PlaybackOptions {
	if o.Playback != nil {
		if p, err := render.NormalizePlaybackOptions(*o.Playback); err == nil {
			return p
		}
	}
	return render.DefaultPlaybackOptions()
}

func playbackArgs(p render.PlaybackOptions) []string {
	keep, pan := "yes", "1"
	if p.Fit == "fit" {
		pan = "0"
	} else if p.Fit == "stretch" {
		keep, pan = "no", "0"
	}
	a := []string{"--keepaspect=" + keep, "--panscan=" + pan,
		"--speed=" + strconv.FormatFloat(p.Speed, 'f', -1, 64), "--mute=" + yesno(p.Muted), "--volume=" + strconv.Itoa(p.Volume)}
	if p.Muted {
		// No audio track means no needless audio decode/device work by default.
		a = append(a, "--no-audio")
	}
	if p.Scale < 1 {
		a = append(a, "--vf="+scaleFilter(p.Scale))
	}
	return a
}

// scaleFilter changes decoded video frames, NOT the desktop surface. Even
// dimensions avoid chroma-format restrictions. It is not promised to save power:
// hardware decoding must use copy mode and scaling uses libavfilter on the CPU.
func scaleFilter(scale float64) string {
	s := strconv.FormatFloat(scale, 'f', -1, 64)
	return "@wpscale:lavfi=[scale=w='max(2,trunc(iw*" + s + "/2)*2)':h='max(2,trunc(ih*" + s + "/2)*2)':flags=bilinear]"
}

func (r *Renderer) Capabilities() render.Capabilities {
	return render.Capabilities{Fit: true, Speed: true, Audio: true, Scale: true, Resume: true, FPS: true}
}

func (r *Renderer) Configure(p render.PlaybackOptions) error {
	p, err := render.NormalizePlaybackOptions(p)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return fmt.Errorf("mpv: configure before Load")
	}
	r.o.Playback = &p
	return nil
}

// UpdatePlayback changes layout/rate in place. Audio selection and software
// filter changes need a new Load, so reject those before issuing any command.
func (r *Renderer) UpdatePlayback(p render.PlaybackOptions) error {
	p, err := render.NormalizePlaybackOptions(p)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return fmt.Errorf("mpv: not running")
	}
	old := playbackOptions(r.o)
	if p.Muted != old.Muted || p.Scale != old.Scale {
		return fmt.Errorf("%w: live mpv audio/scale changes require reload", render.ErrUnsupported)
	}
	type change struct {
		name      string
		old, next any
	}
	var changes []change
	if p.Fit != old.Fit {
		oldKeep, oldPan := fitValues(old.Fit)
		keep, pan := fitValues(p.Fit)
		changes = append(changes, change{"keepaspect", oldKeep, keep}, change{"panscan", oldPan, pan})
	}
	if p.Speed != old.Speed {
		changes = append(changes, change{"speed", old.Speed, p.Speed})
	}
	if p.Volume != old.Volume {
		changes = append(changes, change{"volume", old.Volume, p.Volume})
	}
	for i, ch := range changes {
		if _, err := r.c.cmd("set_property", ch.name, ch.next); err != nil {
			// A failed request can have an uncertain outcome (IPC timeout). Restore
			// all attempted properties, including that request, to the old values.
			result := fmt.Errorf("mpv: update %s: %w", ch.name, err)
			for j := i; j >= 0; j-- {
				if _, rollback := r.c.cmd("set_property", changes[j].name, changes[j].old); rollback != nil {
					result = errors.Join(result, fmt.Errorf("restore %s: %w", changes[j].name, rollback))
				}
			}
			return result
		}
	}
	r.o.Playback = &p // L3 resume and later loads use the latest live settings.
	return nil
}

func fitValues(fit string) (keep bool, panscan float64) {
	if fit == "fill" {
		return true, 1
	}
	return fit != "stretch", 0
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ResolveExecutable checks discovery and file permissions without launching any
// process. A found executable is not a claim that its codecs or DLLs will load.
func ResolveExecutable(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "mpv"
		if runtime.GOOS == "windows" {
			path = "mpv.exe"
		}
	}
	return exec.LookPath(path)
}

func executableError(path string, err error) error {
	return fmt.Errorf("mpv could not start (%q): install mpv separately and set its executable in Settings > Playback & Performance > mpv path; on Windows choose mpv.exe and keep its required DLLs together: %w", path, err)
}

// Load 启动 mpv 并等待 IPC 就绪。
func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	r.Close()
	name := r.ipcName()
	r.mu.Lock()
	o := r.o
	r.mu.Unlock()
	path, err := ResolveExecutable(o.Path)
	if err != nil {
		return executableError(o.Path, err)
	}
	cmd := exec.Command(path, Args(o, s.Native(), name, it.Path, paused, it.Start)...)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return executableError(path, err)
	}
	rw, err := dial(name, 5*time.Second)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return executableError(path, err)
	}
	c := newClient(rw)
	killing := &atomic.Bool{}
	stop := make(chan struct{})
	r.mu.Lock()
	r.cmd, r.c, r.stop, r.killing, r.item, r.pos = cmd, c, stop, killing, it, it.Start
	r.mu.Unlock()
	r.stats.Store(render.Stats{Backend: "mpv", Pos: it.Start})
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
	c, it, o := r.c, r.item, r.o
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
		options := resumeOptions(o, pos, int(r.cap.Load()))
		options["pause"] = yesno(l > visibility.L0)
		_, err = c.cmd("loadfile", it.Path, "replace", -1, options)
		if err == nil {
			err = c.setPause(l > visibility.L0)
		}
	default:
		err = c.setPause(l > visibility.L0)
	}
	if err == nil {
		r.level.Store(int32(l))
	}
	return err
}

// loadfile resets per-file options; send the full effective configuration on L3
// resume instead of relying on the previous file's filters and playback state.
func resumeOptions(o Options, pos float64, fps int) map[string]string {
	p := playbackOptions(o)
	options := map[string]string{"start": strconv.FormatFloat(pos, 'f', 3, 64),
		"speed": strconv.FormatFloat(p.Speed, 'f', -1, 64), "mute": yesno(p.Muted), "volume": strconv.Itoa(p.Volume),
		"keepaspect": "yes", "panscan": "1", "aid": "auto", "hwdec": playbackHwdec(o)}
	if p.Muted {
		options["aid"] = "no"
	}
	if p.Fit == "fit" {
		options["panscan"] = "0"
	} else if p.Fit == "stretch" {
		options["keepaspect"], options["panscan"] = "no", "0"
	}
	filters := []string{}
	if p.Scale < 1 {
		filters = append(filters, scaleFilter(p.Scale))
	}
	if fps > 0 {
		filters = append(filters, "@wpcap:fps="+strconv.Itoa(fps))
		options["hwdec"] = copyHwdec(hwdecFor(o.Hwdec))
	}
	options["vf"] = strings.Join(filters, ",")
	return options
}

// SetFPSCap 限帧（F-05 电池 15 fps 档、过热、省电性能档）：插入带标签的 lavfi fps 滤镜 `@wpcap:fps=N`。
// 代价：软件滤镜需要系统内存里的帧，所以限帧期间把 hwdec 切到对应的 *-copy 模式（d3d11va-copy / auto-copy）；
// 解码仍是硬件，只多一次显存→内存拷贝，换来呈现/合成次数减半。是否比全帧率零拷贝更省电【推断】，
// 需要 TEST_PLAN PB-battery 实测（Q-PB1）。fps=0 恢复原状。
func (r *Renderer) SetFPSCap(fps int) error {
	if fps < 0 || fps > 240 {
		return fmt.Errorf("mpv: fps must be between 0 and 240")
	}
	r.mu.Lock()
	c, o := r.c, r.o
	r.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mpv: not running")
	}
	if int(r.cap.Load()) == fps {
		return nil
	}
	hw := hwdecFor(o.Hwdec)
	_, _ = c.cmd("vf", "remove", "@wpcap")
	if fps > 0 {
		if _, err := c.cmd("set_property", "hwdec", copyHwdec(hw)); err != nil {
			return err
		}
		if _, err := c.cmd("vf", "add", "@wpcap:fps="+strconv.Itoa(fps)); err != nil {
			return err
		}
	} else if _, err := c.cmd("set_property", "hwdec", playbackHwdec(o)); err != nil {
		return err
	}
	r.cap.Store(int32(fps))
	return nil
}

func playbackHwdec(o Options) string {
	hw := hwdecFor(o.Hwdec)
	if playbackOptions(o).Scale < 1 {
		return copyHwdec(hw)
	}
	return hw
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
var _ render.Configurable = (*Renderer)(nil)
var _ render.PlaybackUpdater = (*Renderer)(nil)
var _ render.CapabilityProvider = (*Renderer)(nil)
var _ io.Closer = (*client)(nil)
