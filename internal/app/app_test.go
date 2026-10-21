package app

import (
	"context"
	"errors"
	"image"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

type fakeSurf struct {
	mu      sync.Mutex
	visible bool
	closed  bool
}

func (s *fakeSurf) Native() uintptr         { return 1 }
func (s *fakeSurf) Bounds() image.Rectangle { return image.Rect(0, 0, 100, 100) }
func (s *fakeSurf) SetVisible(v bool) error { s.mu.Lock(); s.visible = v; s.mu.Unlock(); return nil }
func (s *fakeSurf) Close() error            { s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }

type fakeHost struct {
	mu       sync.Mutex
	displays []desktop.Display
	ev       chan desktop.Event
	surfs    []*fakeSurf
	wall     map[desktop.DisplayID]string
	healthy  error
	failSurf bool
}

func newHost(n int) *fakeHost {
	h := &fakeHost{ev: make(chan desktop.Event, 64), wall: map[desktop.DisplayID]string{}}
	for i := 0; i < n; i++ {
		h.displays = append(h.displays, desktop.Display{ID: desktop.DisplayID(string(rune('A' + i))), Name: string(rune('A' + i)),
			Bounds: image.Rect(i*100, 0, i*100+100, 100), Primary: i == 0})
	}
	return h
}
func (h *fakeHost) Start() error { return nil }
func (h *fakeHost) Displays() []desktop.Display {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]desktop.Display(nil), h.displays...)
}
func (h *fakeHost) CreateSurface(id desktop.DisplayID) (desktop.Surface, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failSurf {
		return nil, errors.New("no desktop")
	}
	s := &fakeSurf{}
	h.surfs = append(h.surfs, s)
	return s, nil
}
func (h *fakeHost) Health() error                { h.mu.Lock(); defer h.mu.Unlock(); return h.healthy }
func (h *fakeHost) Events() <-chan desktop.Event { return h.ev }
func (h *fakeHost) Caps() desktop.Caps           { return desktop.Caps{Video: true, PerDisplay: true} }
func (h *fakeHost) Close() error                 { return nil }
func (h *fakeHost) SetSystemWallpaper(id desktop.DisplayID, p string) error {
	h.mu.Lock()
	h.wall[id] = p
	h.mu.Unlock()
	return nil
}

type fakeR struct {
	mu      sync.Mutex
	display desktop.DisplayID
	levels  []visibility.Level
	loaded  bool
	closed  bool
	paused  bool
	done    chan error
	failLd  bool
	caps    []int
}

func (r *fakeR) Load(it render.Item, s desktop.Surface, paused bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failLd {
		return errors.New("load failed")
	}
	r.loaded, r.paused = true, paused
	return nil
}
func (r *fakeR) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	r.levels = append(r.levels, l)
	r.mu.Unlock()
	return nil
}
func (r *fakeR) SetFPSCap(n int) error {
	r.mu.Lock()
	r.caps = append(r.caps, n)
	r.mu.Unlock()
	return nil
}
func (r *fakeR) lastCap() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.caps) == 0 {
		return -1
	}
	return r.caps[len(r.caps)-1]
}
func (r *fakeR) Snapshot(string) error { return nil }
func (r *fakeR) Stats() render.Stats   { return render.Stats{Backend: "fake"} }
func (r *fakeR) Done() <-chan error    { return r.done }
func (r *fakeR) Close() error          { r.mu.Lock(); r.closed = true; r.mu.Unlock(); return nil }
func (r *fakeR) isLoaded() bool        { r.mu.Lock(); defer r.mu.Unlock(); return r.loaded }

func (r *fakeR) last() visibility.Level {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.levels) == 0 {
		return -1
	}
	return r.levels[len(r.levels)-1]
}

type env struct {
	a      *App
	h      *fakeHost
	mu     sync.Mutex
	rs     []*fakeR
	cancel context.CancelFunc
	failLd bool
	store  *config.Store
}

func (e *env) renderers(d desktop.DisplayID) []*fakeR {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*fakeR
	for _, r := range e.rs {
		if r.display == d {
			out = append(out, r)
		}
	}
	return out
}

func (e *env) cur(d desktop.DisplayID) *fakeR {
	rs := e.renderers(d)
	if len(rs) == 0 {
		return nil
	}
	return rs[len(rs)-1]
}

func setup(t *testing.T, n int, withItem bool) *env {
	t.Helper()
	return setupWith(t, n, withItem, nil)
}

func setupWith(t *testing.T, n int, withItem bool, mod func(*Deps)) *env {
	t.Helper()
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := config.DefaultPaths()
	st, err := config.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if withItem {
		dir := filepath.Join(p.Library, "x")
		os.MkdirAll(filepath.Join(dir, "content"), 0o755)
		st.Library.Upsert(config.Item{ID: "x", Dir: dir, Type: "video", Entry: "content/a.mp4", Snapshot: filepath.Join(p.Cache, "x", "first.png")})
		st.Assign.Default = "x"
	}
	e := &env{h: newHost(n), store: st}
	deps := (Deps{Host: e.h, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewRenderer: func(kind string, cfg config.Config, d desktop.DisplayID) render.Renderer {
			e.mu.Lock()
			defer e.mu.Unlock()
			r := &fakeR{display: d, done: make(chan error, 1), failLd: e.failLd}
			e.rs = append(e.rs, r)
			return r
		},
		Import: func(ctx context.Context, src string) (*importer.Result, error) {
			if src == "bad" {
				return nil, errors.New("nope")
			}
			return &importer.Result{Item: config.Item{ID: "new", Title: "New", Type: "video", Dir: t.TempDir(), Entry: "content/b.mp4"}, Warnings: []string{"w"}}, nil
		}})
	if mod != nil {
		mod(&deps)
	}
	e.a = New(deps)
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	go e.a.Run(ctx)
	t.Cleanup(func() { cancel(); e.a.Wait() })
	return e
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	eventuallyD(t, 3*time.Second, what, f)
}

func eventuallyD(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	for i := 0; i < int(d/(10*time.Millisecond)); i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("超时: " + what)
}

func (e *env) sync() { ch := make(chan struct{}); e.a.Do(func() { close(ch) }); <-ch }

func TestStartsRendererPerDisplayAndSetsSystemWallpaper(t *testing.T) {
	e := setup(t, 2, true)
	eventually(t, "两块屏各一个渲染器", func() bool {
		a, b := e.cur("A"), e.cur("B")
		return a != nil && b != nil && a.isLoaded() && b.isLoaded()
	})
	eventually(t, "系统壁纸兜底", func() bool { e.h.mu.Lock(); defer e.h.mu.Unlock(); return len(e.h.wall) == 2 })
	v := e.a.View()
	if len(v.Displays) != 2 || v.Displays[0].ItemID != "x" || v.Displays[0].Level != "L0" {
		t.Fatalf("%+v", v.Displays)
	}
}

func TestPerDisplayFullscreenPause(t *testing.T) {
	e := setup(t, 2, true)
	eventually(t, "启动", func() bool { r := e.cur("B"); return r != nil && r.isLoaded() })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "A", Reason: visibility.Fullscreen, Active: true}
	eventually(t, "A 暂停", func() bool { return e.cur("A").last() == visibility.L1 })
	if l := e.cur("B").last(); l == visibility.L1 {
		t.Fatal("B 不应暂停（按屏判定）")
	}
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "A", Reason: visibility.Fullscreen, Active: false}
	eventually(t, "A 去抖后恢复", func() bool { return e.cur("A").last() == visibility.L0 })
}

func TestUserPauseAllAndConfigToggle(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	e.a.SetPausedAll(true)
	eventually(t, "暂停全部", func() bool { return e.cur("A").last() == visibility.L1 })
	if !e.a.View().PausedAll {
		e.sync()
		if !e.a.View().PausedAll {
			t.Fatal("视图应显示暂停全部")
		}
	}
	e.a.SetPausedAll(false)
	eventually(t, "恢复", func() bool { return e.cur("A").last() == visibility.L0 })
	// 关闭“全屏时暂停”后，全屏信号被忽略
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.OnFullscreen = false })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "A", Reason: visibility.Fullscreen, Active: true}
	time.Sleep(300 * time.Millisecond)
	if e.cur("A").last() == visibility.L1 {
		t.Fatal("关闭后不应暂停")
	}
}

func TestBatteryPolicyAndLowBattery(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true}
	eventually(t, "电池默认暂停（D-07）", func() bool { return e.cur("A").last() == visibility.L1 })
	first := e.cur("A")
	e.h.ev <- desktop.Event{Kind: desktop.EventBattery, Percent: 15}
	eventually(t, "低电量 ≤20% → L4 结束渲染器", func() bool { first.mu.Lock(); defer first.mu.Unlock(); return first.closed })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: false}
	eventually(t, "插电后重新启动渲染器", func() bool { r := e.cur("A"); return r != first && r != nil && r.isLoaded() })
	// 策略改为 ignore：拔电不暂停
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.BatteryPolicy = "ignore" })
	e.sync()
	r := e.cur("A")
	n := len(e.renderers("A"))
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true}
	time.Sleep(300 * time.Millisecond)
	if e.cur("A").last() == visibility.L1 && len(e.renderers("A")) == n {
		t.Fatal("ignore 策略下不应暂停", r.last())
	}
}

func TestCrashRestartAndFail(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	r := e.cur("A")
	r.done <- errors.New("crash")
	eventually(t, "退避 0.5 s 后重启", func() bool { c := e.cur("A"); return c != r && c != nil && c.isLoaded() })
	v := e.a.View()
	if v.Displays[0].State != "active" {
		e.sync()
	}
	// 让后续启动全部失败 → 很快累计到 5 次 → Failed
	e.mu.Lock()
	e.failLd = true
	e.mu.Unlock()
	e.cur("A").done <- errors.New("crash2")
	eventuallyD(t, 15*time.Second, "进入 Failed（退避 0.5+1+2+4 s）", func() bool {
		for _, d := range e.a.View().Displays {
			if d.State == "failed" {
				return true
			}
		}
		return false
	})
	if e.a.View().LastError == "" {
		t.Fatal("应提示用户")
	}
	// 更换壁纸 = 重试
	e.mu.Lock()
	e.failLd = false
	e.mu.Unlock()
	yd := t.TempDir()
	e.a.Do(func() { e.store.Library.Upsert(config.Item{ID: "y", Dir: yd, Type: "video", Entry: "content/b.mp4"}) })
	e.a.Assign("A", "y")
	eventually(t, "重试后恢复", func() bool {
		d := e.a.View().Displays[0]
		c := e.cur("A")
		return d.State == "active" && c.isLoaded() && d.ItemID == "y"
	})
}

func TestShellRestartReattach(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	r := e.cur("A")
	e.h.ev <- desktop.Event{Kind: desktop.EventShellRestarted, Source: "TaskbarCreated"}
	eventually(t, "重挂后重新启动", func() bool { c := e.cur("A"); return c != r && c.isLoaded() })
	e.h.mu.Lock()
	n := len(e.h.surfs)
	closed := e.h.surfs[0].closed
	e.h.mu.Unlock()
	if n != 2 || !closed {
		t.Fatal("旧表面应关闭、新表面应创建", n, closed)
	}
	// 健康检查失败（唤醒后）也触发重挂
	e.h.mu.Lock()
	e.h.healthy = desktop.ErrDetached
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Suspend, Active: true}
	e.h.mu.Lock()
	e.h.healthy = nil
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Suspend, Active: false}
	eventually(t, "唤醒健康检查", func() bool { e.h.mu.Lock(); defer e.h.mu.Unlock(); return len(e.h.surfs) >= 2 })
}

func TestHotplug(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	e.h.mu.Lock()
	e.h.displays = append(e.h.displays, desktop.Display{ID: "B", Name: "B", Bounds: image.Rect(100, 0, 200, 100)})
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "新屏启动", func() bool { r := e.cur("B"); return r != nil && r.isLoaded() })
	e.h.mu.Lock()
	e.h.displays = e.h.displays[1:]
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "拔掉的屏渲染器结束", func() bool { r := e.cur("A"); r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
	if len(e.a.View().Displays) != 1 {
		e.sync()
	}
	if v := e.a.View(); len(v.Displays) != 1 || v.Displays[0].ID != "B" {
		t.Fatal(v.Displays)
	}
}

func TestImportAssignRemove(t *testing.T) {
	e := setup(t, 1, false)
	e.sync()
	if e.cur("A") != nil {
		t.Fatal("没有壁纸时不应启动渲染器")
	}
	e.a.Import("bad", "")
	eventually(t, "导入失败提示", func() bool {
		v := e.a.View()
		return !v.Importing && len(v.LastImport) > 0 && v.LastImport[:6] == "导入"
	})
	e.a.Import("good.mp4", "A")
	eventually(t, "导入并应用到 A 后播放", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	if v := e.a.View(); len(v.Library) != 1 || v.Displays[0].ItemID != "new" {
		t.Fatal(v)
	}
	e.a.Remove("new")
	eventually(t, "删除后停止", func() bool { r := e.cur("A"); r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
	e.sync()
	if v := e.a.View(); len(v.Library) != 0 || v.Displays[0].ItemID != "" {
		t.Fatal(v)
	}
	e.cancel()
	e.a.Wait()
	b, err := os.ReadFile(filepath.Join(e.store.Paths.Config, "state.json"))
	if err != nil || !contains(string(b), `"cleanShutdown": true`) {
		t.Fatal("退出时应写 cleanShutdown=true", err, string(b))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestSafeMode(t *testing.T) {
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := config.DefaultPaths()
	st, _ := config.Open(p)
	st.Library.Upsert(config.Item{ID: "x", Dir: t.TempDir(), Type: "video", Entry: "content/a.mp4"})
	st.Assign.Default = "x"
	h := newHost(1)
	var made int
	var mu sync.Mutex
	a := New(Deps{Host: h, Store: st, SafeMode: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewRenderer: func(string, config.Config, desktop.DisplayID) render.Renderer {
			mu.Lock()
			made++
			mu.Unlock()
			return &fakeR{done: make(chan error, 1)}
		}})
	ctx, cancel := context.WithCancel(context.Background())
	go a.Run(ctx)
	defer func() { cancel(); a.Wait() }()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if made != 0 || !a.View().SafeMode {
		t.Fatal("安全模式不应加载壁纸")
	}
	mu.Unlock()
	a.ExitSafeMode()
	eventually(t, "退出安全模式后加载", func() bool { mu.Lock(); defer mu.Unlock(); return made == 1 })
}

// F-05：电池 fps15 档 → 渲染器限帧 15；插电恢复 0；省电性能档全程 15。
func TestFPS15BatteryAndSaverProfile(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.BatteryPolicy = "fps15" })
	e.sync()
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true}
	eventually(t, "电池 → 15 fps", func() bool { return e.cur("A").lastCap() == 15 })
	if e.cur("A").last() == visibility.L1 {
		t.Fatal("fps15 不应暂停")
	}
	eventually(t, "视图显示限帧", func() bool {
		v := e.a.View()
		return len(v.Displays) == 1 && v.Displays[0].FPSCap == 15 && v.Power.OnBattery
	})
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: false}
	eventually(t, "插电 → 不限", func() bool { return e.cur("A").lastCap() == 0 })
	e.a.UpdateConfig(func(c *config.Config) { c.Perf.Profile = "saver" })
	eventually(t, "省电档 → 15", func() bool { return e.cur("A").lastCap() == 15 })
	e.a.UpdateConfig(func(c *config.Config) { c.Perf.Profile = "smooth" })
	eventually(t, "流畅档 → 不限", func() bool { return e.cur("A").lastCap() == 0 })
	// 过热（ThermalSerious）只限帧不暂停
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Reason: visibility.ThermalSerious, Active: true}
	eventually(t, "过热 → 15", func() bool { return e.cur("A").lastCap() == 15 })
	eventually(t, "视图显示过热", func() bool { return e.a.View().Power.Thermal == "ThermalSerious" })
}

func TestEffectiveCap(t *testing.T) {
	for _, c := range [][3]int{{0, 0, 0}, {0, 15, 15}, {30, 0, 30}, {30, 15, 15}, {15, 30, 15}} {
		if EffectiveCap(c[0], c[1]) != c[2] {
			t.Error(c)
		}
	}
}

// F-07：一键转码替换入口并重启渲染器；失败保留原条目。
func TestTranscodeFlow(t *testing.T) {
	var fail atomic.Bool
	e := setupWith(t, 1, true, func(d *Deps) {
		d.Transcode = func(ctx context.Context, src, dst string) error {
			if fail.Load() {
				return errors.New("boom")
			}
			return os.WriteFile(dst, []byte("x"), 0o644)
		}
	})
	eventually(t, "启动", func() bool { r := e.cur("A"); return r != nil && r.isLoaded() })
	if !e.a.View().HasEncoder {
		t.Fatal("HasEncoder")
	}
	first := e.cur("A")
	e.a.Transcode("x")
	eventually(t, "转码完成", func() bool {
		v := e.a.View()
		return len(v.Library) == 1 && v.Library[0].Transcode == "done" && v.Library[0].Entry == "optimized.mp4"
	})
	eventually(t, "重启渲染器", func() bool { r := e.cur("A"); return r != first && r.isLoaded() })
	fail.Store(true)
	e.sync()
	e.a.Transcode("x")
	eventually(t, "失败提示", func() bool {
		return e.a.View().Transcoding["x"] != "" && e.a.View().Transcoding["x"] != "正在转码…"
	})
	if e.a.View().Library[0].Entry != "optimized.mp4" {
		t.Fatal("失败不应改条目")
	}
}

func TestNextRotatesDefault(t *testing.T) {
	e := setup(t, 1, true)
	dir := t.TempDir()
	e.a.Do(func() { e.store.Library.Upsert(config.Item{ID: "y", Dir: dir, Type: "video", Entry: "a.mp4"}) })
	e.sync()
	e.a.Next()
	eventually(t, "切到 y", func() bool { return e.defaultID() == "y" })
	e.a.Next()
	eventually(t, "回到 x", func() bool { return e.defaultID() == "x" })
}

func (e *env) defaultID() string {
	ch := make(chan string)
	e.a.Do(func() { ch <- e.store.Assign.Default })
	return <-ch
}
