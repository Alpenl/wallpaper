// Package visibility 是 07 章《暂停与省电状态机》v1.1 的可运行实现（纯逻辑、无 I/O、无真实时钟）。
//
// 时间一律由调用方以 time.Duration（自任意起点）传入，所以测试是确定性的。
// 每个显示器一个 Machine；Controller 负责把全局原因扇出到所有显示器。
// Machine 只产生 Action（指令），由平台层/渲染器驱动去执行。
package visibility

import (
	"fmt"
	"sort"
	"time"
)

// Level 是暂停级别；L0=播放。
type Level int

const (
	L0 Level = iota // Running
	L1              // Paused：视频 pause / Web IsVisible=false / Shader 停 present
	L2              // Frozen：仅 Web 有意义（TrySuspend）；视频与 Shader 跳过
	L3              // Released：停止播放、保留进程（mpv --idle），记录位置
	L4              // Snapshot：结束渲染进程，显示截图
)

func (l Level) String() string { return fmt.Sprintf("L%d", int(l)) }

// Reason 位定义与 07 章 7.2 一一对应。
type Reason uint16

const (
	Fullscreen Reason = 1 << iota
	Covered
	FocusedApp
	Locked
	Remote
	Battery
	BatterySaver
	ThermalSerious
	ThermalCritical
	DisplayOff
	Suspend
	User
	Screensaver
	LowBattery
)

var reasonNames = map[Reason]string{Fullscreen: "Fullscreen", Covered: "Covered", FocusedApp: "FocusedApp", Locked: "Locked",
	Remote: "Remote", Battery: "Battery", BatterySaver: "BatterySaver", ThermalSerious: "ThermalSerious",
	ThermalCritical: "ThermalCritical", DisplayOff: "DisplayOff", Suspend: "Suspend", User: "User",
	Screensaver: "Screensaver", LowBattery: "LowBattery"}

func (r Reason) String() string {
	if n, ok := reasonNames[r]; ok {
		return n
	}
	return fmt.Sprintf("Reason(0x%x)", uint16(r))
}

// Scope：原因允许出现的范围。
type Scope int

const (
	ScopeDisplay Scope = 1
	ScopeGlobal  Scope = 2
	ScopeBoth          = ScopeDisplay | ScopeGlobal
)

var reasonScope = map[Reason]Scope{Fullscreen: ScopeDisplay, Covered: ScopeDisplay, FocusedApp: ScopeGlobal, Locked: ScopeGlobal,
	Remote: ScopeGlobal, Battery: ScopeGlobal, BatterySaver: ScopeGlobal, ThermalSerious: ScopeGlobal, ThermalCritical: ScopeGlobal,
	DisplayOff: ScopeBoth, Suspend: ScopeGlobal, User: ScopeBoth, Screensaver: ScopeGlobal, LowBattery: ScopeGlobal}

// Kind 是壁纸类型，决定 L2 是否存在。
type Kind int

const (
	Video Kind = iota
	Web
	Shader
)

// BatteryPolicy 对应 7.4 的 batteryPolicy。
type BatteryPolicy int

const (
	BatteryPause BatteryPolicy = iota
	BatteryFPS15
	BatteryIgnore
)

// Config 是 7.4 的参数。零值不可用，请用 DefaultConfig。
type Config struct {
	Kind               Kind
	ResumeDebounce     time.Duration
	CoverEnterDebounce time.Duration
	T12, T23, T34      time.Duration // 自“离开 L0”起累计计时
	BatteryPolicy      BatteryPolicy
	SnapshotEvery      time.Duration // 进入暂停前截帧的最小间隔（7.3 末段）
	CrashWindow        time.Duration // 7.3：10 分钟内
	CrashLimit         int           // 7.3：5 次 → Failed
	ThermalFPS         int
}

func DefaultConfig(k Kind) Config {
	c := Config{Kind: k, ResumeDebounce: 300 * time.Millisecond, CoverEnterDebounce: 150 * time.Millisecond,
		T12: 2 * time.Second, T23: 60 * time.Second, T34: 10 * time.Minute, BatteryPolicy: BatteryPause,
		SnapshotEvery: 10 * time.Minute, CrashWindow: 10 * time.Minute, CrashLimit: 5, ThermalFPS: 15}
	if k == Web {
		c.T12 = 0
	}
	return c
}

// ActionKind 是下发给渲染器/平台层的指令。
type ActionKind int

const (
	ActCaptureFrame ActionKind = iota // 截一帧给 L3/L4/崩溃兜底
	ActPause                          // → L1
	ActFreeze                         // → L2（Web TrySuspend）
	ActRelease                        // → L3（保存位置后 stop）
	ActSnapshot                       // → L4（结束进程、显示截图）
	ActResume                         // → L0；From 指出从哪一级恢复（≥L3 需要重新 load + seek）
	ActFPSCap                         // FPS 上限变化；FPS=0 表示不限
	ActHealthCheck                    // 唤醒后先健康检查（Suspend 解除）
	ActRestart                        // 请 supervisor 退避重启渲染器
	ActRestartedAt                    // 重启成功后把渲染器放到 Level（L0 播放，L1/L2 暂停，L3 只起空闲进程，L4 不起）
	ActFail                           // 进入 Failed：显示截图并提示
	ActDestroy                        // 显示器移除
)

var actNames = [...]string{"CaptureFrame", "Pause", "Freeze", "Release", "Snapshot", "Resume", "FPSCap", "HealthCheck",
	"Restart", "RestartedAt", "Fail", "Destroy"}

func (k ActionKind) String() string { return actNames[k] }

type Action struct {
	At    time.Duration
	Kind  ActionKind
	Level Level // Pause..Snapshot：目标级别；Resume：From；RestartedAt：级别
	FPS   int
}

func (a Action) String() string {
	return fmt.Sprintf("%v@%v(%v,fps=%d)", a.Kind, a.At, a.Level, a.FPS)
}

// State 是对外可见状态。
type State int

const (
	StateActive     State = iota // 正常（Level 表示当前级别）
	StateRecovering              // 渲染器崩溃，等待重启结果
	StateFailed
	StateDestroyed
)

// Machine：单个显示器的状态机。
type Machine struct {
	cfg   Config
	raw   Reason                   // 已上报为“开”的原因
	since map[Reason]time.Duration // 原因开始的时间（用于进入去抖）
	eff   Reason                   // 通过进入去抖的有效原因

	level      Level
	state      State
	pausedAt   time.Duration // 离开 L0 的时间
	resumeAt   time.Duration // >0 表示恢复计时中，到点恢复
	resumeArm  bool
	lastCap    time.Duration
	capped     bool // 是否截过帧
	fps        int
	crashes    []time.Duration
	now        time.Duration
	out        []Action
	killedByUs bool // L4 时进程是我们结束的，Done() 不算崩溃
	suspendAt  time.Duration
}

func NewMachine(cfg Config) *Machine {
	return &Machine{cfg: cfg, since: map[Reason]time.Duration{}}
}

func (m *Machine) Level() Level      { return m.level }
func (m *Machine) State() State      { return m.state }
func (m *Machine) Effective() Reason { return m.eff }
func (m *Machine) FPSCap() int       { return m.fps }

// Drain 取走并清空已产生的指令。
func (m *Machine) Drain() []Action {
	a := m.out
	m.out = nil
	return a
}

func (m *Machine) emit(k ActionKind, l Level) {
	m.out = append(m.out, Action{At: m.now, Kind: k, Level: l, FPS: m.fps})
}

func (m *Machine) enterDebounce(r Reason) time.Duration {
	if r == Covered {
		return m.cfg.CoverEnterDebounce
	}
	return 0
}

// floorOf：单个原因的级别下限；0 表示“不暂停”（可能只限帧）。
func (m *Machine) floorOf(r Reason) Level {
	switch r {
	case Locked, DisplayOff, Suspend, Screensaver:
		return L3
	case LowBattery:
		return L4
	case ThermalSerious:
		return L0
	case Battery:
		if m.cfg.BatteryPolicy == BatteryPause {
			return L1
		}
		return L0
	}
	return L1
}

func (m *Machine) floor() Level {
	f := L0
	for r := Reason(1); r != 0 && r <= LowBattery; r <<= 1 {
		if m.eff&r != 0 && m.floorOf(r) > f {
			f = m.floorOf(r)
		}
	}
	return f
}

// WantFPS 返回当前有效原因要求的限帧（0 = 不限）。
func (m *Machine) WantFPS() int { return m.wantFPS() }

func (m *Machine) wantFPS() int {
	if m.eff&ThermalSerious != 0 {
		return m.cfg.ThermalFPS
	}
	if m.eff&Battery != 0 && m.cfg.BatteryPolicy == BatteryFPS15 {
		return 15
	}
	return 0
}

// escalated：暂停累计时长对应的级别。
func (m *Machine) escalated() Level {
	d := m.now - m.pausedAt
	switch {
	case d >= m.cfg.T34:
		return L4
	case d >= m.cfg.T23:
		return L3
	case m.cfg.Kind == Web && d >= m.cfg.T12:
		return L2
	}
	return L1
}

func (m *Machine) skipL2(l Level) Level {
	if l == L2 && m.cfg.Kind != Web {
		return L1
	}
	return l
}

// set 由 Controller 调用（已做范围校验）。
func (m *Machine) set(now time.Duration, r Reason, on bool) {
	if m.state == StateDestroyed {
		return
	}
	m.Advance(now)
	was := m.raw&r != 0
	if on == was {
		return
	}
	if on {
		m.raw |= r
		m.since[r] = now
		if r == Suspend {
			m.suspendAt = now
		}
	} else {
		m.raw &^= r
		delete(m.since, r)
		if m.eff&r != 0 {
			m.eff &^= r
			if r == Suspend {
				// v1.1：睡眠期间升级计时冻结；唤醒后把睡眠时长从暂停累计时间中扣除，避免唤醒瞬间“补升”到 L4
				if m.level > L0 {
					m.pausedAt += now - m.suspendAt
				}
				m.emit(ActHealthCheck, m.level)
			}
		}
	}
	m.evaluate()
}

// Advance 把时间推进到 now，按时间先后处理期间到期的所有计时器。
func (m *Machine) Advance(now time.Duration) {
	if now < m.now {
		panic(fmt.Sprintf("time went backwards: %v < %v", now, m.now))
	}
	for m.state != StateDestroyed {
		next, ok := m.nextDeadline()
		if !ok || next > now {
			break
		}
		m.now = next
		m.evaluate()
	}
	m.now = now
	m.evaluate()
}

func (m *Machine) nextDeadline() (time.Duration, bool) {
	var ds []time.Duration
	for r, t := range m.since {
		if m.eff&r == 0 {
			ds = append(ds, t+m.enterDebounce(r))
		}
	}
	if m.resumeArm {
		ds = append(ds, m.resumeAt)
	}
	if m.level > L0 && m.eff&Suspend == 0 {
		for _, t := range []time.Duration{m.cfg.T12, m.cfg.T23, m.cfg.T34} {
			if d := m.pausedAt + t; d > m.now {
				ds = append(ds, d)
			}
		}
	}
	if len(ds) == 0 {
		return 0, false
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	for _, d := range ds {
		if d > m.now {
			return d, true
		}
	}
	return 0, false
}

func (m *Machine) evaluate() {
	if m.state == StateDestroyed || m.state == StateFailed {
		return
	}
	// 1. 进入去抖
	for r, t := range m.since {
		if m.eff&r == 0 && m.now-t >= m.enterDebounce(r) {
			m.eff |= r
		}
	}
	// 2. 帧率上限
	if f := m.wantFPS(); f != m.fps {
		m.fps = f
		m.emit(ActFPSCap, m.level)
	}
	fl := m.floor()
	// 3. L0 → 暂停（快停）
	if m.level == L0 {
		m.resumeArm = false
		if fl >= L1 {
			m.pausedAt = m.now
			target := m.skipL2(maxL(fl, m.escalated()))
			if !m.capped || m.now-m.lastCap >= m.cfg.SnapshotEvery {
				m.capped, m.lastCap = true, m.now
				m.emit(ActCaptureFrame, L0)
			}
			m.goTo(target)
		}
		return
	}
	// 4. 已暂停：只升不降；原因清空后经 resumeDebounce 恢复
	if fl == L0 {
		if !m.resumeArm {
			m.resumeArm, m.resumeAt = true, m.now+m.cfg.ResumeDebounce
		}
		if m.now >= m.resumeAt {
			m.resumeArm = false
			from := m.level
			m.level = L0
			m.killedByUs = false
			if m.state == StateActive {
				m.emit(ActResume, from)
			}
			// Recovering 中恢复：重启成功时按 L0 启动（见 RestartOK）
		}
		return
	}
	m.resumeArm = false
	esc := m.escalated()
	if m.eff&Suspend != 0 {
		esc = L1 // 睡眠中不按时间升级
	}
	if t := m.skipL2(maxL(fl, esc)); t > m.level {
		m.goTo(t)
	}
}

func (m *Machine) goTo(t Level) {
	m.level = t
	if m.state != StateActive {
		return // Recovering：只记目标级别，不对不存在的渲染器下指令
	}
	switch t {
	case L1:
		m.emit(ActPause, t)
	case L2:
		m.emit(ActFreeze, t)
	case L3:
		m.emit(ActRelease, t)
	case L4:
		m.killedByUs = true
		m.emit(ActSnapshot, t)
	}
}

// RendererDone：渲染进程退出（Done() 通道关闭）。
func (m *Machine) RendererDone(now time.Duration) {
	if m.state == StateDestroyed || m.state == StateFailed {
		return
	}
	m.Advance(now)
	if m.killedByUs && m.level == L4 {
		return // L4 是我们主动结束的
	}
	m.recordCrash()
}

// RestartFailed：supervisor 报告一次重启失败（也计入崩溃次数）。
func (m *Machine) RestartFailed(now time.Duration) {
	if m.state != StateRecovering {
		return
	}
	m.Advance(now)
	m.recordCrash()
}

func (m *Machine) recordCrash() {
	keep := m.crashes[:0]
	for _, t := range m.crashes {
		if m.now-t < m.cfg.CrashWindow {
			keep = append(keep, t)
		}
	}
	m.crashes = append(keep, m.now)
	if len(m.crashes) >= m.cfg.CrashLimit {
		m.state = StateFailed
		m.emit(ActFail, m.level)
		return
	}
	m.state = StateRecovering
	m.emit(ActRestart, m.level)
}

// RestartOK：重启成功；渲染器按当前目标级别就位。
func (m *Machine) RestartOK(now time.Duration) {
	if m.state != StateRecovering {
		return
	}
	m.Advance(now)
	m.state = StateActive
	m.killedByUs = m.level == L4
	m.emit(ActRestartedAt, m.level)
}

// Retry：Failed 后用户点“重试”或更换壁纸。
func (m *Machine) Retry(now time.Duration) {
	if m.state != StateFailed {
		return
	}
	m.now = maxD(m.now, now)
	m.crashes = nil
	m.state = StateRecovering
	m.emit(ActRestart, m.level)
}

func (m *Machine) destroy(now time.Duration) {
	if m.state == StateDestroyed {
		return
	}
	m.now = maxD(m.now, now)
	m.state = StateDestroyed
	m.emit(ActDestroy, m.level)
}

func maxL(a, b Level) Level {
	if a > b {
		return a
	}
	return b
}
func maxD(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// Controller：全局原因 ∪ 显示器原因。
type Controller struct {
	cfg      Config
	global   map[Reason]bool
	disp     map[int]Reason // 各显示器自身来源的原因
	machines map[int]*Machine
}

func NewController(cfg Config) *Controller {
	return &Controller{cfg: cfg, global: map[Reason]bool{}, disp: map[int]Reason{}, machines: map[int]*Machine{}}
}

// AddDisplay 新增显示器；当前生效的全局原因立即带入。
func (c *Controller) AddDisplay(now time.Duration, id int) *Machine {
	m := NewMachine(c.cfg)
	m.now = now
	c.machines[id] = m
	for r, on := range c.global {
		if on {
			m.set(now, r, true)
		}
	}
	return m
}

func (c *Controller) Display(id int) *Machine { return c.machines[id] }

func (c *Controller) RemoveDisplay(now time.Duration, id int) {
	if m := c.machines[id]; m != nil {
		m.destroy(now)
	}
}

// Set 上报原因。display < 0 表示全局。原因与范围不符时返回错误。
func (c *Controller) Set(now time.Duration, r Reason, display int, on bool) error {
	sc, ok := reasonScope[r]
	if !ok {
		return fmt.Errorf("unknown reason %v", r)
	}
	if display < 0 {
		if sc&ScopeGlobal == 0 {
			return fmt.Errorf("%v is display-scoped", r)
		}
		c.global[r] = on
		for _, id := range c.ids() {
			if !on && c.disp[id]&r != 0 {
				continue // 该显示器自己的来源仍为开
			}
			c.machines[id].set(now, r, on)
		}
		return nil
	}
	if sc&ScopeDisplay == 0 {
		return fmt.Errorf("%v is global-scoped", r)
	}
	m := c.machines[display]
	if m == nil {
		return fmt.Errorf("no display %d", display)
	}
	// 同一原因可能既有全局也有显示器来源（DisplayOff/User）：任一为开即为开
	if on {
		c.disp[display] |= r
	} else {
		c.disp[display] &^= r
		if c.global[r] {
			return nil
		}
	}
	m.set(now, r, on)
	return nil
}

// Advance 推进所有显示器的时钟。
func (c *Controller) Advance(now time.Duration) {
	for _, id := range c.ids() {
		c.machines[id].Advance(now)
	}
}

func (c *Controller) ids() []int {
	ids := make([]int, 0, len(c.machines))
	for id := range c.machines {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// NextDeadline 返回所有显示器中最早的待处理截止时间（去抖完成、恢复、计时升级）。
// 平台层据此设置一次性定时器，空闲时不需要周期唤醒（07 章 7.6）。
func (c *Controller) NextDeadline() (time.Duration, bool) {
	var best time.Duration
	found := false
	for _, id := range c.ids() {
		m := c.machines[id]
		if m.state == StateDestroyed || m.state == StateFailed {
			continue
		}
		if d, ok := m.nextDeadline(); ok && (!found || d < best) {
			best, found = d, true
		}
	}
	return best, found
}

// IDs 返回已登记显示器的编号（升序）。
func (c *Controller) IDs() []int { return c.ids() }

// SetKind preserves policy signals/timers while changing the renderer type.
func (m *Machine) SetKind(k Kind) { m.cfg.Kind = k; m.cfg.T12 = DefaultConfig(k).T12 }
