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
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/media"
	"wpapp/internal/render"
	"wpapp/internal/supervisor"
	"wpapp/internal/telemetry"
	"wpapp/internal/transcode"
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
	// Transcode 是 F-07 的系统/用户编码器（transcode.Encoder）；nil 表示本机没有可用编码器。
	Transcode func(ctx context.Context, src, dst string) error
	// TranscodeExt 是转码产物扩展名（mac avconvert 输出 .mov，其余 .mp4）。
	TranscodeExt    string
	EncodeRendition func(context.Context, string, string, transcode.Target) error
	// ResolveEncoder resolves user-selected encoders again when FFmpegPath changes.
	ResolveEncoder func(config.Config) (func(context.Context, string, string) error, string)
	EncoderInfo    func(config.Config) transcode.Capabilities
	// ProbeMedia isolates generated-output parsing and refreshes library metadata.
	ProbeMedia func(context.Context, string) (*media.Info, error)
}

// DisplayView 是 UI 用的显示器快照。
type DisplayView struct {
	ID string // 稳定 ID（持久化键）
	// Index 是按物理排列（左→右、上→下）的编号，从 1 开始；UI 里的“1”“2”就是它。
	Index int
	// Name 是显示名：用户别名 → 系统友好名 → “显示器 N”。
	Name           string
	Friendly       string // 系统友好名（可能为空）
	Alias          string // 用户别名（可能为空）
	Device         string // 系统内部名（\\.\DISPLAYn），只用于诊断
	Custom         bool   // 这块屏有单独设置的壁纸（否则跟随默认壁纸）
	W, H           int    // host topology units, not necessarily physical pixels on macOS
	PixelW, PixelH int    // backing pixels for rendition sizing; never used as layout coordinates
	Primary        bool
	ItemID         string
	Level          string
	State          string // active | recovering | failed | idle
	Backend        string
	HWDec          string
	Dropped        uint64
	Metrics        DisplayMetricsView
	X, Y           int                   // 虚拟屏坐标（布局示意图）
	Reasons        string                // 当前生效的暂停/限帧原因（07 章名称，| 分隔）
	FPSCap         int                   // 当前生效的限帧，0 = 不限
	Note           string                // 例如“此后端不支持限帧”
	Options        config.DisplayOptions // 这块屏的覆盖项（空值 = 跟随壁纸）
	Effective      config.Playback       // 覆盖项 + 壁纸组属性解析后的实际值
	Playback       render.Capabilities
}

// Power 是电源快照（F-05）。
type Power struct {
	OnBattery bool
	Percent   int // -1 = 未知
	Saver     bool
	Thermal   string // "" | ThermalSerious | ThermalCritical
}

// View 是 UI 用的整体快照。
type View struct {
	SignalConsent  *SignalConsent
	NetworkConsent *NetworkConsent
	Renditions     []RenditionView
	Layout         string // effective independent | span
	LayoutNote     string // unsupported saved preference/topology

	ImportQueued     int
	Capabilities     desktop.Caps
	Paths            config.Paths
	Storage          StorageView
	ForegroundAction string
	Displays         []DisplayView
	Library          []config.Item
	Config           config.Config
	PausedAll        bool
	SafeMode         bool
	Importing        bool
	LastImport       string
	LastError        string
	ReadOnly         []string
	Power            Power
	Transcoding      map[string]string // itemID → 状态文本（正在转码…/失败原因）
	HasEncoder       bool
	Encoder          transcode.Capabilities
	Default          string // 默认壁纸 id（assignments.default）
}

// screenWorker is allocated with its screen; only the serial ops worker may
// access its contents. Main-loop screen copies must never dereference it.
type screenWorker struct {
	level visibility.Level
}

type screen struct {
	mediaPath string
	canvas    image.Rectangle // empty = independent native surface

	n              int // 状态机里的编号（单调递增，不复用）
	d              desktop.Display
	surf           desktop.Surface
	r              render.Renderer
	gen            int
	item           *config.Item
	started        bool
	metricsReady   bool // successfully acknowledged renderer state (including policy L4)
	metricsLevel   visibility.Level
	levelPending   int
	levelFailures  int
	levelNote      string
	levelExhausted bool
	worker         *screenWorker // immutable pointer; contents owned by the ops worker
	crashes        int
	okAt           time.Time
	machCap        int // 状态机要求的限帧（电池 fps15 / 过热）
	curCap         int // 已下发给渲染器的限帧
	note           string
	ops            chan func()
	quit           chan struct{}
	watchStop      chan struct{}
}

// App 是编排器。
type App struct {
	signalConsent  *SignalConsent
	signalAnswer   chan bool
	networkConsent *NetworkConsent
	networkAnswer  chan bool
	itemGeneration map[string]uint64
	removing       map[string]uint64
	d              Deps
	encoderInfo    transcode.Capabilities
	log            *slog.Logger
	t0             time.Time
	metrics        runtimeMetrics
	ctl            *visibility.Controller
	screens        map[desktop.DisplayID]*screen
	spanOwner      desktop.DisplayID // pinned through teardown; never falls to a removed owner's secondary
	nextN          int
	post           chan func()
	timer          *time.Timer
	// 原始信号（用于配置变化后重建状态机时回放）
	global           map[visibility.Reason]bool
	perDisp          map[desktop.DisplayID]visibility.Reason
	onBattery        bool
	battPct          int
	pausedAll        bool
	foreground       string
	foregroundAction string
	storage          StorageView
	storageDirty     bool
	ctx              context.Context
	importing        bool
	importCancel     context.CancelFunc
	importCancelled  bool
	importQueue      []importRequest
	renditionQueue   []*renditionJob
	renditionJobs    map[string]*renditionJob
	activeRendition  *renditionJob
	transcodeQueue   []string
	transcodingID    string
	transcodeCancel  context.CancelFunc
	transcodeDone    <-chan struct{}
	cleaning         bool
	lastImp          string
	lastErr          string
	reattach         int
	transc           map[string]string
	power            map[visibility.Reason]bool

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
	var encoderInfo transcode.Capabilities
	if d.EncoderInfo != nil {
		encoderInfo = d.EncoderInfo(d.Store.Config)
	}
	return &App{encoderInfo: encoderInfo, itemGeneration: map[string]uint64{}, removing: map[string]uint64{}, d: d, log: d.Log, screens: map[desktop.DisplayID]*screen{}, post: make(chan func(), 256),
		global: map[visibility.Reason]bool{}, perDisp: map[desktop.DisplayID]visibility.Reason{}, battPct: -1,
		transc: map[string]string{}, power: map[visibility.Reason]bool{}, stopped: make(chan struct{})}
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
	return cloneView(a.view)
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
	a.ctx = ctx
	a.ctl = visibility.NewController(VisConfig(a.d.Store.Config))
	a.timer = time.NewTimer(time.Hour)
	a.timer.Stop()
	defer close(a.stopped)
	if err := a.d.Host.Start(); err != nil {
		return fmt.Errorf("desktop host: %w", err)
	}
	a.log.Info("start", "safeMode", a.d.SafeMode, "displays", len(a.d.Host.Displays()), "caps", fmt.Sprintf("%+v", a.d.Host.Caps()))
	a.syncDisplays()
	a.refreshStorage()
	a.publish()
	events := a.d.Host.Events()
	checkpoint := time.NewTicker(30 * time.Second)
	defer checkpoint.Stop()
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
		case <-checkpoint.C:
			a.checkpointPositions()
		}
		a.runActions()
		a.publish()
	}
}

func (a *App) shutdown() {
	a.sampleMetrics(time.Now())
	a.importQueue = nil
	a.transcodeQueue = nil
	if a.transcodeCancel != nil {
		a.transcodeCancel()
	}
	st := &a.d.Store.State
	a.checkpointPositions()
	for _, s := range a.screens {
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
	canvas, _ := a.desiredCanvas()
	want := map[desktop.DisplayID]desktop.Display{}
	for _, d := range a.d.Host.Displays() {
		want[d.ID] = d
	}
	if a.migrateIDs(want) {
		a.saveAssign()
	}
	// Keep the old owner pinned while screens are removed. Recomputing from a
	// shrinking map lets a paused secondary overwrite shared resume history.
	if old := a.spanLeader(); old != nil {
		a.spanOwner = old.d.ID
	}
	newOwner := topologyOwner(want)
	ownerChanged := !canvas.Empty() && a.spanOwner != newOwner
	for id, s := range a.screens {
		if d, ok := want[id]; !ok || d.Bounds != s.d.Bounds || d.Scale != s.d.Scale || (!canvas.Empty() && d.PixelSize() != s.d.PixelSize()) || canvas != s.canvas || ownerChanged || (!canvas.Empty() && d.Primary != s.d.Primary) {
			a.log.Info("display removed/changed", "display", id)
			a.closeScreen(s, true)
			a.ctl.RemoveDisplay(a.now(), s.n)
			delete(a.screens, id)
		}
	}
	a.spanOwner = newOwner
	for id, d := range want {
		if s := a.screens[id]; s != nil {
			pixelsChanged := s.d.PixelSize() != d.PixelSize()
			s.d = d // Geometry is unchanged; backing raster may change independently.
			if pixelsChanged && s.item != nil {
				if !a.updateScreenPlayback(s) {
					a.restartPlayback(s)
				}
			}
		}
	}
	// Register the complete topology before starting any renderer. Spanning audio
	// and resume ownership must not depend on which display is visited first.
	var added []*screen
	for _, d := range layoutOrder(want) {
		id := d.ID
		if _, ok := a.screens[id]; ok {
			continue
		}
		s := &screen{worker: &screenWorker{}, canvas: canvas, n: a.nextN, d: want[id], ops: make(chan func(), 64), quit: make(chan struct{})}
		a.nextN++
		go worker(s.ops, s.quit)
		a.screens[id] = s
		a.ctl.AddDisplay(a.now(), s.n)
		if a.d.Store.Assign.OptionsFor(string(id)).Paused {
			a.perDisp[id] |= visibility.User
		}
		if r := a.perDisp[id]; r != 0 {
			a.replayDisplay(s, r)
		}
		a.log.Info("display added", "display", id, "bounds", s.d.Bounds.String(), "primary", s.d.Primary)
		added = append(added, s)
	}
	for _, s := range added {
		id := s.d.ID
		if a.d.SafeMode {
			continue
		}
		surf, err := a.createSurface(s)
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

// layoutOrder 按物理排列返回显示器（desktop.SortByLayout）。
func layoutOrder(m map[desktop.DisplayID]desktop.Display) []desktop.Display {
	ds := make([]desktop.Display, 0, len(m))
	for _, d := range m {
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
	desktop.SortByLayout(ds)
	return ds
}

// migrateIDs 把旧版 ID（例如 0.2.0 用 GDI 名算出的 ID）下的设置搬到新的稳定 ID。
func (a *App) migrateIDs(want map[desktop.DisplayID]desktop.Display) bool {
	changed := false
	for _, d := range layoutOrder(want) {
		var legacy []string
		for _, l := range d.LegacyIDs {
			if _, live := want[l]; !live { // 旧 ID 正好是另一块在线屏的 ID 时不动
				legacy = append(legacy, string(l))
			}
		}
		if a.d.Store.Assign.Migrate(string(d.ID), legacy) {
			a.log.Info("display id migrated", "display", d.ID, "legacy", legacy)
			changed = true
		}
		positionsChanged := false
		for _, old := range legacy {
			for key, pos := range a.d.Store.State.Positions {
				if strings.HasPrefix(key, old+"|") {
					next := string(d.ID) + strings.TrimPrefix(key, old)
					if _, exists := a.d.Store.State.Positions[next]; !exists {
						a.d.Store.State.Positions[next] = pos
					}
					delete(a.d.Store.State.Positions, key)
					positionsChanged = true
				}
			}
		}
		if positionsChanged {
			a.saveState()
		}

	}
	return changed
}

// DisplayName 是显示名规则：别名 → 友好名 → “显示器 N”。
func DisplayName(alias, friendly string, index int) string {
	if alias != "" {
		return alias
	}
	if friendly != "" {
		return friendly
	}
	return fmt.Sprintf("显示器 %d", index)
}

// layoutScreens 按物理排列返回屏幕（UI 编号顺序）。
func (a *App) layoutScreens() []*screen {
	out := a.sortedScreens()
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i].d.Bounds.Min, out[j].d.Bounds.Min
		if x.X != y.X {
			return x.X < y.X
		}
		return x.Y < y.Y
	})
	return out
}

func (a *App) labelOf(s *screen) string {
	for i, x := range a.layoutScreens() {
		if x == s {
			return DisplayName(a.d.Store.Assign.Aliases[string(s.d.ID)], s.d.Name, i+1)
		}
	}
	return DisplayName("", s.d.Name, 0)
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
	id := a.assignmentFor(s)
	s.item = nil
	if id != "" {
		if it := a.d.Store.Library.Find(id); it != nil {
			cp := *it
			s.item = &cp
			if m := a.ctl.Display(s.n); m != nil {
				kind := visibility.Video
				if cp.Type == "web" {
					kind = visibility.Web
				} else if cp.Type == "shader" {
					kind = visibility.Shader
				}
				m.SetKind(kind)
			}
		}
	}
	a.installSystemWallpaper(s)
}

// closeScreen 结束渲染器；destroySurface=true 时也关闭表面和 worker。
func (a *App) closeScreen(s *screen, destroySurface bool) {
	a.closeScreenForPause(s, destroySurface, false)
}

func (a *App) closeScreenForPause(s *screen, destroySurface, policyPause bool) {
	countStopped := policyPause
	r, surf := s.r, s.surf
	a.capturePosition(s)
	if s.watchStop != nil {
		close(s.watchStop)
		s.watchStop = nil
	}
	s.r, s.started, s.metricsReady = nil, false, false
	s.levelPending = 0
	s.gen++
	gen := s.gen
	if destroySurface {
		s.surf = nil
	}
	id := s.d.ID
	done := make(chan struct{})
	s.ops <- func() {
		var closeErr error
		begin := time.Now()
		from := s.worker.level
		if r != nil {
			closeErr = r.Close()
		}
		if policyPause {
			if closeErr == nil {
				s.worker.level = visibility.L4
			}
			backend := "none"
			if r != nil {
				backend = r.Stats().Backend
			}
			a.log.Info("renderer level transition", "display", id, "backend", backend, "from", from.String(), "to", "L4", "duration", time.Since(begin), "err", closeErr)
			if closeErr != nil {
				a.log.Error("renderer stop failed", "display", id, "err", closeErr)
				a.Do(func() {
					if s.gen == gen {
						s.levelNote = "播放状态恢复失败，请重新应用壁纸"
					}
				})
			}
		}
		if countStopped && closeErr == nil {
			a.Do(func() {
				if s.gen == gen {
					s.metricsReady, s.metricsLevel = true, visibility.L4
				}
			})
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
			a.log.Warn("close screen timed out", "display", id)
		}
	}
}

// startScreen 在 delay 后启动渲染器（结果回到事件循环）。
func (a *App) startScreen(s *screen, delay time.Duration) {
	if s.levelExhausted {
		s.levelFailures, s.levelExhausted = 0, false
	}
	if s.surf == nil || a.d.SafeMode {
		return
	}
	if s.item != nil {
		if _, pending := a.removing[s.item.ID]; pending {
			return
		}
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
	if r == nil {
		a.lastErr = "当前壁纸类型没有可用渲染器"
		return
	}
	if s.watchStop != nil {
		close(s.watchStop)
	}
	s.watchStop = make(chan struct{})
	s.r, s.started, s.metricsReady = r, false, false
	s.levelPending = 0
	it := render.Item{PackageDir: s.item.Dir, ID: s.item.ID, Kind: s.item.Type, Path: a.renditionPath(s, true),
		SignalsBlocked: a.signalsBlocked(s), AudioEnabled: signalGrant(s.item, a.d.Store.Config, "audio"), InputEnabled: signalGrant(s.item, a.d.Store.Config, "input"), Viewport: s.d.Bounds,
		Start: a.d.Store.State.Positions[a.positionKey(s)], NetworkGranted: s.item.NetworkGranted, FPSCap: a.screenFPSCap(s), Properties: s.item.Props.WebValues}
	if !a.d.Store.Config.Render.ResumePosition {
		it.Start = 0
	}
	if cp, ok := r.(render.CapabilityProvider); ok && !cp.Capabilities().Resume {
		it.Start = 0
	}
	s.mediaPath = it.Path
	options, note := a.playbackOptions(s, r)
	s.note = note
	surf := s.surf
	launch := func() {
		s.ops <- func() {
			if level < visibility.L4 {
				_ = surf.SetVisible(true)
			}
			var err error
			if c, ok := r.(render.Configurable); ok {
				err = c.Configure(options)
			}
			if err == nil {
				err = r.Load(it, surf, level > visibility.L0)
				if err == nil {
					s.worker.level = visibility.L0
					if level > visibility.L0 {
						s.worker.level = visibility.L1
					}
				}
			}
			a.Do(func() { a.onStarted(s, gen, r, level, err) })
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

func (a *App) onStarted(s *screen, gen int, r render.Renderer, loadedLevel visibility.Level, err error) {
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
	s.started, s.metricsReady = true, true
	s.metricsLevel = visibility.L0
	if loadedLevel > visibility.L0 {
		s.metricsLevel = visibility.L1 // Load(startPaused) is confirmed, not L3/L4 yet.
	}
	s.okAt = time.Now()
	a.log.Info("renderer started", "display", s.d.ID, "backend", r.Stats().Backend, "item", s.item.ID)
	watchStop := s.watchStop
	go func() {
		select {
		case e := <-r.Done():
			a.Do(func() { a.onDone(s, gen, e) })
		case <-watchStop:
		case <-a.ctx.Done():
		}
	}()
	s.curCap = 0
	a.applyCap(s)
	if m.State() == visibility.StateRecovering {
		m.RestartOK(t)
	} else {
		a.applyLevel(s, m.Level())
	}
}

// ProfileCap combines the explicit FPS ceiling with the saver profile (15 fps).
func ProfileCap(c config.Config) int {
	if c.Perf.Profile == "saver" {
		return EffectiveCap(15, c.Perf.FPSCap)
	}
	return c.Perf.FPSCap
}

// EffectiveCap 取两个限帧里更严格的一个（0 = 不限）。
func EffectiveCap(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return min(a, b)
}

// applyCap 把“档位 ∧ 状态机”的限帧下发给渲染器（F-05）。
func (a *App) screenFPSCap(s *screen) int {
	cfg := a.d.Store.Config
	if profile := a.d.Store.Assign.OptionsFor(string(s.d.ID)).Profile; profile != "" {
		cfg.Perf.Profile = profile
	}
	if s.item != nil && s.item.Type == "shader" {
		cfg.Perf.FPSCap = s.item.Props.ShaderFPS
		if cfg.Perf.FPSCap == 0 {
			cfg.Perf.FPSCap = 30
		}
	}
	want := EffectiveCap(ProfileCap(cfg), s.machCap)
	if a.foregroundAction == "saver" {
		want = EffectiveCap(want, 15)
	}
	return want
}
func (a *App) applyCap(s *screen) {
	want := a.screenFPSCap(s)
	r := s.r
	if r == nil || !s.started || want == s.curCap {
		return
	}
	s.curCap = want
	gen := s.gen
	s.ops <- func() {
		err := r.SetFPSCap(want)
		a.Do(func() {
			if s.gen != gen {
				return
			}
			switch {
			case errors.Is(err, render.ErrUnsupported) && want > 0:
				s.note = "当前渲染后端不支持限帧，按原帧率播放"
			case err != nil:
				s.note = "限帧失败：" + err.Error()
				a.log.Info("fpsCap not applied", "display", s.d.ID, "fps", want, "err", err)
			default:
				_, s.note = a.playbackOptions(s, r)
			}
		})
	}
}

func (a *App) onDone(s *screen, gen int, err error) {
	if s.gen != gen {
		return
	}
	a.log.Warn("renderer died", "display", s.d.ID, "err", err)
	s.started, s.metricsReady = false, false
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
		a.closeScreenForPause(s, false, true)
		if s.surf != nil {
			surf := s.surf
			s.ops <- func() { _ = surf.SetVisible(false) }
		}
		return
	}
	gen := s.gen
	id := s.d.ID
	queued := time.Now()
	s.levelPending++
	s.ops <- func() {
		begin := time.Now()
		from := s.worker.level
		err := r.SetLevel(l)
		if err == nil {
			s.worker.level = l
		}
		a.log.Info("renderer level transition", "display", id, "backend", r.Stats().Backend, "from", from.String(), "to", l.String(), "duration", time.Since(begin), "queueDuration", begin.Sub(queued), "err", err)
		a.Do(func() {
			if s.gen != gen {
				return
			}
			s.levelPending--
			if err != nil {
				a.log.Error("renderer level transition failed", "display", id, "level", l.String(), "err", err)
				s.levelNote = "播放状态切换失败，已尝试重启渲染器"
				if s.levelFailures < 2 {
					s.levelFailures++
					a.restartPlayback(s)
				} else {
					s.levelExhausted = true
					s.levelNote = "播放状态恢复失败，请重新应用壁纸"
					a.closeScreen(s, false)
				}
				return
			}
			if s.started {
				s.metricsReady, s.metricsLevel = true, l
			}
		})
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
			case visibility.ActPause:
				a.applyLevel(s, visibility.L1)
			case visibility.ActFreeze:
				a.applyLevel(s, visibility.L2)
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
				s.machCap = act.FPS
				a.applyCap(s)
			case visibility.ActHealthCheck:
				a.healthCheck("resume")
			case visibility.ActRestart:
				// A corrected runtime may be retried while policy requires a
				// static snapshot. Clear the failed state, but do not launch a
				// process until the normal resume transition permits it.
				if m.Level() >= visibility.L4 {
					m.RestartOK(a.now())
					a.closeScreenForPause(s, false, true)
					continue
				}
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
				a.lastErr = fmt.Sprintf("显示器 %s 上的壁纸 10 分钟内崩溃 %d 次，已停用并显示截图", a.labelOf(s), VisConfig(a.d.Store.Config).CrashLimit)
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
	case desktop.EventForegroundApp:
		a.foreground = ev.Executable
		a.updateAppRule()
	case desktop.EventDisplaysChanged:
		a.syncDisplays()
	case desktop.EventShellRestarted:
		a.reattachAll(ev.Source)
	case desktop.EventBattery:
		a.battPct = ev.Percent
		a.updateLowBattery()
	case desktop.EventSignal:
		a.signal(ev.Reason, ev.Display, ev.Active, ev.Source)
		if ev.Display == "" {
			a.power[ev.Reason] = ev.Active
		}
	}
}

func (a *App) signal(r visibility.Reason, disp desktop.DisplayID, on bool, src string) {
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
	for _, screen := range a.sortedScreens() {
		if updater, ok := screen.r.(render.SignalPolicyUpdater); ok {
			blocked := a.signalsBlocked(screen)
			screen.ops <- func() { _ = updater.BlockSignals(blocked) }
		}
	}
	effective := on
	if idx >= 0 {
		effective = on && a.reasonEnabled(r)
	}
	if err := a.ctl.Set(a.now(), r, idx, effective); err != nil {
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
		if r&b != 0 && a.reasonEnabled(b) {
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
		surf, err := a.createSurface(s)
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
	c := a.d.Store.Config.Clone()
	a.d.Store.ConfigSaver.Schedule(func() any { return c })
}

func (a *App) saveLibrary() {
	l := config.Library{SchemaVersion: config.LibraryVersion, Items: append([]config.Item(nil), a.d.Store.Library.Items...)}
	for i := range l.Items {
		l.Items[i] = l.Items[i].Clone()
	}
	a.d.Store.LibrarySaver.Schedule(func() any { return l })
}

func (a *App) saveAssign() {
	m := map[string]string{}
	for k, v := range a.d.Store.Assign.Displays {
		m[k] = v
	}
	al := map[string]string{}
	for k, v := range a.d.Store.Assign.Aliases {
		al[k] = v
	}
	opts := map[string]config.DisplayOptions{}
	for k, v := range a.d.Store.Assign.Options {
		opts[k] = v
	}
	as := config.Assignments{Layout: a.d.Store.Assign.Layout, SpanItem: a.d.Store.Assign.SpanItem, SpanOptions: a.d.Store.Assign.SpanOptions, SchemaVersion: config.AssignmentsVersion, Default: a.d.Store.Assign.Default, Displays: m, Aliases: al, Options: opts}
	a.d.Store.AssignSaver.Schedule(func() any { return as })
}

func (a *App) saveState() {
	st := a.d.Store.State.Clone()
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
	a.sampleMetrics(time.Now())
	v := View{SignalConsent: a.signalConsent, NetworkConsent: a.networkConsent, ImportQueued: len(a.importQueue), Config: a.d.Store.Config.Clone(), Capabilities: a.d.Host.Caps(), Paths: a.d.Store.Paths, Storage: a.storage, ForegroundAction: a.foregroundAction, PausedAll: a.pausedAll, SafeMode: a.d.SafeMode, Importing: a.importing,
		LastImport: a.lastImp, LastError: a.lastErr, ReadOnly: a.d.Store.ReadOnlyWarn,
		Library: append([]config.Item(nil), a.d.Store.Library.Items...), HasEncoder: a.d.Transcode != nil, Encoder: a.encoderInfo, Default: a.d.Store.Assign.Default,
		Power:       Power{OnBattery: a.onBattery, Percent: a.battPct, Saver: a.power[visibility.BatterySaver]},
		Transcoding: map[string]string{}}
	canvas, layoutNote := a.desiredCanvas()
	v.Layout, v.LayoutNote = "independent", layoutNote
	if !canvas.Empty() {
		v.Layout = "span"
		v.Default = a.d.Store.Assign.SpanItem
	}
	switch {
	case a.power[visibility.ThermalCritical]:
		v.Power.Thermal = "ThermalCritical"
	case a.power[visibility.ThermalSerious]:
		v.Power.Thermal = "ThermalSerious"
	}
	v.Renditions = a.renditionViews()
	for k, x := range a.transc {
		v.Transcoding[k] = x
	}
	for i, s := range a.layoutScreens() {
		lv, st := levelName(a.ctl.Display(s.n))
		if s.item != nil {
			lv = ""
			if s.metricsReady {
				lv = s.metricsLevel.String()
			}
			if s.levelExhausted {
				st = "failed"
			}
			if st != "failed" && ((!s.started && !(s.metricsReady && s.metricsLevel == visibility.L4)) || s.levelPending > 0 || lv != a.ctl.Display(s.n).Level().String() || (s.metricsLevel > visibility.L0 && a.ctl.Display(s.n).Effective() == 0)) {
				st = "recovering"
			}
		}
		alias := a.d.Store.Assign.Aliases[string(s.d.ID)]
		_, custom := a.d.Store.Assign.Displays[string(s.d.ID)]
		dv := DisplayView{ID: string(s.d.ID), Index: i + 1, Name: DisplayName(alias, s.d.Name, i+1), Friendly: s.d.Name, Alias: alias,
			Device: s.d.Device, Custom: custom, W: s.d.Bounds.Dx(), H: s.d.Bounds.Dy(), PixelW: s.d.PixelSize().X, PixelH: s.d.PixelSize().Y, Primary: s.d.Primary,
			Metrics: a.displayMetrics(string(s.d.ID)), Level: lv, State: st, X: s.d.Bounds.Min.X, Y: s.d.Bounds.Min.Y, FPSCap: s.curCap, Note: joinNotes([]string{s.note, s.levelNote}), Options: a.displayOptions(s), Effective: a.effectivePlayback(s)}
		if m := a.ctl.Display(s.n); m != nil {
			dv.Reasons = reasonsString(m.Effective())
		}
		if s.item != nil {
			dv.ItemID = s.item.ID
		}
		if s.r != nil {
			if c, ok := s.r.(render.CapabilityProvider); ok {
				dv.Playback = c.Capabilities()
			}
			stt := s.r.Stats()
			if stt.LastErr != "" {
				dv.Note = joinNotes([]string{dv.Note, stt.LastErr})
			}
			dv.Backend, dv.HWDec, dv.Dropped = stt.Backend, stt.HWDec, stt.Dropped
		}
		if !s.canvas.Empty() {
			dv.Custom = false
			if s.item != nil && s.item.Type == "image" {
				dv.Playback.Fit = true
			}
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
		a.global[visibility.User] = on
		_ = a.ctl.Set(a.now(), visibility.User, -1, on)
	})
}

// Assign 设置某一块显示器的壁纸（按稳定 ID 持久化）；itemID 为空表示清除单独设置、改为跟随默认壁纸。
// display 为空表示只改默认壁纸（新接入的显示器和没有单独设置的显示器使用它），不动已单独设置的屏。
func (a *App) Assign(display, itemID string) { a.Do(func() { a.assign(display, itemID) }) }

// AssignAll 把壁纸应用到所有显示器：设为默认并清除所有单独设置。
func (a *App) AssignAll(itemID string) {
	a.Do(func() {
		if itemID != "" && a.d.Store.Library.Find(itemID) == nil {
			a.lastErr = "找不到要应用的壁纸"
			return
		}
		if !a.spanning() {
			a.d.Store.Assign.Displays = map[string]string{}
		}
		a.assign("", itemID)
	})
}

// SetAlias 给显示器起名（按稳定 ID 持久化）；空串表示恢复系统名称。
func (a *App) SetAlias(display, alias string) {
	a.Do(func() {
		if display == "" {
			return
		}
		a.d.Store.Assign.SetAlias(display, alias)
		a.saveAssign()
	})
}

func (a *App) assign(display, itemID string) {
	if itemID != "" && a.d.Store.Library.Find(itemID) == nil {
		a.lastErr = "找不到要应用的壁纸"
		return
	}
	{
		if a.spanning() {
			a.d.Store.Assign.SpanItem = itemID
			display = ""
		} else if display == "" {
			a.d.Store.Assign.Default = itemID
		} else if itemID == "" {
			delete(a.d.Store.Assign.Displays, display)
		} else {
			if a.d.Store.Assign.Displays == nil {
				a.d.Store.Assign.Displays = map[string]string{}
			}
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
			now := a.assignmentFor(s)
			if a.d.Store.Library.Find(now) == nil {
				now = ""
			}
			if old == now && s.r != nil {
				continue
			}
			s.crashes = 0
			if s.r != nil {
				a.closeScreen(s, false)
			}
			a.assignItem(s)
			if m := a.ctl.Display(s.n); m.State() == visibility.StateFailed && now != "" {
				m.Retry(a.now())
				continue
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
	}
}

// ApplyAll 是 Import 的 applyTo 取值：导入后应用到所有显示器（headless / e2e 用）。
const ApplyAll = "*"

// Import 异步导入，完成后只加入壁纸库。applyTo 非空时再应用：显示器稳定 ID = 只应用到那块屏；
// ApplyAll = 所有显示器。导入本身不会改动任何显示器（修复 0.2.0“导入即应用到所有屏”）。
// MaxQueuedJobs limits pending import requests and encoder jobs separately.
const MaxQueuedJobs = 256

type importRequest struct{ path, applyTo string }

func (a *App) Import(path, applyTo string) {
	a.Do(func() {
		if a.d.Import == nil {
			a.lastImp = "此环境没有可用的导入器"
			return
		}
		if len(a.importQueue) >= MaxQueuedJobs {
			a.lastErr = "导入队列已满（256 项），请等待后重试"
			return
		}
		a.importQueue = append(a.importQueue, importRequest{path, applyTo})
		a.startNextImport()
	})
}

// CancelImports stops the active importer and drops queued requests. Completed
// items remain in the library; cancellation never auto-applies a partial batch.
func (a *App) CancelImports() {
	a.Do(func() {
		a.importQueue = nil
		a.importCancelled = true
		if a.importCancel != nil {
			a.importCancel()
		}
		a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
	})
}

// Imports may replace complete item directories, so they are serialized with
// encoder commits and removals. Finite import requests take priority over queued
// optimizations; after the import queue drains, one encoder starts at a time.
func (a *App) advanceJobs() {
	a.startNextImport()
	if !a.importing {
		a.startNextTranscode()
	}
}

func (a *App) startNextImport() {
	if a.importing || a.cleaning || a.transcodingID != "" || len(a.removing) > 0 || len(a.importQueue) == 0 || a.ctx.Err() != nil {
		return
	}
	next := a.importQueue[0]
	a.importQueue = a.importQueue[1:]
	a.beginImport(next.path, next.applyTo)
}

func (a *App) beginImport(path, applyTo string) {
	if a.d.Import == nil {
		a.lastImp = "此环境没有可用的导入器"
		return
	}
	a.importing = true
	a.importCancelled = false
	a.lastImp = "正在导入 " + filepath.Base(path) + " …"
	ctx, cancel := context.WithCancel(a.ctx)
	a.importCancel = cancel
	go func() {
		defer cancel()
		paths := []string{path}
		var err error
		if st, e := os.Stat(path); e == nil && st.IsDir() {
			paths, err = importer.CollectSources(path)
		}
		if err != nil {
			a.Do(func() {
				a.importing, a.importCancel = false, nil
				a.lastImp = "导入失败: " + err.Error()
				if a.importCancelled {
					a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
				} else {
					a.lastErr = a.lastImp
				}
				a.advanceJobs()
			})
			return
		}
		imported, failed := 0, 0
		firstID, firstTitle := "", ""
		var failures []string
		var warnings []string
		for i, source := range paths {
			if ctx.Err() != nil {
				break
			}
			res, e := a.d.Import(ctx, source)
			if e != nil {
				failed++
				if len(failures) < 3 {
					failures = append(failures, filepath.Base(source)+": "+e.Error())
				}
				a.log.Warn("import failed", "file", telemetry.HashPath(source), "err", e)
				continue
			}
			if res == nil {
				failed++
				continue
			}
			imported++
			for _, warning := range res.Warnings {
				if len(warnings) < 3 {
					warnings = append(warnings, warning)
				}
			}
			if firstID == "" {
				firstID, firstTitle = res.Item.ID, res.Item.Title
			}
			progress := fmt.Sprintf("正在导入 %d/%d：%s", i+1, len(paths), res.Item.Title)
			result := *res
			a.Do(func() {
				old := a.d.Store.Library.Find(result.Item.ID)
				replacing := old != nil
				if replacing && result.Item.Props == (config.ItemProps{}) {
					result.Item.Props = old.Props // 重新导入同一壁纸时保留用户调好的组属性
				}
				a.invalidateItemWork(result.Item.ID)
				a.d.Store.Library.Upsert(result.Item)
				if replacing {
					a.refreshItemScreens(result.Item.ID)
				}
				a.saveLibrary()
				a.lastImp = progress
				if a.d.Store.Config.Render.AutoTranscode && result.Item.Transcode == "recommended" && a.d.Transcode != nil {
					a.transcode(result.Item.ID)
				}
			})
		}
		cancelled := ctx.Err() != nil
		a.Do(func() {
			a.importing, a.importCancel = false, nil
			defer a.advanceJobs()
			if cancelled || a.importCancelled {
				a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
				a.refreshStorage()
				return
			}
			switch {
			case imported == 1 && failed == 0:
				a.lastImp = "已导入「" + firstTitle + "」"
			case imported > 0:
				a.lastImp = fmt.Sprintf("已导入 %d 项，失败 %d 项", imported, failed)
			default:
				a.lastImp = "导入失败"
			}
			if len(failures) > 0 {
				a.lastImp += "：" + joinNotes(failures)
			}
			if len(warnings) > 0 {
				a.lastImp += "（" + joinNotes(warnings) + "）"
			}
			if imported > 0 {
				switch {
				case applyTo == ApplyAll:
					if !a.spanning() {
						a.d.Store.Assign.Displays = map[string]string{}
					}
					a.assign("", firstID)
				case applyTo != "":
					if s := a.screens[desktop.DisplayID(applyTo)]; s != nil {
						a.assign(applyTo, firstID)
						a.lastImp += "，已将首项应用到「" + a.labelOf(s) + "」"
					}
				}
			}
			if failed > 0 {
				a.lastErr = a.lastImp
			}
			a.refreshStorage()
		})
	}()
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
		if a.importing {
			a.lastErr = "导入进行中，请完成后再删除壁纸"
			return
		}
		if a.d.Store.Library.Find(itemID) == nil {
			return
		}
		if _, pending := a.removing[itemID]; pending {
			return
		}
		a.invalidateItemWork(itemID)
		generation := a.itemGeneration[itemID]
		a.removing[itemID] = generation
		var wait sync.WaitGroup
		if a.transcodingID == itemID && a.transcodeDone != nil {
			done := a.transcodeDone
			wait.Add(1)
			go func() { <-done; wait.Done() }()
		}
		for _, s := range a.screens {
			if s.item != nil && s.item.ID == itemID {
				a.closeScreen(s, false)
				wait.Add(1)
				s.ops <- func() { wait.Done() }
			}
		}
		go func() { wait.Wait(); a.Do(func() { a.removeItemNow(itemID, generation) }) }()
	})
}

func (a *App) removeItemNow(itemID string, generation uint64) {
	defer a.advanceJobs()
	if a.removing[itemID] != generation || a.itemGeneration[itemID] != generation {
		return
	}
	delete(a.removing, itemID)
	it := a.d.Store.Library.Find(itemID)
	if it == nil {
		return
	}
	var tombstones []string
	if managedChild(a.d.Store.Paths.Library, it.Dir) {
		moved, err := tombstone(it.Dir)
		if err != nil {
			a.lastErr = "暂无法删除壁纸文件，请稍后重试：" + err.Error()
			a.refreshItemScreens(itemID)
			return
		}
		if moved != "" {
			tombstones = append(tombstones, moved)
		}
	}
	if it.Snapshot != "" {
		dir := filepath.Dir(it.Snapshot)
		if managedChild(a.d.Store.Paths.Cache, dir) {
			moved, err := tombstone(dir)
			if err != nil {
				a.lastErr = "壁纸已移除，旧预览暂无法清理：" + err.Error()
			} else if moved != "" {
				tombstones = append(tombstones, moved)
			}
		}
	}
	cacheDir := filepath.Join(a.d.Store.Paths.Cache, itemID)
	if managedChild(a.d.Store.Paths.Cache, cacheDir) {
		if moved, err := tombstone(cacheDir); err == nil && moved != "" {
			tombstones = append(tombstones, moved)
		} else if err != nil {
			a.lastErr = err.Error()
		}
	}
	a.d.Store.Library.Remove(itemID)
	a.saveLibrary()
	if a.d.Store.Assign.SpanItem == itemID {
		a.d.Store.Assign.SpanItem = ""
	}
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
			s.item = nil
			if s.surf != nil {
				surf := s.surf
				s.ops <- func() { _ = surf.SetVisible(false) }
			}
		}
	}
	for key := range a.d.Store.State.Positions {
		if strings.HasSuffix(key, "|"+itemID) {
			delete(a.d.Store.State.Positions, key)
		}
	}
	a.saveState()
	go func() {
		var err error
		for _, path := range tombstones {
			if e := os.RemoveAll(path); e != nil {
				err = e
			}
		}
		a.Do(func() {
			if err != nil {
				a.lastErr = "文件清理失败：" + err.Error()
			}
			a.refreshStorage()
		})
	}()
}

// A package update can retain its ID while changing entry, snapshot and metadata.
// Refresh active copies explicitly instead of treating same-ID assignment as a no-op.
func (a *App) refreshItemScreens(itemID string) {
	for _, s := range a.screens {
		if s.item == nil || s.item.ID != itemID {
			continue
		}
		if s.r != nil {
			a.closeScreen(s, false)
		}
		a.assignItem(s)
		m := a.ctl.Display(s.n)
		if m.State() == visibility.StateFailed {
			m.Retry(a.now())
			continue
		}
		if m.Level() < visibility.L4 {
			a.startScreen(s, 0)
		}
	}
}

// Invalidate queued/running work before replacing or removing a library identity.
func (a *App) invalidateItemWork(itemID string) {
	for _, j := range a.renditionJobs {
		if j.item == itemID {
			j.state = "已取消"
		}
	}
	delete(a.removing, itemID)
	a.itemGeneration[itemID]++
	if a.transcodingID == itemID && a.transcodeCancel != nil {
		a.transcodeCancel()
	}
	pending := a.transcodeQueue[:0]
	for _, id := range a.transcodeQueue {
		if id != itemID {
			pending = append(pending, id)
		}
	}
	a.transcodeQueue = pending
	delete(a.transc, itemID)
}

// UpdateConfig 修改配置：暂停策略变化会重建状态机并回放当前信号；后端变化会重启渲染器。
func (a *App) UpdateConfig(fn func(*config.Config)) {
	a.Do(func() {
		old := a.d.Store.Config.Clone()
		next := old.Clone()
		fn(&next)
		a.d.Store.Config = next
		a.d.Store.Config.Normalize()
		c := a.d.Store.Config
		if c.Startup.LaunchAtLogin != old.Startup.LaunchAtLogin {
			var err error
			if a.d.SetLaunchAtLogin == nil {
				err = errors.New("当前环境不支持开机自启")
			} else {
				err = a.d.SetLaunchAtLogin(c.Startup.LaunchAtLogin)
			}
			if err != nil {
				a.lastErr = "设置开机自启失败: " + err.Error()
				a.d.Store.Config.Startup.LaunchAtLogin = old.Startup.LaunchAtLogin
			}
		}
		if c.Render.FFmpegPath != old.Render.FFmpegPath || c.Render.TranscodeResolution != old.Render.TranscodeResolution {
			if a.d.ResolveEncoder != nil {
				a.d.Transcode, a.d.TranscodeExt = a.d.ResolveEncoder(c)
			}
			if a.d.EncoderInfo != nil {
				a.encoderInfo = a.d.EncoderInfo(c)
			}
		}
		a.saveConfig()
		if !reflect.DeepEqual(c.Pause, old.Pause) {
			a.updateAppRule()
			a.rebuildMachines()
		}
		if c.Perf != old.Perf {
			for _, s := range a.sortedScreens() {
				a.applyCap(s)
			}
		}
		playbackChanged := c.Render.WindowsBackend != old.Render.WindowsBackend || c.Render.MpvPath != old.Render.MpvPath || c.Render.Hwdec != old.Render.Hwdec || c.Render.Muted != old.Render.Muted || c.Render.ResumePosition != old.Render.ResumePosition || c.Perf.RenderScale != old.Perf.RenderScale
		if c.Privacy.AudioReactive != old.Privacy.AudioReactive || c.Privacy.MouseInput != old.Privacy.MouseInput || playbackChanged {
			for _, s := range a.sortedScreens() {
				if !playbackChanged && (s.item == nil || (s.item.Type != "shader" && s.item.Type != "web")) {
					continue
				}
				if c.Render.WindowsBackend != old.Render.WindowsBackend {
					a.installSystemWallpaper(s)
				}
				if s.r != nil {
					a.closeScreen(s, false)
					if a.ctl.Display(s.n).Level() < visibility.L4 {
						a.startScreen(s, 0)
					}
				} else if c.Render.MpvPath != old.Render.MpvPath && !a.d.SafeMode && s.surf != nil && s.item != nil && s.item.Type != "image" {
					// A missing executable can exhaust retries before the user
					// configures it. An explicit path correction starts a new retry
					// cycle without clearing pause signals or display assignments.
					m := a.ctl.Display(s.n)
					if m.State() == visibility.StateFailed {
						s.crashes = 0
						m.Retry(a.now())
						m.Advance(a.now())
					} else if m.Level() < visibility.L4 {
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
	a.updateLowBattery()
	for _, s := range a.sortedScreens() {
		m := a.ctl.AddDisplay(now, s.n)
		if s.item != nil && s.item.Type == "web" {
			m.SetKind(visibility.Web)
		}
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
	a.ctl.Advance(a.now())
	for _, s := range a.sortedScreens() {
		a.ctl.Display(s.n).Drain()
		l := a.ctl.Display(s.n).Level()
		s.machCap = a.ctl.Display(s.n).WantFPS()
		a.applyCap(s)
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
				surf, err := a.createSurface(s)
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

// Next 是托盘“切换到下一张”（F-09）：每块屏各自按导入顺序换到下一张，结果写成该屏的单独设置；
// 默认壁纸也前进一张（给新接入的显示器）。
func (a *App) Next() {
	a.Do(func() {
		items := a.d.Store.Library.Items
		if len(items) == 0 {
			return
		}
		after := func(cur string) string {
			for i, it := range items {
				if it.ID == cur {
					return items[(i+1)%len(items)].ID
				}
			}
			return items[0].ID
		}
		if a.spanning() {
			a.assign("", after(a.d.Store.Assign.SpanItem))
			return
		}
		for _, s := range a.layoutScreens() {
			cur := ""
			if s.item != nil {
				cur = s.item.ID
			}
			a.assign(string(s.d.ID), after(cur))
		}
		a.assign("", after(a.d.Store.Assign.Default))
	})
}

// Transcode 对一个“建议转码”的条目运行编码器（F-07），完成后替换入口并重启正在播放它的屏幕。
// 原文件保留，转码失败不影响原条目。
func (a *App) Transcode(itemID string) { a.Do(func() { a.transcode(itemID) }) }

func (a *App) transcode(itemID string) {
	it := a.d.Store.Library.Find(itemID)
	if it == nil || a.transc[itemID] == "正在转码…" || a.transc[itemID] == "等待转码…" {
		return
	}
	if a.d.Transcode == nil {
		a.transc[itemID] = "本机没有可用的编码器"
		return
	}
	if len(a.transcodeQueue) >= MaxQueuedJobs {
		a.transc[itemID] = "转码队列已满，请稍后重试"
		return
	}
	a.transc[itemID] = "等待转码…"
	a.transcodeQueue = append(a.transcodeQueue, itemID)
	a.startNextTranscode()
}

func (a *App) startNextTranscode() {
	if a.transcodingID != "" || a.importing || a.cleaning || len(a.removing) > 0 || a.ctx.Err() != nil {
		return
	}
	for len(a.transcodeQueue) > 0 {
		id := a.transcodeQueue[0]
		a.transcodeQueue = a.transcodeQueue[1:]
		if a.d.Store.Library.Find(id) == nil {
			delete(a.transc, id)
			continue
		}
		if a.d.Transcode == nil {
			a.transc[id] = "本机没有可用的编码器"
			continue
		}
		a.beginTranscode(id)
		if a.transcodingID != "" {
			return
		}
	}
	a.startNextRendition()
}

func (a *App) beginTranscode(itemID string) {
	it := a.d.Store.Library.Find(itemID)
	if it == nil {
		return
	}
	dirInfo, err := os.Stat(it.Dir)
	if err != nil {
		a.transc[itemID] = "转码失败：" + err.Error()
		return
	}
	generation := a.itemGeneration[itemID]
	a.transcodingID = itemID
	ctx, cancel := context.WithCancel(a.ctx)
	a.transcodeCancel = cancel
	done := make(chan struct{})
	a.transcodeDone = done
	ext := a.d.TranscodeExt
	if ext == "" {
		ext = ".mp4"
	}
	src := filepath.Join(it.Dir, filepath.FromSlash(it.Entry))
	sourceInfo, _ := os.Stat(src)
	rel := "optimized" + ext
	dst := filepath.Join(it.Dir, rel)
	a.transc[itemID] = "正在转码…"
	enc := a.d.Transcode
	probe := a.d.ProbeMedia
	strict := a.encoderInfo.Strict
	go func() {
		f, err := os.CreateTemp(a.d.Store.Paths.Library, ".transcode-*"+ext)
		tmp := ""
		if err == nil {
			tmp = f.Name()
			err = f.Close()
		}
		if err == nil {
			err = enc(ctx, src, tmp)
		}
		var info *media.Info
		if err == nil && probe != nil {
			info, err = probe(ctx, tmp)
			if err == nil && (info == nil || info.Kind != "video" || (info.Codec != "h264" && info.Codec != "hevc")) {
				err = errors.New("encoder output is not a supported video")
			}
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		// The encoder and probe have returned and released their file handles.
		// Removal must wait for this before renaming the source directory on Windows.
		close(done)
		committed := make(chan struct{})
		a.Do(func() {
			defer close(committed)
			defer func() {
				if tmp != "" {
					_ = os.Remove(tmp)
				}
			}()

			cancel()
			a.transcodeCancel = nil
			a.transcodingID = ""
			defer a.advanceJobs()
			p := a.d.Store.Library.Find(itemID)
			if p == nil || a.itemGeneration[itemID] != generation {
				return
			}
			currentInfo, identityErr := os.Stat(p.Dir)
			if identityErr != nil || !os.SameFile(dirInfo, currentInfo) {
				delete(a.transc, itemID)
				return
			}
			if err == nil && sourceInfo != nil {
				current, checkErr := os.Stat(src)
				if checkErr != nil || !os.SameFile(sourceInfo, current) || current.Size() != sourceInfo.Size() || !current.ModTime().Equal(sourceInfo.ModTime()) {
					err = errors.New("source media changed during encoding; retry the new source")
				}
			}
			if err == nil {
				err = os.Rename(tmp, dst)
			}
			if err != nil {
				a.transc[itemID] = "转码失败：" + err.Error()
				a.log.Warn("transcode failed", "id", itemID, "err", err)
				return
			}
			delete(a.transc, itemID)
			p.Entry, p.Transcode = rel, "done"
			if strict {
				p.Note = "已转码为 H.264、30 fps、无音轨"
			} else {
				p.Note = "转码完成；帧率和音轨依实际编码器能力"
			}
			if info != nil {
				p.Codec, p.Width, p.Height, p.DurationSec = info.Codec, info.W, info.H, info.DurationSec
			}
			if st, err := os.Stat(dst); err == nil {
				p.SizeBytes = st.Size()
			}
			a.saveLibrary()
			a.log.Info("transcoded", "id", itemID)
			a.refreshStorage()
			for _, s := range a.sortedScreens() {
				if s.item != nil && s.item.ID == itemID {
					a.assignItem(s)
					a.closeScreen(s, false)
					if a.ctl.Display(s.n).Level() < visibility.L4 {
						a.startScreen(s, 0)
					}
				}
			}
		})
		select {
		case <-committed:
		case <-a.stopped:
		}
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
}

// Wait 等待事件循环结束。
func (a *App) Wait() { <-a.stopped }

// ErrNoDisplays 在宿主没有显示器时返回。
var ErrNoDisplays = errors.New("app: no displays")
