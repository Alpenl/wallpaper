//go:build windows

// Windows 原型（第 19 轮重写）：
//   - 每块显示器一个 screen：自建子 HWND（Progman/WorkerW 两条挂载分支）+ 一个渲染后端（mpv 或 mfhost 子进程）；
//   - 暂停逻辑由 visibility 包（07 章 v1.1 状态机）驱动，信号源：WinEventHook（前台/最小化/移动结束）+ 1 s 兜底轮询
//     （全屏、遮挡网格）、WTS 会话通知（锁屏、远程桌面）、电源设置通知（交流/电池、电量、省电、显示器开关）、
//     WM_POWERBROADCAST（睡眠/唤醒）、屏保轮询；
//   - 状态机的 Action 在 UI 线程上执行；后端的异步结果通过 PostMessage 回到 UI 线程，状态机本身不加锁；
//   - Job Object（KILL_ON_JOB_CLOSE）、TaskbarCreated 异步重挂、健康检查保留。
//
// 只用标准库 syscall，CGO_ENABLED=0。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	vis "wpvis"
)

func osName() string { return "windows" }

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	wtsapi32 = syscall.NewLazyDLL("wtsapi32.dll")

	pFindWindowW               = user32.NewProc("FindWindowW")
	pFindWindowExW             = user32.NewProc("FindWindowExW")
	pSendMessageTimeoutW       = user32.NewProc("SendMessageTimeoutW")
	pEnumWindows               = user32.NewProc("EnumWindows")
	pGetWindowLongPtrW         = user32.NewProc("GetWindowLongPtrW")
	pSetWindowPos              = user32.NewProc("SetWindowPos")
	pRegisterClassExW          = user32.NewProc("RegisterClassExW")
	pCreateWindowExW           = user32.NewProc("CreateWindowExW")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pDefWindowProcW            = user32.NewProc("DefWindowProcW")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pRegisterWindowMessageW    = user32.NewProc("RegisterWindowMessageW")
	pSetLayeredWindowAttrs     = user32.NewProc("SetLayeredWindowAttributes")
	pMapWindowPoints           = user32.NewProc("MapWindowPoints")
	pEnumDisplayMonitors       = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	pMonitorFromWindow         = user32.NewProc("MonitorFromWindow")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pGetWindowRect             = user32.NewProc("GetWindowRect")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pIsWindow                  = user32.NewProc("IsWindow")
	pIsWindowVisible           = user32.NewProc("IsWindowVisible")
	pIsIconic                  = user32.NewProc("IsIconic")
	pIsZoomed                  = user32.NewProc("IsZoomed")
	pGetParent                 = user32.NewProc("GetParent")
	pSetWinEventHook           = user32.NewProc("SetWinEventHook")
	pSetTimer                  = user32.NewProc("SetTimer")
	pKillTimer                 = user32.NewProc("KillTimer")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pSystemParametersInfoW     = user32.NewProc("SystemParametersInfoW")
	pRegisterPowerSettingNotif = user32.NewProc("RegisterPowerSettingNotification")
	pSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	pCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	pSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	pGetCurrentProcess         = kernel32.NewProc("GetCurrentProcess")
	pWaitNamedPipeW            = kernel32.NewProc("WaitNamedPipeW")
	pDwmGetWindowAttribute     = dwmapi.NewProc("DwmGetWindowAttribute")
	pSHQueryUserNotifState     = shell32.NewProc("SHQueryUserNotificationState")
	pWTSRegisterSessionNotif   = wtsapi32.NewProc("WTSRegisterSessionNotification")
)

const (
	wsChild, wsVisible, wsClipChildren, wsPopup                    = 0x40000000, 0x10000000, 0x02000000, 0x80000000
	wsExLayered, wsExNoRedirBitmap, wsExToolWindow, wsExNoActivate = 0x00080000, 0x00200000, 0x00000080, 0x08000000
	swpNoSize, swpNoMove, swpNoActivate, swpNoZOrder               = 0x1, 0x2, 0x10, 0x4
	lwaAlpha                                                       = 0x2
	wmTimer, wmDestroy                                             = 0x0113, 0x0002
	wmPowerBroadcast, wmWTSSessionChange, wmDisplayChange          = 0x0218, 0x02B1, 0x007E
	wmAppReattach                                                  = 0x8001
	wmAppBackend                                                   = 0x8002 // wParam=屏号，lParam=beExited/beStartOK/beStartFail
	wmAppUserToggle                                                = 0x8003
	eventSystemForeground, eventSystemMoveSizeEnd                  = 0x0003, 0x000B
	eventSystemMinimizeStart, eventSystemMinimizeEnd               = 0x0016, 0x0017
	winEventOutOfContext                                           = 0x0000
	dwmwaCloaked                                                   = 14
	qunsRunningD3DFull                                             = 3
	gwlExStyle                                                     = ^uintptr(19) // -20
	monitorDefaultNull                                             = 0
	smRemoteSession                                                = 0x1000
	spiGetScreenSaverRunning                                       = 0x0072
	pbtAPMSuspend, pbtAPMResumeAutomatic, pbtPowerSettingChange    = 0x0004, 0x0012, 0x8013
	wtsConsoleConnect, wtsConsoleDisconnect                        = 0x1, 0x2
	wtsRemoteConnect, wtsRemoteDisconnect                          = 0x3, 0x4
	wtsSessionLock, wtsSessionUnlock                               = 0x7, 0x8
	timerPoll, timerHealth, timerDeadline                          = 1, 2, 3
	beExited, beStartOK, beStartFail                               = 1, 2, 3
)

// 电源设置 GUID：前三个核对自 mingw-w64 winnt.h；GUID_POWER_SAVING_STATUS 取自微软文档（mingw 头文件里没有）
var (
	guidACDCPowerSource     = guid{0x5d3e9a59, 0xe9d5, 0x4b00, [8]byte{0xa6, 0xbd, 0xff, 0x34, 0xff, 0x51, 0x65, 0x48}}
	guidBatteryPercentage   = guid{0xa7ad8041, 0xb45a, 0x4cae, [8]byte{0x87, 0xa3, 0xee, 0xcb, 0xb4, 0x68, 0xa9, 0xe1}}
	guidConsoleDisplayState = guid{0x6fe69556, 0x704a, 0x47a0, [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47}}
	guidPowerSavingStatus   = guid{0xe00958c0, 0xc213, 0x4ace, [8]byte{0xac, 0x77, 0xfe, 0xcc, 0xed, 0x2e, 0xee, 0xa5}}
)

type rect struct{ L, T, R, B int32 }
type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}
type wndClassEx struct {
	Size, Style                     uint32
	WndProc                         uintptr
	ClsExtra, WndExtra              int32
	Instance, Icon, Cursor, BgBrush uintptr
	MenuName, ClassName             *uint16
	IconSm                          uintptr
}
type msgT struct {
	Hwnd, Message, WParam, LParam uintptr
	Time                          uint32
	X, Y                          int32
	_                             uint32
}

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func up(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

// screen：一块显示器上的一张壁纸
type screen struct {
	id        int
	mon       monitorInfo
	hmon      uintptr
	surface   uintptr
	be        renderBackend
	crashes   int  // 连续重启次数，用于退避
	started   bool // 后端当前是否在运行
	lastFS    bool
	lastCover bool
}

func (s *screen) w() int32 { return s.mon.Monitor.R - s.mon.Monitor.L }
func (s *screen) h() int32 { return s.mon.Monitor.B - s.mon.Monitor.T }

var (
	hInst                     uintptr
	msgWnd                    uintptr
	msgTaskbarCreated         uintptr
	progman, defView, workerw uintptr
	raised                    bool
	screens                   []*screen
	ctl                       *vis.Controller
	reattaching               atomic.Bool
	reattachTry               int
	reattachBack              = []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	onBattery                 bool
	batteryPct                = -1
	userPaused                bool
	pollStats                 struct{ n, maxUs, sumUs int64 }
)

var findCB uintptr

var debugCov = os.Getenv("WPPROTO_DEBUGCOV") != ""

func now() time.Duration { return time.Since(start) }

// ---- 桌面层级（参照 Lively WinDesktopCore.cs 150–230 / 1036–1080）----
func findDesktop() error {
	progman, _, _ = pFindWindowW.Call(up(u16("Progman")), 0)
	if progman == 0 {
		return fmt.Errorf("Progman not found")
	}
	var r uintptr
	pSendMessageTimeoutW.Call(progman, 0x052C, 0xD, 0x1, 0 /*SMTO_NORMAL*/, 1000, uintptr(unsafe.Pointer(&r)))
	ex, _, _ := pGetWindowLongPtrW.Call(progman, gwlExStyle)
	raised = ex&wsExNoRedirBitmap != 0
	workerw, defView = 0, 0
	if raised { // 24H2+：DefView 和 WorkerW 都是 Progman 的子窗口
		defView, _, _ = pFindWindowExW.Call(progman, 0, up(u16("SHELLDLL_DefView")), 0)
		workerw, _, _ = pFindWindowExW.Call(progman, 0, up(u16("WorkerW")), 0)
	} else { // 旧版：含 DefView 的顶层窗口之后的那个 WorkerW
		if findCB == 0 { // 回调只建一次（NewCallback 不释放）
			findCB = syscall.NewCallback(func(top, _ uintptr) uintptr {
				if p, _, _ := pFindWindowExW.Call(top, 0, up(u16("SHELLDLL_DefView")), 0); p != 0 {
					defView = p
					workerw, _, _ = pFindWindowExW.Call(0, top, up(u16("WorkerW")), 0)
				}
				return 1
			})
		}
		pEnumWindows.Call(findCB, 0)
	}
	logf("DESKTOP progman=0x%x raised24H2=%v defView=0x%x workerw=0x%x", progman, raised, defView, workerw)
	if workerw == 0 {
		return fmt.Errorf("WorkerW not found")
	}
	return nil
}

func className(h uintptr) string {
	buf := make([]uint16, 256)
	pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
	return syscall.UTF16ToString(buf)
}

// ---- Job Object：本进程加入 KILL_ON_JOB_CLOSE 作业，之后的 mpv / mfhost 子进程自动继承 ----
func setupJob() {
	job, _, err := pCreateJobObjectW.Call(0, 0)
	if job == 0 {
		logf("JOB_ERR CreateJobObject %v", err)
		return
	}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION（x64 下 144 字节）；LimitFlags 在偏移 16
	var info [144]byte
	*(*uint32)(unsafe.Pointer(&info[16])) = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if r, _, err := pSetInformationJobObject.Call(job, 9 /*JobObjectExtendedLimitInformation*/, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info))); r == 0 {
		logf("JOB_ERR SetInformation %v", err)
		return
	}
	self, _, _ := pGetCurrentProcess.Call()
	if r, _, err := pAssignProcessToJobObject.Call(job, self); r == 0 {
		logf("JOB_ERR Assign %v（可能已在不允许嵌套的作业里）", err)
		return
	}
	logf("JOB ok（句柄不可继承；本进程退出或被杀时 mpv 随之结束）")
}

// ---- 挂载：为一块屏建子 HWND ----
func createSurface(s *screen) error {
	if *flagStandalone {
		w, h := s.w(), s.h()
		if w > 1280 {
			w, h = 1280, 720
		}
		hw, _, err := pCreateWindowExW.Call(0, up(u16("WpProtoSurface")), up(u16(fmt.Sprintf("wpproto-standalone-%d", s.id))),
			0x10000000|0x00C00000 /*WS_VISIBLE|WS_CAPTION*/, uintptr(s.mon.Monitor.L), uintptr(s.mon.Monitor.T), uintptr(w), uintptr(h), 0, 0, hInst, 0)
		if hw == 0 {
			return fmt.Errorf("CreateWindowEx(standalone): %v", err)
		}
		s.surface = hw
		s.mon.Monitor = rect{s.mon.Monitor.L, s.mon.Monitor.T, s.mon.Monitor.L + w, s.mon.Monitor.T + h}
		logf("SURFACE screen=%d standalone hwnd=0x%x %dx%d", s.id, hw, w, h)
		return nil
	}
	parent := workerw
	if raised {
		parent = progman
	}
	r := s.mon.Monitor
	pMapWindowPoints.Call(0, parent, uintptr(unsafe.Pointer(&r)), 2)
	ex := uintptr(wsExNoActivate)
	if raised {
		ex |= wsExLayered
	}
	h, _, err := pCreateWindowExW.Call(ex, up(u16("WpProtoSurface")), up(u16("wpproto")),
		wsChild|wsVisible|wsClipChildren, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), parent, 0, hInst, 0)
	if h == 0 {
		return fmt.Errorf("CreateWindowEx: %v", err)
	}
	if raised {
		pSetLayeredWindowAttrs.Call(h, 0, 255, lwaAlpha)
		pSetWindowPos.Call(h, defView, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		pSetWindowPos.Call(workerw, 1 /*HWND_BOTTOM*/, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
	s.surface = h
	p, _, _ := pGetParent.Call(h)
	logf("SURFACE screen=%d hwnd=0x%x parent=0x%x rect=(%d,%d %dx%d) layered=%v", s.id, h, p, r.L, r.T, r.R-r.L, r.B-r.T, raised)
	return nil
}

func createAllSurfaces() error {
	if !*flagStandalone {
		if err := findDesktop(); err != nil {
			return err
		}
	}
	for _, s := range screens {
		if err := createSurface(s); err != nil {
			return err
		}
	}
	return nil
}

// ---- 渲染后端的启动、退避与结果回传（结果一律 PostMessage 回 UI 线程）----
func startScreen(s *screen, delay time.Duration, paused bool) {
	surf, w, h := s.surface, s.w(), s.h()
	go func() {
		time.Sleep(delay)
		if reattaching.Load() || surf == 0 {
			pPostMessageW.Call(msgWnd, wmAppBackend, uintptr(s.id), beStartFail)
			return
		}
		if err := s.be.start(surf, w, h, paused); err != nil {
			logf("BACKEND screen=%d start 失败: %v", s.id, err)
			pPostMessageW.Call(msgWnd, wmAppBackend, uintptr(s.id), beStartFail)
			return
		}
		pPostMessageW.Call(msgWnd, wmAppBackend, uintptr(s.id), beStartOK)
	}()
}

func backoff(n int) time.Duration {
	d := time.Second << min(n, 5) // 1,2,4,…,32 s
	return min(d, 30*time.Second)
}

func onBackend(id int, code uintptr) {
	if id < 0 || id >= len(screens) {
		return
	}
	s := screens[id]
	m := ctl.Display(id)
	t := now()
	switch code {
	case beExited:
		s.started = false
		logf("BACKEND screen=%d 渲染进程意外退出", id)
		m.RendererDone(t)
	case beStartOK:
		s.started = true
		if m.State() == vis.StateRecovering {
			m.RestartOK(t)
		} else {
			applyLevel(s, m.Level()) // 初次启动：按当前级别就位
		}
		go func() { time.Sleep(60 * time.Second); s.crashes = 0 }() // 稳定运行 60 s 后清零退避（原型简化，非严格线程安全）
	case beStartFail:
		if m.State() == vis.StateRecovering {
			m.RestartFailed(t)
		} else {
			m.RendererDone(t)
		}
	}
	runActions()
}

func applyLevel(s *screen, l vis.Level) {
	switch {
	case l == vis.L0:
		go s.be.setLevel(0)
	case l <= vis.L2:
		go s.be.setLevel(1)
	case l == vis.L3:
		go s.be.setLevel(3)
	default:
		s.be.kill()
		s.started = false
	}
}

// ---- 执行状态机产生的 Action ----
func runActions() {
	for _, s := range screens {
		m := ctl.Display(s.id)
		for _, a := range m.Drain() {
			logf("VIS screen=%d %v level=%v state=%d eff=%s", s.id, a, m.Level(), m.State(), reasonsString(m.Effective()))
			switch a.Kind {
			case vis.ActCaptureFrame:
				// 原型不截图；产品中用于 L3/L4 与崩溃兜底
			case vis.ActPause, vis.ActFreeze:
				go s.be.setLevel(1)
			case vis.ActRelease:
				go s.be.setLevel(3)
			case vis.ActSnapshot:
				s.be.kill() // L4：结束渲染进程（产品中显示截图；原型露出系统壁纸）
				s.started = false
			case vis.ActResume:
				if a.Level == vis.L4 || !s.started {
					startScreen(s, 0, false)
				} else {
					go s.be.setLevel(0)
				}
			case vis.ActFPSCap:
				logf("FPSCAP screen=%d fps=%d（原型只记录，未实现）", s.id, a.FPS)
			case vis.ActHealthCheck:
				healthCheck()
			case vis.ActRestart:
				d := backoff(s.crashes)
				s.crashes++
				logf("SUPERVISOR screen=%d %v 后重启（第 %d 次）", s.id, d, s.crashes)
				s.be.kill()
				startScreen(s, d, m.Level() > vis.L0)
			case vis.ActRestartedAt:
				applyLevel(s, a.Level)
			case vis.ActFail:
				logf("FAILED screen=%d 10 分钟内崩溃过多，停止重启（产品中显示截图并提示）", s.id)
			}
		}
	}
	scheduleDeadline()
}

func reasonsString(r vis.Reason) string {
	out := ""
	for b := vis.Reason(1); b != 0 && b <= vis.LowBattery; b <<= 1 {
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

// 只在有截止时间时设一次性定时器，空闲时不周期唤醒
func scheduleDeadline() {
	pKillTimer.Call(msgWnd, timerDeadline)
	if d, ok := ctl.NextDeadline(); ok {
		wait := d - now()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		pSetTimer.Call(msgWnd, timerDeadline, uintptr(wait.Milliseconds()+1), 0)
	}
}

func setReason(r vis.Reason, display int, on bool, src string) {
	if err := ctl.Set(now(), r, display, on); err != nil {
		logf("VIS_ERR %v", err)
		return
	}
	logf("SIGNAL %s display=%d %v=%v", src, display, r, on)
	runActions()
}

// ---- 信号源 1：前台全屏 + 遮挡网格（按屏）----
var shellClasses = map[string]bool{"Progman": true, "WorkerW": true, "Shell_TrayWnd": true, "Shell_SecondaryTrayWnd": true,
	"Windows.UI.Core.CoreWindow": true, "XamlExplorerHostIslandWindow": true, "WpProtoSurface": true, "WpProtoMsg": true}

func cloaked(h uintptr) bool {
	var c uint32
	pDwmGetWindowAttribute.Call(h, dwmwaCloaked, uintptr(unsafe.Pointer(&c)), 4)
	return c != 0
}

func evalVisibility(src string) {
	t0 := time.Now()
	// 全屏：前台窗口覆盖其所在显示器，且不是 shell/cloaked；QUNS 独占全屏也算在前台窗口所在的屏上
	fg, _, _ := pGetForegroundWindow.Call()
	fsOn := make([]bool, len(screens)) // 按屏索引判定（不用 HMONITOR 比较，-fakemonitors 下多块虚拟屏共用一个 HMONITOR）
	if fg != 0 && !shellClasses[className(fg)] && !cloaked(fg) {
		m, _, _ := pMonitorFromWindow.Call(fg, monitorDefaultNull)
		var r rect
		pGetWindowRect.Call(fg, uintptr(unsafe.Pointer(&r)))
		var quns uint32
		pSHQueryUserNotifState.Call(uintptr(unsafe.Pointer(&quns)))
		for i, s := range screens {
			mr := s.mon.Monitor
			covers := r.L <= mr.L && r.T <= mr.T && r.R >= mr.R && r.B >= mr.B
			fsOn[i] = m == s.hmon && (covers || (quns == qunsRunningD3DFull && !*flagStandalone))
		}
	}
	cov := coverage()
	for i, s := range screens {
		fs := fsOn[i]
		if fs != s.lastFS {
			s.lastFS = fs
			setReason(vis.Fullscreen, s.id, fs, "fg/"+src)
		}
		if cov[i] != s.lastCover {
			s.lastCover = cov[i]
			setReason(vis.Covered, s.id, cov[i], "grid/"+src)
		}
	}
	us := time.Since(t0).Microseconds()
	pollStats.n++
	pollStats.sumUs += us
	pollStats.maxUs = max(pollStats.maxUs, us)
	if pollStats.n%60 == 0 {
		logf("PROBE_COST n=%d avg=%dus max=%dus（07 章 7.6：每次 < 1 ms）", pollStats.n, pollStats.sumUs/pollStats.n, pollStats.maxUs)
	}
}

const tile = 50

type covGrid struct {
	cols, rows int
	cells      []bool
	hit        bool
}

// syscall.NewCallback 创建的回调永不释放且总数有上限（约 2000 个），所以 EnumWindows 回调只建一次，
// 通过包级变量 covGrids 传递状态（只在 UI 线程上使用）。第 19 轮在 Wine 长跑前发现：旧写法每次轮询新建回调，约 30 分钟后会 panic。
var (
	covGrids []covGrid
	covCB    uintptr
)

func coverCallback() uintptr {
	if covCB == 0 {
		covCB = syscall.NewCallback(func(h, _ uintptr) uintptr {
			if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
				return 1
			}
			if ic, _, _ := pIsIconic.Call(h); ic != 0 {
				return 1
			}
			ex, _, _ := pGetWindowLongPtrW.Call(h, gwlExStyle)
			if ex&wsExToolWindow != 0 || shellClasses[className(h)] || cloaked(h) {
				return 1
			}
			var r rect
			pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
			if debugCov {
				logf("COVDBG hwnd=0x%x class=%q rect=%v", h, className(h), r)
			}
			zoomed, _, _ := pIsZoomed.Call(h)
			wm, _, _ := pMonitorFromWindow.Call(h, monitorDefaultNull)
			for i, s := range screens {
				mr := s.mon.Monitor
				l, t, rr, b := max(r.L, mr.L), max(r.T, mr.T), min(r.R, mr.R), min(r.B, mr.B)
				if rr <= l || b <= t {
					continue
				}
				if zoomed != 0 && wm == s.hmon {
					covGrids[i].hit = true
				}
				if int64(rr-l)*int64(b-t)*100 >= int64(s.w())*int64(s.h())*95 {
					covGrids[i].hit = true
				}
				g := &covGrids[i]
				for y := int(t-mr.T) / tile; y < g.rows && y*tile < int(b-mr.T); y++ {
					for x := int(l-mr.L) / tile; x < g.cols && x*tile < int(rr-mr.L); x++ {
						g.cells[y*g.cols+x] = true
					}
				}
			}
			return 1
		})
	}
	return covCB
}

// 遮挡判定（07 章 7.4，Lively 算法）：50 px 网格，未覆盖 tile ≤ 5%；或该屏上有最大化窗口；或单个窗口覆盖 ≥ 95%。
func coverage() []bool {
	gs := make([]covGrid, len(screens))
	for i, s := range screens {
		c, r := int(s.w()+tile-1)/tile, int(s.h()+tile-1)/tile
		gs[i] = covGrid{cols: c, rows: r, cells: make([]bool, c*r)}
	}
	covGrids = gs
	pEnumWindows.Call(coverCallback(), 0)
	gs = covGrids
	out := make([]bool, len(screens))
	for i, g := range gs {
		free := 0
		for _, c := range g.cells {
			if !c {
				free++
			}
		}
		out[i] = g.hit || free*100 <= len(g.cells)*5
	}
	return out
}

// ---- 信号源 2：会话（锁屏、远程桌面）----
func onSession(code uintptr) {
	switch code {
	case wtsSessionLock:
		setReason(vis.Locked, -1, true, "wts")
	case wtsSessionUnlock:
		setReason(vis.Locked, -1, false, "wts")
	case wtsRemoteConnect, wtsRemoteDisconnect, wtsConsoleConnect, wtsConsoleDisconnect:
		r, _, _ := pGetSystemMetrics.Call(smRemoteSession)
		setReason(vis.Remote, -1, r != 0, fmt.Sprintf("wts/%d", code))
	}
}

// ---- 信号源 3：电源（交流/电池、电量、省电、显示器开关、睡眠唤醒）----
type powerBroadcastSetting struct {
	Guid       guid
	DataLength uint32
	Data       uint32
}

func onPower(w, l uintptr) {
	switch w {
	case pbtAPMSuspend:
		setReason(vis.Suspend, -1, true, "pbt")
	case pbtAPMResumeAutomatic:
		setReason(vis.Suspend, -1, false, "pbt")
	case pbtPowerSettingChange:
		ps := (*powerBroadcastSetting)(unsafe.Pointer(l))
		switch ps.Guid {
		case guidACDCPowerSource:
			onBattery = ps.Data != 0 // 0=交流，1=电池，2=短时 UPS
			setReason(vis.Battery, -1, onBattery, "power/acdc")
		case guidBatteryPercentage:
			batteryPct = int(ps.Data)
		case guidPowerSavingStatus:
			setReason(vis.BatterySaver, -1, ps.Data != 0, "power/saver")
		case guidConsoleDisplayState:
			setReason(vis.DisplayOff, -1, ps.Data == 0, "power/display") // 0=关，1=开，2=变暗（变暗不暂停）
		}
		low := onBattery && batteryPct >= 0 && batteryPct <= 20
		setReason(vis.LowBattery, -1, low, fmt.Sprintf("power/pct=%d", batteryPct))
	}
}

func pollScreensaver() {
	var running int32
	pSystemParametersInfoW.Call(spiGetScreenSaverRunning, 0, uintptr(unsafe.Pointer(&running)), 0)
	m := ctl.Display(0)
	if m != nil && (m.Effective()&vis.Screensaver != 0) != (running != 0) {
		setReason(vis.Screensaver, -1, running != 0, "spi")
	}
}

// ---- 健康检查 / 异步重挂 ----
func healthCheck() {
	if *flagStandalone || reattaching.Load() {
		return
	}
	want := workerw
	if raised {
		want = progman
	}
	for _, s := range screens {
		ok, _, _ := pIsWindow.Call(s.surface)
		p, _, _ := pGetParent.Call(s.surface)
		if ok == 0 || p != want {
			logf("HEALTH screen=%d 失败 isWindow=%d parent=0x%x want=0x%x → 重挂", s.id, ok, p, want)
			reattach("health")
			return
		}
	}
}

func reattach(why string) {
	if !reattaching.CompareAndSwap(false, true) {
		return
	}
	logf("REATTACH start reason=%s", why)
	for _, s := range screens {
		s.be.kill()
		s.started = false
		if s.surface != 0 {
			pDestroyWindow.Call(s.surface)
			s.surface = 0
		}
	}
	reattachTry = 0
	scheduleReattach()
}

func scheduleReattach() {
	d := reattachBack[min(reattachTry, len(reattachBack)-1)]
	go func() { time.Sleep(d); pPostMessageW.Call(msgWnd, wmAppReattach, 0, 0) }()
}

func reattachAttempt() {
	reattachTry++
	if err := createAllSurfaces(); err != nil {
		logf("REATTACH 第 %d 次失败: %v", reattachTry, err)
		for _, s := range screens {
			if s.surface != 0 {
				pDestroyWindow.Call(s.surface)
				s.surface = 0
			}
		}
		if reattachTry >= len(reattachBack) {
			logf("REATTACH 放弃（产品中降级为静态壁纸）")
			reattaching.Store(false)
			return
		}
		scheduleReattach()
		return
	}
	logf("REATTACH ok 第 %d 次", reattachTry)
	reattaching.Store(false)
	for _, s := range screens {
		if l := ctl.Display(s.id).Level(); l < vis.L4 {
			startScreen(s, 0, l > vis.L0)
		}
	}
}

func wndProc(h, m, w, l uintptr) uintptr {
	switch {
	case m == msgTaskbarCreated && msgTaskbarCreated != 0:
		logf("TASKBAR_CREATED（Explorer 重启）")
		reattach("TaskbarCreated")
		return 0
	case m == wmTimer && w == timerPoll:
		evalVisibility("timer")
		pollScreensaver()
		return 0
	case m == wmTimer && w == timerHealth:
		healthCheck()
		return 0
	case m == wmTimer && w == timerDeadline:
		pKillTimer.Call(msgWnd, timerDeadline)
		ctl.Advance(now())
		runActions()
		return 0
	case m == wmAppReattach:
		reattachAttempt()
		return 0
	case m == wmAppBackend:
		onBackend(int(w), l)
		return 0
	case m == wmAppUserToggle:
		userPaused = !userPaused
		setReason(vis.User, -1, userPaused, "pausetest")
		return 0
	case m == wmWTSSessionChange:
		onSession(w)
		return 0
	case m == wmPowerBroadcast:
		onPower(w, l)
		return 1
	case m == wmDisplayChange:
		logf("DISPLAY_CHANGE（原型不处理热插拔；产品中按 09 章重建）")
	}
	r, _, _ := pDefWindowProcW.Call(h, m, w, l)
	return r
}

func run() {
	runtime.LockOSThread()
	pSetProcessDpiAwarenessCtx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	hInst, _, _ = pGetModuleHandleW.Call(0)
	setupJob()

	cfg := vis.DefaultConfig(vis.Video)
	if *flagPauseTest > 0 { // 测试模式：缩短升级计时
		cfg.T23, cfg.T34 = *flagPauseTest/3, *flagPauseTest*2/3 // 一个暂停周期内依次经过 L1→L3→L4
	}
	ctl = vis.NewController(cfg)

	// 显示器：-monitor -1 = 全部；否则只用指定的一块（0=主屏）
	var hs []uintptr
	cb := syscall.NewCallback(func(h, _, _, _ uintptr) uintptr { hs = append(hs, h); return 1 })
	pEnumDisplayMonitors.Call(0, 0, cb, 0)
	for i, h := range hs {
		mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
		primary := mi.Flags&1 != 0
		logf("MONITOR #%d rect=%v primary=%v", i, mi.Monitor, primary)
		if *flagMonitor < 0 || (*flagMonitor == 0 && primary) || (*flagMonitor > 0 && i == *flagMonitor) {
			s := &screen{id: len(screens), mon: mi, hmon: h}
			screens = append(screens, s)
			ctl.AddDisplay(now(), s.id)
		}
	}
	if *flagStandalone && *flagFakeMon > 1 && len(screens) == 1 {
		base := screens[0]
		screens = screens[:0]
		ctl = vis.NewController(cfg)
		w := base.w() / int32(*flagFakeMon)
		for i := 0; i < *flagFakeMon; i++ {
			mi := base.mon
			mi.Monitor.L = base.mon.Monitor.L + int32(i)*w
			mi.Monitor.R = mi.Monitor.L + w
			s := &screen{id: i, mon: mi, hmon: base.hmon}
			screens = append(screens, s)
			ctl.AddDisplay(now(), i)
			logf("FAKEMONITOR #%d rect=%v", i, mi.Monitor)
		}
	}
	if len(screens) == 0 {
		logf("FATAL 找不到显示器 %d", *flagMonitor)
		return
	}

	proc := syscall.NewCallback(wndProc)
	for _, cn := range []string{"WpProtoSurface", "WpProtoMsg"} {
		wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: proc, Instance: hInst, ClassName: u16(cn)}
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
	msgWnd, _, _ = pCreateWindowExW.Call(wsExToolWindow, up(u16("WpProtoMsg")), up(u16("wpproto-msg")), wsPopup, 0, 0, 0, 0, 0, 0, hInst, 0)
	msgTaskbarCreated, _, _ = pRegisterWindowMessageW.Call(up(u16("TaskbarCreated")))

	if err := createAllSurfaces(); err != nil {
		logf("FATAL %v", err)
		return
	}
	for _, s := range screens {
		s.be = pickBackend(s.id)
		logf("BACKEND screen=%d %s", s.id, s.be.name())
	}

	// 信号源注册
	r1, _, e1 := pWTSRegisterSessionNotif.Call(msgWnd, 0 /*NOTIFY_FOR_THIS_SESSION*/)
	logf("REGISTER wts=%v err=%v", r1 != 0, e1)
	for name, g := range map[string]*guid{"acdc": &guidACDCPowerSource, "pct": &guidBatteryPercentage, "saver": &guidPowerSavingStatus, "display": &guidConsoleDisplayState} {
		r, _, e := pRegisterPowerSettingNotif.Call(msgWnd, uintptr(unsafe.Pointer(g)), 0 /*DEVICE_NOTIFY_WINDOW_HANDLE*/)
		logf("REGISTER power/%s=%v err=%v", name, r != 0, e)
	}
	if r, _, _ := pGetSystemMetrics.Call(smRemoteSession); r != 0 {
		setReason(vis.Remote, -1, true, "startup")
	}
	hookCB := syscall.NewCallback(func(_, ev, _, _, _, _, _ uintptr) uintptr {
		if ev == eventSystemForeground || ev == eventSystemMoveSizeEnd || ev == eventSystemMinimizeStart || ev == eventSystemMinimizeEnd {
			evalVisibility(fmt.Sprintf("hook/0x%x", ev))
		}
		return 0
	})
	pSetWinEventHook.Call(eventSystemForeground, eventSystemMinimizeEnd, 0, hookCB, 0, 0, winEventOutOfContext)
	pSetTimer.Call(msgWnd, timerPoll, 1000, 0)   // 兜底轮询 1 s（07 章 winPollFallback）
	pSetTimer.Call(msgWnd, timerHealth, 3000, 0) // 健康检查 3 s
	evalVisibility("startup")

	for _, s := range screens {
		startScreen(s, 0, ctl.Display(s.id).Level() > vis.L0)
	}
	if *flagPauseTest > 0 {
		go func() {
			for range time.Tick(*flagPauseTest) {
				pPostMessageW.Call(msgWnd, wmAppUserToggle, 0, 0)
			}
		}()
	}
	if *flagDuration > 0 {
		go func() { time.Sleep(*flagDuration); pPostMessageW.Call(msgWnd, 0x0012, 0, 0) }()
	}
	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if pollStats.n > 0 {
		logf("PROBE_COST n=%d avg=%dus max=%dus（退出时汇总）", pollStats.n, pollStats.sumUs/pollStats.n, pollStats.maxUs)
	}
	for _, s := range screens {
		s.be.kill()
		pDestroyWindow.Call(s.surface)
	}
}
