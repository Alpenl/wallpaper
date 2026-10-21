// Package app 是编排层（02 章 2.6）：壁纸库 × 显示器 → 分配，驱动 visibility 状态机与渲染器监管。
// 只依赖接口包（desktop、render、visibility、config）；平台实现由 cmd/wallpaperd 注入。
//
// 线程模型：一个事件循环 goroutine 拥有全部状态（状态机不加锁，07 章）；每块显示器一个 worker goroutine
// 串行执行可能阻塞的渲染器调用（IPC），结果投递回事件循环。UI 通过 View() 读取快照，通过公开方法投递命令。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/render"
	"wpapp/internal/supervisor"
	"wpapp/internal/telemetry"
	"wpapp/internal/visibility"
)

// RendererFactory 按壁纸类型与当前配置创建渲染器。
type RendererFactory func(kind string, cfg config.Config, display desktop.DisplayID) render.Renderer

// Deps 是注入的依赖。
type Deps struct {
	Host        desktop.Host
	Store       *config.Store
	NewRenderer RendererFactory
	Import      func(ctx context.Context, src string) (*importer.Result, error)
	Log         *slog.Logger
	SafeMode    bool
	// SetLaunchAtLogin 由 UI 层（MyGo）提供；nil 表示不支持。
	SetLaunchAtLogin func(bool) error
}

// DisplayView 是 UI 用的显示器快照。
type DisplayView struct {
	ID      string
	Name    string
	W, H    int
	Primary bool
	ItemID  string
	Level   string
	State   string // active | recovering | failed | idle
	Backend string
	HWDec   string
	Dropped uint64
}

// View 是 UI 用的整体快照。
type View struct {
	Displays   []DisplayView
	Library    []config.Item
	Config     config.Config
	PausedAll  bool
	SafeMode   bool
	Importing  bool
	LastImport string
	LastError  string
	ReadOnly   []string
}

type screen struct {
	n       int // 状态机里的编号（单调递增，不复用）
	d       desktop.Display
	surf    desktop.Surface
	r       render.Renderer
	gen     int
	item    *config.Item
	started bool
	crashes int
	okAt    time.Time
	ops     chan func()
	quit    chan struct{}
}

// App 是编排器。
type App struct {
	d       Deps
	log     *slog.Logger
	t0      time.Time
	ctl     *visibility.Controller
	screens map[desktop.DisplayID]*screen
	nextN   int
	post    chan func()
	timer   *time.Timer
	// 原始信号（用于配置变化后重建状态机时回放）
	global    map[visibility.Reason]bool
	perDisp   map[desktop.DisplayID]visibility.Reason
	onBattery bool
	battPct   int
	pausedAll bool
	importing bool
	lastImp   string
	lastErr   string
	reattach  int

	vmu      sync.Mutex
	view     View
	onChange []func()
	stopped  chan struct{}
}

// New 创建编排器。
func New(d Deps) *App {
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &App{d: d, log: d.Log, screens: map[desktop.DisplayID]*screen{}, post: make(chan func(), 256),
		global: map[visibility.Reason]bool{}, perDisp: map[desktop.DisplayID]visibility.Reason{}, battPct: -1,
		stopped: make(chan struct{})}
}

func (a *App) now() time.Duration { return time.Since(a.t0) }

// VisConfig 把 config.json 的暂停策略映射到 07 章状态机参数。
func VisConfig(c config.Config) visibility.Config {
	vc := visibility.DefaultConfig(visibility.Video)
	switch c.Pause.BatteryPolicy {
	case "fps15":
		vc.BatteryPolicy = visibility.BatteryFPS15
	case "ignore":
		vc.BatteryPolicy = visibility.BatteryIgnore
	default:
		vc.BatteryPolicy = visibility.BatteryPause
	}
	vc.ResumeDebounce = time.Duration(c.Pause.ResumeDebounceMs) * time.Millisecond
	return vc
}

// OnChange 注册视图变化回调（在事件循环 goroutine 上调用，回调须自行切到 UI 线程）。
func (a *App) OnChange(fn func()) {
	a.vmu.Lock()
	a.onChange = append(a.onChange, fn)
	a.vmu.Unlock()
}

// View 返回最新快照（任意 goroutine）。
func (a *App) View() View {
	a.vmu.Lock()
	defer a.vmu.Unlock()
	return a.view
}

// Do 把 fn 投递到事件循环。
func (a *App) Do(fn func()) {
	select {
	case a.post <- fn:
	case <-a.stopped:
	}
}

// Run 运行事件循环直到 ctx 结束。
func (a *App) Run(ctx context.Context) error {
	a.t0 = time.Now()
	a.ctl = visibility.NewController(VisConfig(a.d.Store.Config))
	a.timer = time.NewTimer(time.Hour)
	a.timer.Stop()
	defer close(a.stopped)
	if err := a.d.Host.Start(); err != nil {
		return fmt.Errorf("desktop host: %w", err)
	}
	a.log.Info("start", "safeMode", a.d.SafeMode, "displays", len(a.d.Host.Displays()), "caps", fmt.Sprintf("%+v", a.d.Host.Caps()))
	a.syncDisplays()
	a.publish()
	events := a.d.Host.Events()
	for {
		select {
		case <-ctx.Done():
			a.shutdown()
			return nil
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			a.onEvent(ev)
		case fn := <-a.post:
			fn()
		case <-a.timer.C:
			a.ctl.Advance(a.now())
		}
		a.runActions()
		a.publish()
	}
}

func (a *App) shutdown() {
	st := &a.d.Store.State
	for id, s := range a.screens {
		if p, ok := s.r.(interface{ Position() float64 }); ok && s.item != nil {
			st.Positions[string(id)+"|"+s.item.ID] = p.Position()
		}
		a.closeScreen(s, true)
	}
	st.CleanShutdown = true
	st.Starts = nil
	a.saveState()
	a.d.Store.FlushAll()
	_ = a.d.Host.Close()
	a.log.Info("exit")
}

// ---- 显示器与表面 ----

func (a *App) syncDisplays() {
	want := map[desktop.DisplayID]desktop.Display{}
	for _, d := range a.d.Host.Displays() {
		want[d.ID] = d
	}
	for id, s := range a.screens {
		if d, ok := want[id]; !ok || d.Bounds != s.d.Bounds {
			a.log.Info("display removed/changed", "display", id)
			a.closeScreen(s, true)
			a.ctl.RemoveDisplay(a.now(), s.n)
			delete(a.screens, id)
		}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, sid := range ids {
		id := desktop.DisplayID(sid)
		if _, ok := a.screens[id]; ok {
			continue
		}
		s := &screen{n: a.nextN, d: want[id], ops: make(chan func(), 64), quit: make(chan struct{})}
		a.nextN++
		go worker(s.ops, s.quit)
		a.screens[id] = s
		a.ctl.AddDisplay(a.now(), s.n)
		if r := a.perDisp[id]; r != 0 {
			a.replayDisplay(s, r)
		}
		a.log.Info("display added", "display", id, "bounds", s.d.Bounds.String(), "primary", s.d.Primary)
		if a.d.SafeMode {
			continue
		}
		surf, err := a.d.Host.CreateSurface(id)
		if err != nil {
			a.lastErr = "创建壁纸窗口失败: " + err.Error()
			a.log.Error("surface", "display", id, "err", err)
			continue
		}
		s.surf = surf
		a.assignItem(s)
		a.startScreen(s, 0)
	}
}

func worker(ops <-chan func(), quit <-chan struct{}) {
	for {
		select {
		case op := <-ops:
			op()
		case <-quit:
			for {
				select {
				case op := <-ops:
					op()
				default:
					return
				}
			}
		}
	}
}

func (a *App) assignItem(s *screen) {
	id := a.d.Store.Assign.For(string(s.d.ID))
	s.item = nil
	if id != "" {
		if it := a.d.Store.Library.Find(id); it != nil {
			cp := *it
			s.item = &cp
		}
	}
	if s.item != nil && s.item.Snapshot != "" {
		path, host, did := s.item.Snapshot, a.d.Host, s.d.ID
		s.ops <- func() {
			if err := host.SetSystemWallpaper(did, path); err != nil {
				a.log.Warn("set system wallpaper", "display", did, "err", err)
			}
		}
	}
}

// closeScreen 结束渲染器；destroySurface=true 时也关闭表面和 worker。
func (a *App) closeScreen(s *screen, destroySurface bool) {
	r, surf := s.r, s.surf
	s.r, s.started = nil, false
	s.gen++
	if destroySurface {
		s.surf = nil
	}
	done := make(chan struct{})
	s.ops <- func() {
		if r != nil {
			_ = r.Close()
		}
		if destroySurface && surf != nil {
			_ = surf.Close()
		}
		close(done)
	}
	if destroySurface {
		close(s.quit)
		select { // 有界等待，避免卡住事件循环
		case <-done:
		case <-time.After(3 * time.Second):
			a.log.Warn("close screen timed out", "display", s.d.ID)
		}
	}
}

// startScreen 在 delay 后启动渲染器（结果回到事件循环）。
func (a *App) startScreen(s *screen, delay time.Duration) {
	if s.surf == nil || a.d.SafeMode {
		return
	}
	if s.item == nil {
		_ = s.surf.SetVisible(false) // 没有分配壁纸：露出系统壁纸
		return
	}
	if s.r != nil {
		old := s.r
		s.ops <- func() { _ = old.Close() }
	}
	s.gen++
	gen := s.gen
	level := a.ctl.Display(s.n).Level()
	r := a.d.NewRenderer(s.item.Type, a.d.Store.Config, s.d.ID)
	s.r, s.started = r, false
	it := render.Item{ID: s.item.ID, Kind: s.item.Type, Path: filepath.Join(s.item.Dir, filepath.FromSlash(s.item.Entry)),
		Start: a.d.Store.State.Positions[string(s.d.ID)+"|"+s.item.ID]}
	surf := s.surf
	launch := func() {
		s.ops <- func() {
			if level < visibility.L4 {
				_ = surf.SetVisible(true)
			}
			err := r.Load(it, surf, level > visibility.L0)
			a.Do(func() { a.onStarted(s, gen, r, err) })
		}
	}
	if delay > 0 {
		time.AfterFunc(delay, func() {
			a.Do(func() {
				if s.gen == gen {
					launch()
				}
			})
		})
	} else {
		launch()
	}
}

func (a *App) onStarted(s *screen, gen int, r render.Renderer, err error) {
	if s.gen != gen {
		_ = r.Close() // 已被取代
		return
	}
	m := a.ctl.Display(s.n)
	t := a.now()
	if err != nil {
		a.log.Error("renderer start failed", "display", s.d.ID, "err", err)
		a.lastErr = "渲染器启动失败: " + err.Error()
		s.r = nil
		if m.State() == visibility.StateRecovering {
			m.RestartFailed(t)
		} else {
			m.RendererDone(t)
		}
		return
	}
	s.started = true
	s.okAt = time.Now()
	a.log.Info("renderer started", "display", s.d.ID, "backend", r.Stats().Backend, "item", s.item.ID)
	go func() {
		e := <-r.Done()
		a.Do(func() { a.onDone(s, gen, e) })
	}()
	if m.State() == visibility.StateRecovering {
		m.RestartOK(t)
	} else {
		a.applyLevel(s, m.Level())
	}
}

func (a *App) onDone(s *screen, gen int, err error) {
	if s.gen != gen {
		return
	}
	a.log.Warn("renderer died", "display", s.d.ID, "err", err)
	s.started = false
	if time.Since(s.okAt) > supervisor.StableAfter {
		s.crashes = 0
	}
	a.ctl.Display(s.n).RendererDone(a.now())
}

func (a *App) applyLevel(s *screen, l visibility.Level) {
	r := s.r
	if r == nil {
		return
	}
	if l >= visibility.L4 {
		a.closeScreen(s, false)
		if s.surf != nil {
			surf := s.surf
			s.ops <- func() { _ = surf.SetVisible(false) }
		}
		return
	}
	s.ops <- func() {
		if err := r.SetLevel(l); err != nil {
			a.log.Warn("setLevel", "display", s.d.ID, "level", l.String(), "err", err)
		}
	}
}

// ---- 状态机动作 ----

func (a *App) runActions() {
	for _, s := range a.sortedScreens() {
		m := a.ctl.Display(s.n)
		if m == nil {
			continue
		}
		for _, act := range m.Drain() {
			a.log.Info("vis", "display", s.d.ID, "action", act.Kind.String(), "level", m.Level().String(),
				"eff", reasonsString(m.Effective()))
			switch act.Kind {
			case visibility.ActCaptureFrame:
				// 首帧截图在导入时已生成并设为系统壁纸（2.5），这里不再每次截帧
			case visibility.ActPause, visibility.ActFreeze:
				a.applyLevel(s, visibility.L1)
			case visibility.ActRelease:
				a.applyLevel(s, visibility.L3)
			case visibility.ActSnapshot:
				a.applyLevel(s, visibility.L4)
			case visibility.ActResume:
				if act.Level == visibility.L4 || s.r == nil {
					a.startScreen(s, 0)
				} else {
					a.applyLevel(s, visibility.L0)
				}
			case visibility.ActFPSCap:
				if r := s.r; r != nil {
					fps := act.FPS
					s.ops <- func() {
						if err := r.SetFPSCap(fps); err != nil {
							a.log.Info("fpsCap not applied", "display", s.d.ID, "fps", fps, "err", err)
						}
					}
				}
			case visibility.ActHealthCheck:
				a.healthCheck("resume")
			case visibility.ActRestart:
				d := supervisor.Backoff(s.crashes)
				s.crashes++
				a.log.Info("restart scheduled", "display", s.d.ID, "in", d.String(), "n", s.crashes)
				if s.r != nil {
					old := s.r
					s.ops <- func() { _ = old.Close() }
					s.r = nil
				}
				a.startScreen(s, d)
			case visibility.ActRestartedAt:
				a.applyLevel(s, act.Level)
			case visibility.ActFail:
				a.lastErr = fmt.Sprintf("显示器 %s 上的壁纸 10 分钟内崩溃 %d 次，已停用并显示截图", s.d.Name, VisConfig(a.d.Store.Config).CrashLimit)
				a.closeScreen(s, false)
				if s.surf != nil {
					surf := s.surf
					s.ops <- func() { _ = surf.SetVisible(false) }
				}
			case visibility.ActDestroy:
			}
		}
	}
	a.timer.Stop()
	if d, ok := a.ctl.NextDeadline(); ok {
		w := d - a.now()
		if w < time.Millisecond {
			w = time.Millisecond
		}
		a.timer.Reset(w)
	}
}

func (a *App) sortedScreens() []*screen {
	out := make([]*screen, 0, len(a.screens))
	for _, s := range a.screens {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	return out
}

func reasonsString(r visibility.Reason) string {
	out := ""
	for b := visibility.Reason(1); b != 0 && b <= visibility.LowBattery; b <<= 1 {
		if r&b != 0 {
			if out != "" {
				out += "|"
			}
			out += b.String()
		}
	}
	if out == "" {
		return "-"
	}
	return out
}

// ---- 宿主事件 ----

func (a *App) onEvent(ev desktop.Event) {
	switch ev.Kind {
	case desktop.EventDisplaysChanged:
		a.syncDisplays()
	case desktop.EventShellRestarted:
		a.reattachAll(ev.Source)
	case desktop.EventBattery:
		a.battPct = ev.Percent
		a.updateLowBattery()
	case desktop.EventSignal:
		a.signal(ev.Reason, ev.Display, ev.Active, ev.Source)
	}
}

func (a *App) signal(r visibility.Reason, disp desktop.DisplayID, on bool, src string) {
	cfg := a.d.Store.Config.Pause
	if r == visibility.Fullscreen && !cfg.OnFullscreen || r == visibility.Covered && !cfg.OnCovered {
		on = false
	}
	if r == visibility.Battery {
		a.onBattery = on
	}
	idx := -1
	if disp != "" {
		s := a.screens[disp]
		if s == nil {
			return
		}
		idx = s.n
		if on {
			a.perDisp[disp] |= r
		} else {
			a.perDisp[disp] &^= r
		}
	} else {
		a.global[r] = on
	}
	if err := a.ctl.Set(a.now(), r, idx, on); err != nil {
		a.log.Warn("signal", "reason", r.String(), "err", err)
		return
	}
	a.log.Debug("signal", "src", src, "display", disp, "reason", r.String(), "on", on)
	if r == visibility.Battery {
		a.updateLowBattery()
	}
}

func (a *App) updateLowBattery() {
	low := a.onBattery && a.battPct >= 0 && a.battPct <= a.d.Store.Config.Pause.LowBatteryPct
	if a.global[visibility.LowBattery] != low {
		a.global[visibility.LowBattery] = low
		_ = a.ctl.Set(a.now(), visibility.LowBattery, -1, low)
	}
}

func (a *App) replayDisplay(s *screen, r visibility.Reason) {
	for b := visibility.Reason(1); b != 0 && b <= visibility.LowBattery; b <<= 1 {
		if r&b != 0 {
			_ = a.ctl.Set(a.now(), b, s.n, true)
		}
	}
}

func (a *App) healthCheck(why string) {
	if a.d.SafeMode {
		return
	}
	if err := a.d.Host.Health(); err != nil {
		a.log.Warn("health check failed", "why", why, "err", err)
		a.reattachAll("health")
	}
}

// reattachAll：Explorer 重启或挂载失效。销毁全部表面，按 0/1/2/4/8/16 s 退避重建（Q-P2：不阻塞）。
func (a *App) reattachAll(why string) {
	a.log.Info("reattach", "why", why, "try", a.reattach)
	for _, s := range a.screens {
		a.closeScreen(s, false)
		if s.surf != nil {
			surf := s.surf
			s.surf = nil
			s.ops <- func() { _ = surf.Close() }
		}
	}
	back := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	d := back[min(a.reattach, len(back)-1)]
	time.AfterFunc(d, func() { a.Do(a.reattachAttempt) })
}

func (a *App) reattachAttempt() {
	failed := false
	for _, s := range a.sortedScreens() {
		if s.surf != nil {
			continue
		}
		surf, err := a.d.Host.CreateSurface(s.d.ID)
		if err != nil {
			a.log.Warn("reattach surface", "display", s.d.ID, "err", err)
			failed = true
			continue
		}
		s.surf = surf
		if a.ctl.Display(s.n).Level() < visibility.L4 {
			a.startScreen(s, 0)
		}
	}
	if failed {
		a.reattach++
		if a.reattach > 6 {
			a.lastErr = "无法重新挂载到桌面，已降级为系统壁纸"
			a.reattach = 0
			return
		}
		back := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
		time.AfterFunc(back[min(a.reattach, len(back)-1)], func() { a.Do(a.reattachAttempt) })
		return
	}
	a.reattach = 0
}

// ---- 持久化 ----

func (a *App) saveConfig() {
	c := a.d.Store.Config
	a.d.Store.ConfigSaver.Schedule(func() any { return c })
}

func (a *App) saveLibrary() {
	l := config.Library{SchemaVersion: config.LibraryVersion, Items: append([]config.Item(nil), a.d.Store.Library.Items...)}
	a.d.Store.LibrarySaver.Schedule(func() any { return l })
}

func (a *App) saveAssign() {
	m := map[string]string{}
	for k, v := range a.d.Store.Assign.Displays {
		m[k] = v
	}
	as := config.Assignments{SchemaVersion: config.AssignmentsVersion, Default: a.d.Store.Assign.Default, Displays: m}
	a.d.Store.AssignSaver.Schedule(func() any { return as })
}

func (a *App) saveState() {
	st := a.d.Store.State
	pos := map[string]float64{}
	for k, v := range st.Positions {
		pos[k] = v
	}
	st.Positions = pos
	st.Starts = append([]time.Time(nil), st.Starts...)
	a.d.Store.StateSaver.Schedule(func() any { return st })
}

// ---- 视图 ----

func levelName(m *visibility.Machine) (string, string) {
	if m == nil {
		return "-", "idle"
	}
	st := "active"
	switch m.State() {
	case visibility.StateRecovering:
		st = "recovering"
	case visibility.StateFailed:
		st = "failed"
	case visibility.StateDestroyed:
		st = "destroyed"
	}
	return m.Level().String(), st
}

func (a *App) publish() {
	v := View{Config: a.d.Store.Config, PausedAll: a.pausedAll, SafeMode: a.d.SafeMode, Importing: a.importing,
		LastImport: a.lastImp, LastError: a.lastErr, ReadOnly: a.d.Store.ReadOnlyWarn,
		Library: append([]config.Item(nil), a.d.Store.Library.Items...)}
	for _, s := range a.sortedScreens() {
		lv, st := levelName(a.ctl.Display(s.n))
		dv := DisplayView{ID: string(s.d.ID), Name: s.d.Name, W: s.d.Bounds.Dx(), H: s.d.Bounds.Dy(), Primary: s.d.Primary,
			Level: lv, State: st}
		if s.item != nil {
			dv.ItemID = s.item.ID
		}
		if s.r != nil {
			stt := s.r.Stats()
			dv.Backend, dv.HWDec, dv.Dropped = stt.Backend, stt.HWDec, stt.Dropped
		}
		if s.item == nil && st == "active" {
			dv.State = "idle"
		}
		v.Displays = append(v.Displays, dv)
	}
	a.vmu.Lock()
	a.view = v
	cbs := append([]func(){}, a.onChange...)
	a.vmu.Unlock()
	for _, f := range cbs {
		f()
	}
}

// ---- 公开命令（任意 goroutine） ----

// SetPausedAll 对应托盘“暂停全部”（07 章的 User 原因，全局）。
func (a *App) SetPausedAll(on bool) {
	a.Do(func() {
		a.pausedAll = on
		_ = a.ctl.Set(a.now(), visibility.User, -1, on)
	})
}

// Assign 设置某显示器的壁纸；display 为空表示设为默认（所有未单独设置的显示器）。itemID 为空表示清除。
func (a *App) Assign(display, itemID string) {
	a.Do(func() {
		if display == "" {
			a.d.Store.Assign.Default = itemID
		} else if itemID == "" {
			delete(a.d.Store.Assign.Displays, display)
		} else {
			a.d.Store.Assign.Displays[display] = itemID
		}
		a.saveAssign()
		for _, s := range a.sortedScreens() {
			if display != "" && string(s.d.ID) != display {
				continue
			}
			old := ""
			if s.item != nil {
				old = s.item.ID
			}
			a.assignItem(s)
			now := ""
			if s.item != nil {
				now = s.item.ID
			}
			if old == now && s.r != nil {
				continue
			}
			s.crashes = 0
			if m := a.ctl.Display(s.n); m.State() == visibility.StateFailed {
				m.Retry(a.now()) // 7.3：更换壁纸视同重试
				continue
			}
			if s.r != nil {
				a.closeScreen(s, false)
			}
			if now == "" && s.surf != nil {
				surf := s.surf
				s.ops <- func() { _ = surf.SetVisible(false) }
				continue
			}
			if a.ctl.Display(s.n).Level() < visibility.L4 {
				a.startScreen(s, 0)
			}
		}
	})
}

// Import 异步导入；完成后加入库，并在库为空时设为默认壁纸。
func (a *App) Import(path string) {
	a.Do(func() {
		if a.d.Import == nil {
			return
		}
		a.importing = true
		a.lastImp = "正在导入 " + filepath.Base(path) + " …"
		go func() {
			res, err := a.d.Import(context.Background(), path)
			a.Do(func() {
				a.importing = false
				if err != nil {
					a.lastImp = "导入失败: " + err.Error()
					a.log.Warn("import failed", "file", telemetry.HashPath(path), "err", err)
					return
				}
				first := len(a.d.Store.Library.Items) == 0
				a.d.Store.Library.Upsert(res.Item)
				a.saveLibrary()
				a.lastImp = "已导入「" + res.Item.Title + "」"
				if len(res.Warnings) > 0 {
					a.lastImp += "（" + joinNotes(res.Warnings) + "）"
				}
				a.log.Info("imported", "id", res.Item.ID, "type", res.Item.Type, "codec", res.Item.Codec, "transcode", res.Item.Transcode)
				if first && a.d.Store.Assign.Default == "" {
					go a.Assign("", res.Item.ID)
				}
			})
		}()
	})
}

func joinNotes(n []string) string {
	s := ""
	for i, x := range n {
		if i > 0 {
			s += "；"
		}
		s += x
	}
	return s
}

// Remove 从库中删除壁纸（同时删除包目录与缓存）。
func (a *App) Remove(itemID string) {
	a.Do(func() {
		it := a.d.Store.Library.Find(itemID)
		if it == nil {
			return
		}
		dir := it.Dir
		snapDir := ""
		if it.Snapshot != "" {
			snapDir = filepath.Dir(it.Snapshot)
		}
		a.d.Store.Library.Remove(itemID)
		a.saveLibrary()
		if a.d.Store.Assign.Default == itemID {
			a.d.Store.Assign.Default = ""
		}
		for k, v := range a.d.Store.Assign.Displays {
			if v == itemID {
				delete(a.d.Store.Assign.Displays, k)
			}
		}
		a.saveAssign()
		for _, s := range a.screens {
			if s.item != nil && s.item.ID == itemID {
				a.closeScreen(s, false)
				s.item = nil
				if s.surf != nil {
					surf := s.surf
					s.ops <- func() { _ = surf.SetVisible(false) }
				}
			}
		}
		go func() {
			_ = os.RemoveAll(dir)
			if snapDir != "" {
				_ = os.RemoveAll(snapDir)
			}
		}()
	})
}

// UpdateConfig 修改配置：暂停策略变化会重建状态机并回放当前信号；后端变化会重启渲染器。
func (a *App) UpdateConfig(fn func(*config.Config)) {
	a.Do(func() {
		old := a.d.Store.Config
		fn(&a.d.Store.Config)
		a.d.Store.Config.Normalize()
		c := a.d.Store.Config
		if c.Startup.LaunchAtLogin != old.Startup.LaunchAtLogin && a.d.SetLaunchAtLogin != nil {
			if err := a.d.SetLaunchAtLogin(c.Startup.LaunchAtLogin); err != nil {
				a.lastErr = "设置开机自启失败: " + err.Error()
				a.d.Store.Config.Startup.LaunchAtLogin = old.Startup.LaunchAtLogin
			}
		}
		a.saveConfig()
		if c.Pause != old.Pause {
			a.rebuildMachines()
		}
		if c.Render != old.Render {
			for _, s := range a.sortedScreens() {
				if s.r != nil {
					a.closeScreen(s, false)
					if a.ctl.Display(s.n).Level() < visibility.L4 {
						a.startScreen(s, 0)
					}
				}
			}
		}
	})
}

func (a *App) rebuildMachines() {
	now := a.now()
	a.ctl = visibility.NewController(VisConfig(a.d.Store.Config))
	for _, s := range a.sortedScreens() {
		a.ctl.AddDisplay(now, s.n)
	}
	for r, on := range a.global {
		if on {
			_ = a.ctl.Set(now, r, -1, true)
		}
	}
	for id, r := range a.perDisp {
		if s := a.screens[id]; s != nil {
			a.replayDisplay(s, r)
		}
	}
	a.ctl.Advance(now)
	for _, s := range a.sortedScreens() {
		a.ctl.Display(s.n).Drain()
		l := a.ctl.Display(s.n).Level()
		switch {
		case s.r == nil && l < visibility.L4:
			a.startScreen(s, 0)
		case s.r != nil:
			a.applyLevel(s, l)
		}
	}
}

// ExitSafeMode 清除崩溃记录并重新加载壁纸。
func (a *App) ExitSafeMode() {
	a.Do(func() {
		if !a.d.SafeMode {
			return
		}
		a.d.SafeMode = false
		a.d.Store.State.Starts = nil
		a.d.Store.State.SafeMode = false
		a.saveState()
		for _, s := range a.sortedScreens() {
			if s.surf == nil {
				surf, err := a.d.Host.CreateSurface(s.d.ID)
				if err != nil {
					a.lastErr = err.Error()
					continue
				}
				s.surf = surf
			}
			a.assignItem(s)
			a.startScreen(s, 0)
		}
	})
}

// Wait 等待事件循环结束。
func (a *App) Wait() { <-a.stopped }

// ErrNoDisplays 在宿主没有显示器时返回。
var ErrNoDisplays = errors.New("app: no displays")
