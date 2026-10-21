package visibility

import (
	"testing"
	"time"
)

const ms = time.Millisecond

func kinds(as []Action) []ActionKind {
	var k []ActionKind
	for _, a := range as {
		k = append(k, a.Kind)
	}
	return k
}

func has(as []Action, k ActionKind) bool {
	for _, a := range as {
		if a.Kind == k {
			return true
		}
	}
	return false
}

func find(as []Action, k ActionKind) (Action, bool) {
	for _, a := range as {
		if a.Kind == k {
			return a, true
		}
	}
	return Action{}, false
}

func newCtl(t *testing.T, k Kind) (*Controller, *Machine) {
	t.Helper()
	c := NewController(DefaultConfig(k))
	return c, c.AddDisplay(0, 0)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ---- 07 章 7.7 测试向量 ----

// 向量 1：Covered 0→1→0→1 间隔 100 ms。v1.1 澄清：进入去抖 150 ms，100 ms 的抖动不会进入暂停；
// 最后一次变 1 并稳定 150 ms 后进入 L1。之后变 0，300 ms 内不恢复，满 300 ms 才恢复。
func TestVector1CoveredFlap(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(100*ms, Covered, 0, true))
	must(t, c.Set(200*ms, Covered, 0, false))
	must(t, c.Set(300*ms, Covered, 0, true))
	c.Advance(449 * ms)
	if m.Level() != L0 {
		t.Fatalf("抖动期间不应暂停，got %v", m.Level())
	}
	c.Advance(450 * ms)
	if m.Level() != L1 {
		t.Fatalf("稳定 150ms 后应进入 L1，got %v", m.Level())
	}
	a, ok := find(m.Drain(), ActPause)
	if !ok || a.At != 450*ms {
		t.Fatalf("Pause 应在 450ms 发出: %v", a)
	}
	must(t, c.Set(1000*ms, Covered, 0, false))
	c.Advance(1299 * ms)
	if m.Level() != L1 {
		t.Fatal("300ms 内不应恢复")
	}
}

func TestVector1ResumeExactly300(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Covered, 0, true))
	c.Advance(150 * ms)
	must(t, c.Set(1000*ms, Covered, 0, false))
	c.Advance(1299 * ms)
	if m.Level() != L1 {
		t.Fatal("未满 300ms 不应恢复")
	}
	c.Advance(1300 * ms)
	if m.Level() != L0 {
		t.Fatalf("满 300ms 应恢复，got %v", m.Level())
	}
	a, _ := find(m.Drain(), ActResume)
	if a.At != 1300*ms || a.Level != L1 {
		t.Fatalf("Resume 时间/来源不对: %v", a)
	}
}

func TestResumeCancelledByReturn(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	must(t, c.Set(1000*ms, Fullscreen, 0, false))
	must(t, c.Set(1200*ms, Fullscreen, 0, true)) // 恢复计时中又回来
	c.Advance(5 * time.Second)
	if m.Level() != L1 || has(m.Drain(), ActResume) {
		t.Fatal("恢复计时中原因回来，应取消恢复")
	}
}

// 向量 2（v1.1 修正）：Locked 的下限是 L3，所以锁屏立即进入 L3（不必等 t23）；
// 解锁后 300 ms 回到 L0，Resume.From=L3 → 驱动层重新 load 并 seek 到保存位置。
func TestVector2Locked(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Locked, -1, true))
	as := m.Drain()
	if m.Level() != L3 || !has(as, ActRelease) || has(as, ActPause) {
		t.Fatalf("锁屏应直接 L3（Release），got %v %v", m.Level(), kinds(as))
	}
	if as[0].Kind != ActCaptureFrame {
		t.Fatalf("进入暂停前先截帧: %v", kinds(as))
	}
	c.Advance(2 * time.Minute) // 超过 t23，仍是 L3
	if m.Level() != L3 {
		t.Fatal("锁屏 2 分钟应保持 L3")
	}
	must(t, c.Set(2*time.Minute, Locked, -1, false))
	c.Advance(2*time.Minute + 300*ms)
	a, ok := find(m.Drain(), ActResume)
	if !ok || a.Level != L3 || m.Level() != L0 {
		t.Fatalf("解锁 300ms 后应从 L3 恢复: %v", a)
	}
}

// 向量 3：User 暂停 + 全屏退出 → 保持 L1。
func TestVector3UserSticky(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	must(t, c.Set(10*ms, User, -1, true))
	must(t, c.Set(1000*ms, Fullscreen, 0, false))
	c.Advance(1500 * ms)
	if m.Level() != L1 || has(m.Drain(), ActResume) {
		t.Fatal("User 仍在，不应恢复")
	}
}

// 向量 4：渲染器在 L3 崩溃 → Recovering → 重启后仍在 L3，不开始播放。
func TestVector4CrashAtL3(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Locked, -1, true))
	m.Drain()
	m.RendererDone(time.Second)
	if m.State() != StateRecovering || !has(m.Drain(), ActRestart) {
		t.Fatal("应进入 Recovering 并请求重启")
	}
	m.RestartOK(2 * time.Second)
	as := m.Drain()
	a, _ := find(as, ActRestartedAt)
	if m.State() != StateActive || a.Level != L3 || has(as, ActResume) {
		t.Fatalf("重启后应停在 L3: %v", kinds(as))
	}
}

// 向量 5：LowBattery → L4；接上电源（Battery 与 LowBattery 同时清除）→ L0。
func TestVector5LowBattery(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Battery, -1, true))
	must(t, c.Set(time.Second, LowBattery, -1, true))
	if m.Level() != L4 || !has(m.Drain(), ActSnapshot) {
		t.Fatalf("LowBattery 应进入 L4，got %v", m.Level())
	}
	must(t, c.Set(5*time.Second, LowBattery, -1, false))
	must(t, c.Set(5*time.Second, Battery, -1, false))
	c.Advance(5*time.Second + 300*ms)
	a, ok := find(m.Drain(), ActResume)
	if !ok || a.Level != L4 || m.Level() != L0 {
		t.Fatalf("接电源后应从 L4 恢复，got %v", m.Level())
	}
}

// ---- 边界用例（v1.1 新增规则） ----

// 规则：暂停中级别只升不降。LowBattery 解除但 Battery 仍在 → 保持 L4，不回落到 L1。
func TestNoDowngradeWhilePaused(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Battery, -1, true))
	must(t, c.Set(0, LowBattery, -1, true))
	must(t, c.Set(time.Second, LowBattery, -1, false))
	c.Advance(10 * time.Second)
	if m.Level() != L4 {
		t.Fatalf("应保持 L4，got %v", m.Level())
	}
}

// 计时升级：视频跳过 L2：L1 → t23 → L3 → t34 → L4，计时自离开 L0 起累计。
func TestEscalationVideo(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	c.Advance(59 * time.Second)
	if m.Level() != L1 {
		t.Fatalf("t23 前应是 L1，got %v", m.Level())
	}
	c.Advance(10*time.Minute + time.Second)
	as := m.Drain()
	r, _ := find(as, ActRelease)
	s, _ := find(as, ActSnapshot)
	if has(as, ActFreeze) || r.At != 60*time.Second || s.At != 10*time.Minute || m.Level() != L4 {
		t.Fatalf("升级时间不对: %v", as)
	}
}

// Web：t12=0，进入暂停即 L2（L1=IsVisible=false 由 L2 前置完成，见 v1.1）。
func TestEscalationWeb(t *testing.T) {
	c, m := newCtl(t, Web)
	must(t, c.Set(0, Fullscreen, 0, true))
	if m.Level() != L2 || !has(m.Drain(), ActFreeze) {
		t.Fatalf("Web 应立即 L2，got %v", m.Level())
	}
}

func TestEscalationWebDelayed(t *testing.T) {
	cfg := DefaultConfig(Web)
	cfg.T12 = 2 * time.Second
	c := NewController(cfg)
	m := c.AddDisplay(0, 0)
	must(t, c.Set(0, Fullscreen, 0, true))
	if m.Level() != L1 {
		t.Fatal("t12 前应 L1")
	}
	c.Advance(2 * time.Second)
	if m.Level() != L2 {
		t.Fatal("t12 后应 L2")
	}
}

// ThermalSerious：不暂停，只限 15 fps；与 Battery(fps15 策略) 叠加仍为 15；清除后取消。
func TestThermalSeriousCapsOnly(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, ThermalSerious, -1, true))
	as := m.Drain()
	if m.Level() != L0 || m.FPSCap() != 15 || !has(as, ActFPSCap) {
		t.Fatalf("应只限帧: %v %v", m.Level(), as)
	}
	must(t, c.Set(time.Second, ThermalSerious, -1, false))
	if m.FPSCap() != 0 {
		t.Fatal("应取消限帧")
	}
	must(t, c.Set(2*time.Second, ThermalCritical, -1, true))
	if m.Level() != L1 {
		t.Fatal("ThermalCritical 应 L1")
	}
}

func TestBatteryPolicies(t *testing.T) {
	for _, tc := range []struct {
		p     BatteryPolicy
		level Level
		fps   int
	}{{BatteryPause, L1, 0}, {BatteryFPS15, L0, 15}, {BatteryIgnore, L0, 0}} {
		cfg := DefaultConfig(Video)
		cfg.BatteryPolicy = tc.p
		c := NewController(cfg)
		m := c.AddDisplay(0, 0)
		must(t, c.Set(0, Battery, -1, true))
		if m.Level() != tc.level || m.FPSCap() != tc.fps {
			t.Errorf("policy %d: got %v fps=%d", tc.p, m.Level(), m.FPSCap())
		}
	}
}

// 按显示器判定：屏 0 全屏，屏 1 照常播放（7.1 原则 1、4）。
func TestPerDisplayIsolation(t *testing.T) {
	c := NewController(DefaultConfig(Video))
	m0, m1 := c.AddDisplay(0, 0), c.AddDisplay(0, 1)
	must(t, c.Set(0, Fullscreen, 0, true))
	if m0.Level() != L1 || m1.Level() != L0 {
		t.Fatal("只应暂停屏 0")
	}
	must(t, c.Set(0, Locked, -1, true))
	if m1.Level() != L3 {
		t.Fatal("全局原因应扇出到屏 1")
	}
}

// 新接入的显示器继承当前全局原因。
func TestAddDisplayInheritsGlobal(t *testing.T) {
	c := NewController(DefaultConfig(Video))
	c.AddDisplay(0, 0)
	must(t, c.Set(0, User, -1, true))
	m := c.AddDisplay(time.Second, 1)
	if m.Level() != L1 {
		t.Fatal("新屏应继承 User")
	}
}

// DisplayOff/User 可同时有全局和显示器来源：任一为开即为开。
func TestDualScopeReason(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, User, 0, true))
	must(t, c.Set(0, User, -1, true))
	must(t, c.Set(time.Second, User, 0, false)) // 全局仍开
	c.Advance(2 * time.Second)
	if m.Level() != L1 {
		t.Fatal("全局 User 仍开")
	}
	must(t, c.Set(3*time.Second, User, 0, true))
	must(t, c.Set(3*time.Second, User, -1, false)) // 显示器来源仍开
	c.Advance(4 * time.Second)
	if m.Level() != L1 {
		t.Fatal("显示器 User 仍开")
	}
	must(t, c.Set(5*time.Second, User, 0, false))
	c.Advance(6 * time.Second)
	if m.Level() != L0 {
		t.Fatal("两个来源都关后应恢复")
	}
}

func TestScopeErrors(t *testing.T) {
	c, _ := newCtl(t, Video)
	if c.Set(0, Covered, -1, true) == nil {
		t.Error("Covered 不能是全局")
	}
	if c.Set(0, Locked, 0, true) == nil {
		t.Error("Locked 不能按显示器")
	}
	if c.Set(0, Reason(1<<15), 0, true) == nil {
		t.Error("未知原因")
	}
	if c.Set(0, Fullscreen, 9, true) == nil {
		t.Error("不存在的显示器")
	}
}

// Suspend 解除时先发 HealthCheck，再按去抖恢复；Suspend 下限 L3。
func TestSuspendHealthCheck(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Suspend, -1, true))
	if m.Level() != L3 {
		t.Fatal("Suspend 应 L3")
	}
	m.Drain()
	must(t, c.Set(time.Hour, Suspend, -1, false))
	c.Advance(time.Hour + 300*ms)
	k := kinds(m.Drain())
	if len(k) != 2 || k[0] != ActHealthCheck || k[1] != ActResume {
		t.Fatalf("应先 HealthCheck 后 Resume（睡眠期间不升 L4）: %v", k)
	}
}

// 唤醒后停在锁屏：睡眠时长不计入升级计时（否则唤醒瞬间就会补升 L4、杀进程）。
func TestSuspendThenLockedNoCatchUp(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Locked, -1, true))
	must(t, c.Set(time.Minute, Suspend, -1, true))
	must(t, c.Set(9*time.Hour, Suspend, -1, false))
	if m.Level() != L3 || has(m.Drain(), ActSnapshot) {
		t.Fatalf("唤醒时不应补升 L4, got %v", m.Level())
	}
	// 暂停累计 = 睡前 1 分钟 + 醒后时间；到 t34 才升 L4
	c.Advance(9*time.Hour + 8*time.Minute + 59*time.Second)
	if m.Level() != L3 {
		t.Fatal("未满 t34")
	}
	c.Advance(9*time.Hour + 9*time.Minute)
	if m.Level() != L4 {
		t.Fatalf("满 t34 应 L4, got %v", m.Level())
	}
}

// L4 时进程是我们结束的：Done() 不算崩溃。
func TestDoneAtL4NotCrash(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, LowBattery, -1, true))
	m.Drain()
	m.RendererDone(time.Second)
	if m.State() != StateActive || len(m.Drain()) != 0 {
		t.Fatal("L4 主动结束不应触发 Recovering")
	}
}

// 10 分钟内 5 次崩溃 → Failed；窗口外的旧崩溃不计；Retry 清零。
func TestCrashLimit(t *testing.T) {
	_, m := newCtl(t, Video)
	m.RendererDone(0)
	m.RestartOK(1 * time.Second)
	for i := 1; i <= 3; i++ { // 共 4 次（都在 10 分钟内）
		m.RendererDone(time.Duration(i) * time.Minute)
		m.RestartOK(time.Duration(i)*time.Minute + time.Second)
	}
	if m.State() != StateActive {
		t.Fatal("4 次不应 Failed")
	}
	m.RendererDone(10*time.Minute + 30*time.Second) // t=0 那次已出窗口 → 窗口内 1,2,3,10.5 共 4 次
	if m.State() != StateRecovering {
		t.Fatalf("窗口外的旧崩溃不应计入, got %v", m.State())
	}
	m.RestartFailed(10*time.Minute + 31*time.Second) // 第 5 次（窗口内）
	if m.State() != StateFailed || !has(m.Drain(), ActFail) {
		t.Fatal("应 Failed")
	}
	m.RendererDone(12 * time.Minute) // Failed 后忽略
	m.Retry(13 * time.Minute)
	if m.State() != StateRecovering || !has(m.Drain(), ActRestart) {
		t.Fatal("Retry 应重新请求重启")
	}
}

// Recovering 期间原因变化：不对渲染器下指令，只记目标级别；重启成功按最新级别就位。
func TestRecoveringTracksTarget(t *testing.T) {
	c, m := newCtl(t, Video)
	m.RendererDone(0)
	m.Drain()
	must(t, c.Set(time.Second, Fullscreen, 0, true))
	if has(m.Drain(), ActPause) {
		t.Fatal("Recovering 中不应下 Pause")
	}
	m.RestartOK(2 * time.Second)
	a, _ := find(m.Drain(), ActRestartedAt)
	if a.Level != L1 {
		t.Fatalf("重启后应以 L1（暂停）就位, got %v", a.Level)
	}
	// Recovering 中原因清空后恢复：不发 Resume（渲染器不存在），重启后以 L0 播放
	m.RendererDone(3 * time.Second)
	must(t, c.Set(3*time.Second, Fullscreen, 0, false))
	c.Advance(4 * time.Second)
	if has(m.Drain(), ActResume) {
		t.Fatal("Recovering 中不应发 Resume")
	}
	m.RestartOK(5 * time.Second)
	a, _ = find(m.Drain(), ActRestartedAt)
	if a.Level != L0 {
		t.Fatalf("应以 L0 就位, got %v", a.Level)
	}
	m.RestartOK(6 * time.Second) // 非 Recovering 时忽略
	m.RestartFailed(6 * time.Second)
	m.Retry(6 * time.Second)
	if len(m.Drain()) != 0 {
		t.Fatal("非对应状态的调用应被忽略")
	}
}

// 截帧：10 分钟内第二次进入暂停不重复截帧。
func TestCaptureFrameThrottle(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	must(t, c.Set(time.Second, Fullscreen, 0, false))
	must(t, c.Set(2*time.Second, Fullscreen, 0, true))
	n := 0
	for _, a := range m.Drain() {
		if a.Kind == ActCaptureFrame {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("10 分钟内只截一次, got %d", n)
	}
	must(t, c.Set(3*time.Second, Fullscreen, 0, false))
	must(t, c.Set(11*time.Minute, Fullscreen, 0, true))
	if !has(m.Drain(), ActCaptureFrame) {
		t.Fatal("超过 10 分钟应再截")
	}
}

func TestDisplayRemoved(t *testing.T) {
	c, m := newCtl(t, Video)
	c.RemoveDisplay(time.Second, 0)
	c.RemoveDisplay(time.Second, 0) // 幂等
	c.RemoveDisplay(time.Second, 7) // 不存在
	as := m.Drain()
	if m.State() != StateDestroyed || len(as) != 1 || as[0].Kind != ActDestroy {
		t.Fatalf("应销毁一次: %v", as)
	}
	must(t, c.Set(2*time.Second, Fullscreen, 0, true))
	m.RendererDone(3 * time.Second)
	if len(m.Drain()) != 0 {
		t.Fatal("销毁后忽略一切")
	}
}

// 同一时刻多原因、重复上报幂等、时间倒退 panic。
func TestIdempotentAndClock(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	must(t, c.Set(0, Fullscreen, 0, true))
	if n := len(m.Drain()); n != 2 { // CaptureFrame + Pause
		t.Fatalf("重复上报应幂等, got %d", n)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("时间倒退应 panic")
		}
	}()
	c.Advance(-time.Second)
}

// 恢复后再暂停：升级计时重新从 0 开始。
func TestEscalationResetsAfterResume(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Fullscreen, 0, true))
	must(t, c.Set(50*time.Second, Fullscreen, 0, false))
	c.Advance(51 * time.Second)
	must(t, c.Set(55*time.Second, Fullscreen, 0, true))
	c.Advance(100 * time.Second) // 距本次暂停 45 s < t23
	if m.Level() != L1 {
		t.Fatalf("升级计时应重置, got %v", m.Level())
	}
}

// 同一次 Advance 跨越多个截止时间时按时间顺序处理：遮挡去抖完成 → 稍后 t23。
func TestAdvanceOrdering(t *testing.T) {
	c, m := newCtl(t, Video)
	must(t, c.Set(0, Covered, 0, true))
	c.Advance(2 * time.Minute)
	as := m.Drain()
	p, _ := find(as, ActPause)
	r, _ := find(as, ActRelease)
	if p.At != 150*ms || r.At != 150*ms+60*time.Second {
		t.Fatalf("顺序/时间不对: %v", as)
	}
}

func TestStringers(t *testing.T) {
	if Covered.String() != "Covered" || Reason(1<<15).String() == "" || L3.String() != "L3" ||
		ActResume.String() != "Resume" || (Action{Kind: ActPause, Level: L1}).String() == "" {
		t.Fatal("stringer")
	}
	if NewMachine(DefaultConfig(Video)).Effective() != 0 {
		t.Fatal()
	}
	if c := NewController(DefaultConfig(Video)); c.Display(0) != nil {
		t.Fatal()
	}
}

// 平台层用 NextDeadline 设一次性定时器：空闲时没有截止时间，遮挡时为去抖完成时刻，暂停后为恢复/升级时刻。
func TestNextDeadline(t *testing.T) {
	c := NewController(DefaultConfig(Video))
	c.AddDisplay(0, 0)
	c.AddDisplay(0, 1)
	if _, ok := c.NextDeadline(); ok {
		t.Fatal("空闲时不应有截止时间")
	}
	must(t, c.Set(0, Covered, 1, true))
	if d, ok := c.NextDeadline(); !ok || d != 150*ms {
		t.Fatalf("应为去抖完成 150ms, got %v %v", d, ok)
	}
	c.Advance(150 * ms)
	if d, _ := c.NextDeadline(); d != 150*ms+2*time.Second {
		t.Fatalf("暂停后下一个应为 t12 检查点, got %v", d)
	}
	must(t, c.Set(time.Second, Covered, 1, false))
	if d, _ := c.NextDeadline(); d != 1300*ms {
		t.Fatalf("应为恢复时刻 1.3s, got %v", d)
	}
	c.RemoveDisplay(2*time.Second, 1)
	if _, ok := c.NextDeadline(); ok {
		t.Fatal("销毁的显示器不计")
	}
	if len(c.IDs()) != 2 {
		t.Fatal("IDs")
	}
}
