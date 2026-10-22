//go:build windows

// Package win 是 Windows 桌面宿主（03 章）：
//   - 每块显示器一个自建子 HWND，挂到 Progman（24H2+，WS_EX_LAYERED，插在 SHELLDLL_DefView 下方）或 WorkerW（旧版）；
//   - 一个专用 UI 线程（LockOSThread）跑消息循环，与 MyGo 的主线程互不干扰；其他 goroutine 通过 call() 把操作切到该线程；
//   - 可见性信号源（07 章 7.4）：WinEventHook（前台/最小化/移动结束）+ 1 s 兜底轮询（全屏、50 px 网格遮挡）、
//     WTS 会话通知（锁屏、远程桌面）、电源设置通知（交流/电池、电量、省电、显示器开关）、WM_POWERBROADCAST（睡眠/唤醒）、屏保；
//   - TaskbarCreated 与 3 s 健康检查 → EventShellRestarted，由 app 异步退避重挂（Q-P2）；
//   - 系统壁纸兜底：IDesktopWallpaper::SetWallpaper 按显示器设置，失败时退回 SPI_SETDESKWALLPAPER（2.5）。
//
// 只用标准库 syscall，CGO_ENABLED=0。代码来自原型 prototype/proto_windows.go（第 19 轮），改成 desktop.Host 接口。
package win

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"wpapp/internal/desktop"
	"wpapp/internal/visibility"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	wtsapi32 = syscall.NewLazyDLL("wtsapi32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

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
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pRegisterWindowMessageW    = user32.NewProc("RegisterWindowMessageW")
	pSetLayeredWindowAttrs     = user32.NewProc("SetLayeredWindowAttributes")
	pMapWindowPoints           = user32.NewProc("MapWindowPoints")
	pEnumDisplayMonitors       = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	pMonitorFromWindow         = user32.NewProc("MonitorFromWindow")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessID  = user32.NewProc("GetWindowThreadProcessId")
	pGetWindowRect             = user32.NewProc("GetWindowRect")
	pGetClientRect             = user32.NewProc("GetClientRect")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pIsWindow                  = user32.NewProc("IsWindow")
	pIsWindowVisible           = user32.NewProc("IsWindowVisible")
	pIsIconic                  = user32.NewProc("IsIconic")
	pIsZoomed                  = user32.NewProc("IsZoomed")
	pGetParent                 = user32.NewProc("GetParent")
	pShowWindow                = user32.NewProc("ShowWindow")
	pSetWinEventHook           = user32.NewProc("SetWinEventHook")
	pSetTimer                  = user32.NewProc("SetTimer")
	pKillTimer                 = user32.NewProc("KillTimer")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pSystemParametersInfoW     = user32.NewProc("SystemParametersInfoW")
	pRegisterPowerSettingNotif = user32.NewProc("RegisterPowerSettingNotification")
	pSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForMonitor          = syscall.NewLazyDLL("shcore.dll").NewProc("GetDpiForMonitor")
	pGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	pGetSystemPowerStatus      = kernel32.NewProc("GetSystemPowerStatus")
	pOpenProcess               = kernel32.NewProc("OpenProcess")
	pQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	pDwmGetWindowAttribute     = dwmapi.NewProc("DwmGetWindowAttribute")
	pSHQueryUserNotifState     = shell32.NewProc("SHQueryUserNotificationState")
	pWTSRegisterSessionNotif   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	pCoInitializeEx            = ole32.NewProc("CoInitializeEx")
	pCoCreateInstance          = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree             = ole32.NewProc("CoTaskMemFree")
)

const (
	wsChild, wsVisible, wsClipChildren, wsPopup                    = 0x40000000, 0x10000000, 0x02000000, 0x80000000
	wsExLayered, wsExNoRedirBitmap, wsExToolWindow, wsExNoActivate = 0x00080000, 0x00200000, 0x00000080, 0x08000000
	swpNoSize, swpNoMove, swpNoActivate                            = 0x1, 0x2, 0x10
	lwaAlpha                                                       = 0x2
	wmTimer, wmClose                                               = 0x0113, 0x0010
	wmPowerBroadcast, wmWTSSessionChange, wmDisplayChange          = 0x0218, 0x02B1, 0x007E
	wmAppCall                                                      = 0x8001
	eventSystemForeground, eventSystemMoveSizeEnd                  = 0x0003, 0x000B
	eventSystemMinimizeStart, eventSystemMinimizeEnd               = 0x0016, 0x0017
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
	timerPoll, timerHealth, timerDisplays                          = 1, 2, 3
	surfaceClass, msgClass                                         = "WpdSurface", "WpdMsg"
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

// 电源设置 GUID：前三个核对自 mingw-w64 winnt.h；GUID_POWER_SAVING_STATUS 取自微软文档。
var (
	guidACDCPowerSource     = guid{0x5d3e9a59, 0xe9d5, 0x4b00, [8]byte{0xa6, 0xbd, 0xff, 0x34, 0xff, 0x51, 0x65, 0x48}}
	guidBatteryPercentage   = guid{0xa7ad8041, 0xb45a, 0x4cae, [8]byte{0x87, 0xa3, 0xee, 0xcb, 0xb4, 0x68, 0xa9, 0xe1}}
	guidConsoleDisplayState = guid{0x6fe69556, 0x704a, 0x47a0, [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47}}
	guidPowerSavingStatus   = guid{0xe00958c0, 0xc213, 0x4ace, [8]byte{0xac, 0x77, 0xfe, 0xcc, 0xed, 0x2e, 0xee, 0xa5}}
	// IDesktopWallpaper（shobjidl_core.h）
	clsidDesktopWallpaper = guid{0xc2cf3110, 0x460e, 0x4fc1, [8]byte{0xb9, 0xd0, 0x8a, 0x1c, 0x0c, 0x9c, 0xc4, 0xbd}}
	iidIDesktopWallpaper  = guid{0xb92b56a9, 0x8b55, 0x4e14, [8]byte{0x9a, 0x89, 0x01, 0x99, 0xbb, 0xb6, 0xf9, 0x3b}}
)

type rect struct{ L, T, R, B int32 }
type monitorInfoEx struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
	Device        [32]uint16
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

// Options 是宿主参数。
type Options struct {
	Log          *slog.Logger
	Standalone   bool // 不挂桌面，开普通顶层窗口（Wine 自检、隔离挂载因素）
	FakeMonitors int  // 与 Standalone 合用：把主屏横向切成 N 块虚拟显示器
	PollEvery    time.Duration
}

type mon struct {
	d        desktop.Display
	hmon     uintptr
	r        rect
	wallPath string // IDesktopWallpaper 的 device path（SetSystemWallpaper 用）
	// 上次上报的探针结果
	lastFS, lastCov bool
}

// Host 实现 desktop.Host。
type Host struct {
	o       Options
	log     *slog.Logger
	events  chan desktop.Event
	dropped atomic.Uint64

	calls   chan func()
	msgWnd  uintptr
	ready   chan error
	stopped chan struct{}

	// 以下只在 UI 线程上读写
	hInst                     uintptr
	msgTaskbarCreated         uintptr
	progman, defView, workerw uintptr
	raised                    bool
	surfaces                  map[uintptr]*surface
	mons                      []*mon
	onBattery                 bool
	saver                     bool
	wall                      uintptr // IDesktopWallpaper*
	covGrids                  []covGrid
	pollN, pollSumUs, pollMax int64
	foreground                desktop.ForegroundTracker
	foregroundSupported       bool

	mu       sync.Mutex
	displays []desktop.Display
}

var (
	hostSingleton *Host // wndProc / 回调只能是包级函数（NewCallback 不可释放）
	cbFind        uintptr
	cbCover       uintptr
	cbMonitors    uintptr
	cbWndProc     uintptr
	cbHook        uintptr
	monList       []uintptr
)

// New 创建宿主（每个进程只能有一个）。
func New(o Options) *Host {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.PollEvery == 0 {
		o.PollEvery = time.Second
	}
	return &Host{o: o, log: o.Log, events: make(chan desktop.Event, 1024), calls: make(chan func(), 64),
		ready: make(chan error, 1), stopped: make(chan struct{}), surfaces: map[uintptr]*surface{}}
}

func (h *Host) emit(e desktop.Event) bool {
	select {
	case h.events <- e:
		return true
	default:
		h.dropped.Add(1)
		return false
	}
}

// Start 启动 UI 线程。
func (h *Host) Start() error {
	if hostSingleton != nil {
		return errors.New("win: host already started")
	}
	hostSingleton = h
	go h.uiThread()
	return <-h.ready
}

// call 在 UI 线程上执行 fn 并等待（超时 5 s）。
func (h *Host) call(fn func()) error {
	done := make(chan struct{})
	select {
	case h.calls <- func() { fn(); close(done) }:
	case <-h.stopped:
		return errors.New("win: host stopped")
	}
	pPostMessageW.Call(h.msgWnd, wmAppCall, 0, 0)
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("win: UI thread call timed out")
	case <-h.stopped:
		return errors.New("win: host stopped")
	}
}

func (h *Host) Events() <-chan desktop.Event { return h.events }
func (h *Host) Caps() desktop.Caps {
	return desktop.Caps{Video: true, Web: true, PerDisplay: true, ForegroundApp: h.foregroundSupported, Spanning: !h.o.Standalone}
}

func (h *Host) Displays() []desktop.Display {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]desktop.Display(nil), h.displays...)
}

// Close 结束 UI 线程（销毁全部表面）。
func (h *Host) Close() error {
	select {
	case <-h.stopped:
		return nil
	default:
	}
	pPostMessageW.Call(h.msgWnd, wmClose, 0, 0)
	select {
	case <-h.stopped:
	case <-time.After(3 * time.Second):
	}
	return nil
}

func (h *Host) uiThread() {
	runtime.LockOSThread()
	defer close(h.stopped)
	h.foregroundSupported = pGetForegroundWindow.Find() == nil && pGetWindowThreadProcessID.Find() == nil &&
		pOpenProcess.Find() == nil && pQueryFullProcessImageName.Find() == nil
	pSetProcessDpiAwarenessCtx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	pCoInitializeEx.Call(0, 2 /*COINIT_APARTMENTTHREADED*/)
	h.hInst, _, _ = pGetModuleHandleW.Call(0)
	if cbWndProc == 0 {
		cbWndProc = syscall.NewCallback(wndProc)
	}
	for _, cn := range []string{surfaceClass, msgClass} {
		wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: cbWndProc, Instance: h.hInst, ClassName: u16(cn)}
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
	h.msgWnd, _, _ = pCreateWindowExW.Call(wsExToolWindow, up(u16(msgClass)), up(u16("wpd-msg")), wsPopup, 0, 0, 0, 0, 0, 0, h.hInst, 0)
	if h.msgWnd == 0 {
		h.ready <- errors.New("win: cannot create message window")
		return
	}
	h.msgTaskbarCreated, _, _ = pRegisterWindowMessageW.Call(up(u16("TaskbarCreated")))
	var wp uintptr
	if r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidDesktopWallpaper)), 0, 4 /*CLSCTX_LOCAL_SERVER*/ |1,
		uintptr(unsafe.Pointer(&iidIDesktopWallpaper)), uintptr(unsafe.Pointer(&wp))); int32(r) >= 0 {
		h.wall = wp
	} else {
		h.log.Info("IDesktopWallpaper unavailable; falling back to SPI_SETDESKWALLPAPER", "hr", fmt.Sprintf("0x%08x", uint32(r)))
	}
	h.refreshMonitors()
	if !h.o.Standalone {
		if err := h.findDesktop(); err != nil {
			h.log.Warn("desktop not found at startup", "err", err)
		}
	}
	// 信号源
	r1, _, e1 := pWTSRegisterSessionNotif.Call(h.msgWnd, 0)
	h.log.Info("register wts", "ok", r1 != 0, "err", e1)
	for name, g := range map[string]*guid{"acdc": &guidACDCPowerSource, "pct": &guidBatteryPercentage, "saver": &guidPowerSavingStatus, "display": &guidConsoleDisplayState} {
		r, _, e := pRegisterPowerSettingNotif.Call(h.msgWnd, uintptr(unsafe.Pointer(g)), 0)
		h.log.Info("register power", "setting", name, "ok", r != 0, "err", e)
	}
	if cbHook == 0 {
		cbHook = syscall.NewCallback(func(_, ev, _, _, _, _, _ uintptr) uintptr {
			if hs := hostSingleton; hs != nil && (ev == eventSystemForeground || ev == eventSystemMoveSizeEnd || ev == eventSystemMinimizeStart || ev == eventSystemMinimizeEnd) {
				hs.evalVisibility()
			}
			return 0
		})
	}
	pSetWinEventHook.Call(eventSystemForeground, eventSystemMinimizeEnd, 0, cbHook, 0, 0, 0 /*OUTOFCONTEXT*/)
	pSetTimer.Call(h.msgWnd, timerPoll, uintptr(h.o.PollEvery.Milliseconds()), 0)
	pSetTimer.Call(h.msgWnd, timerHealth, 3000, 0)
	h.ready <- nil
	// 初始状态
	if r, _, _ := pGetSystemMetrics.Call(smRemoteSession); r != 0 {
		h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Remote, Active: true, Source: "startup"})
	}
	h.initialPower()
	h.evalVisibility()

	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	for hw := range h.surfaces {
		pDestroyWindow.Call(hw)
	}
	if h.pollN > 0 {
		h.log.Info("probe cost", "n", h.pollN, "avgUs", h.pollSumUs/h.pollN, "maxUs", h.pollMax)
	}
	hostSingleton = nil
}

type systemPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag byte
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

func (h *Host) initialPower() {
	var s systemPowerStatus
	if r, _, _ := pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return
	}
	h.onBattery = s.ACLineStatus == 0
	if h.onBattery {
		h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: true, Source: "startup"})
	}
	if s.BatteryLifePercent <= 100 {
		h.emit(desktop.Event{Kind: desktop.EventBattery, Percent: int(s.BatteryLifePercent), Source: "startup"})
	}
	if s.SystemStatusFlag&1 != 0 {
		h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.BatterySaver, Active: true, Source: "startup"})
	}
}

// ---- 显示器 ----

// sortMons 按物理排列排序并保证 ID 唯一（desktop.SortByLayout / Dedupe）。
func sortMons(mons []*mon) {
	sort.SliceStable(mons, func(i, j int) bool {
		a, b := mons[i].d.Bounds.Min, mons[j].d.Bounds.Min
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
	ds := make([]desktop.Display, len(mons))
	for i := range mons {
		ds[i] = mons[i].d
	}
	desktop.Dedupe(ds)
	for i := range mons {
		mons[i].d.ID = ds[i].ID
	}
}

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func (h *Host) refreshMonitors() {
	monList = monList[:0]
	if cbMonitors == 0 {
		cbMonitors = syscall.NewCallback(func(hm, _, _, _ uintptr) uintptr { monList = append(monList, hm); return 1 })
	}
	pEnumDisplayMonitors.Call(0, 0, cbMonitors, 0)
	paths := h.wallpaperMonitorPaths()
	targets := queryDisplayTargets()
	var mons []*mon
	for _, hm := range monList {
		mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		pGetMonitorInfoW.Call(hm, uintptr(unsafe.Pointer(&mi)))
		dev := syscall.UTF16ToString(mi.Device[:])
		eddPath, eddStr := enumMonitorDevice(dev)
		idn := monIdentity{GDI: dev, DC: targets[upper(dev)], EDDPath: eddPath, EDDString: eddStr, WallPath: paths[mi.Monitor]}
		id, name, legacy := idn.resolve()
		var dx, dy uint32
		scale := 1.0
		if r, _, _ := pGetDpiForMonitor.Call(hm, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			scale = float64(dx) / 96
		}
		d := desktop.Display{ID: id, Name: name, Device: dev, LegacyIDs: legacy, Scale: scale, Primary: mi.Flags&1 != 0,
			Bounds: image.Rect(int(mi.Monitor.L), int(mi.Monitor.T), int(mi.Monitor.R), int(mi.Monitor.B))}
		h.log.Info("monitor", "gdi", dev, "id", id, "name", name, "dcPath", dcPath(idn.DC) != "", "eddPath", eddPath != "", "wallPath", idn.WallPath != "")
		mons = append(mons, &mon{d: d, hmon: hm, r: mi.Monitor, wallPath: idn.WallPath})
	}
	if h.o.Standalone && h.o.FakeMonitors > 1 && len(mons) > 0 {
		base := mons[0]
		for _, m := range mons {
			if m.d.Primary {
				base = m
			}
		}
		mons = nil
		w := (base.r.R - base.r.L) / int32(h.o.FakeMonitors)
		for i := 0; i < h.o.FakeMonitors; i++ {
			r := base.r
			r.L = base.r.L + int32(i)*w
			r.R = r.L + w
			d := base.d
			d.ID = desktop.DisplayID(fmt.Sprintf("fake-%d", i))
			d.Name = fmt.Sprintf("虚拟屏 %d", i+1)
			d.LegacyIDs = nil
			d.Primary = i == 0
			d.Bounds = image.Rect(int(r.L), int(r.T), int(r.R), int(r.B))
			mons = append(mons, &mon{d: d, hmon: base.hmon, r: r})
		}
	}
	sortMons(mons)
	h.mons = mons
	ds := make([]desktop.Display, len(mons))
	for i, m := range mons {
		ds[i] = m.d
	}
	h.mu.Lock()
	h.displays = ds
	h.mu.Unlock()
}

func vcall(obj uintptr, idx int, args ...uintptr) uint32 {
	vt := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vt + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return uint32(r)
}

// IDesktopWallpaper vtable：3 SetWallpaper，5 GetMonitorDevicePathAt，6 GetMonitorDevicePathCount，7 GetMonitorRECT。
func (h *Host) wallpaperMonitorPaths() map[rect]string {
	out := map[rect]string{}
	if h.wall == 0 {
		return out
	}
	var n uint32
	if int32(vcall(h.wall, 6, uintptr(unsafe.Pointer(&n)))) < 0 {
		return out
	}
	for i := uint32(0); i < n; i++ {
		var p *uint16
		if int32(vcall(h.wall, 5, uintptr(i), uintptr(unsafe.Pointer(&p)))) < 0 || p == nil {
			continue
		}
		s := utf16PtrToString(p)
		var r rect
		if int32(vcall(h.wall, 7, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&r)))) >= 0 {
			out[r] = s
		}
		pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	}
	return out
}

func utf16PtrToString(p *uint16) string {
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
		if len(s) > 4096 {
			break
		}
	}
	return syscall.UTF16ToString(s)
}

// SetSystemWallpaper 按显示器设置系统壁纸（首帧截图兜底，2.5）。
func (h *Host) SetSystemWallpaper(id desktop.DisplayID, path string) error {
	var err error
	cerr := h.call(func() {
		if h.wall != 0 {
			paths := h.wallpaperMonitorPaths()
			for _, m := range h.mons {
				if m.d.ID != id {
					continue
				}
				if p, ok := paths[m.r]; ok {
					if hr := vcall(h.wall, 3, up(u16(p)), up(u16(path))); int32(hr) >= 0 {
						return
					} else {
						err = fmt.Errorf("IDesktopWallpaper::SetWallpaper hr=0x%08x", hr)
					}
				}
			}
		}
		// 回退：所有显示器同一张（SPIF_UPDATEINIFILE|SPIF_SENDCHANGE）
		if r, _, e := pSystemParametersInfoW.Call(0x0014, 0, up(u16(path)), 3); r == 0 {
			err = fmt.Errorf("SPI_SETDESKWALLPAPER: %v", e)
		} else {
			err = nil
		}
	})
	if cerr != nil {
		return cerr
	}
	return err
}

// ---- 桌面层级（参照 Lively WinDesktopCore.cs）----

func (h *Host) findDesktop() error {
	h.progman, _, _ = pFindWindowW.Call(up(u16("Progman")), 0)
	if h.progman == 0 {
		return errors.New("Progman not found")
	}
	var r uintptr
	pSendMessageTimeoutW.Call(h.progman, 0x052C, 0xD, 0x1, 0, 1000, uintptr(unsafe.Pointer(&r)))
	ex, _, _ := pGetWindowLongPtrW.Call(h.progman, gwlExStyle)
	h.raised = ex&wsExNoRedirBitmap != 0
	h.workerw, h.defView = 0, 0
	if h.raised { // 24H2+：DefView 和 WorkerW 都是 Progman 的子窗口
		h.defView, _, _ = pFindWindowExW.Call(h.progman, 0, up(u16("SHELLDLL_DefView")), 0)
		h.workerw, _, _ = pFindWindowExW.Call(h.progman, 0, up(u16("WorkerW")), 0)
	} else {
		if cbFind == 0 {
			cbFind = syscall.NewCallback(func(top, _ uintptr) uintptr {
				hs := hostSingleton
				if p, _, _ := pFindWindowExW.Call(top, 0, up(u16("SHELLDLL_DefView")), 0); p != 0 && hs != nil {
					hs.defView = p
					hs.workerw, _, _ = pFindWindowExW.Call(0, top, up(u16("WorkerW")), 0)
				}
				return 1
			})
		}
		pEnumWindows.Call(cbFind, 0)
	}
	h.log.Info("desktop", "progman", fmt.Sprintf("0x%x", h.progman), "raised24H2", h.raised,
		"defView", fmt.Sprintf("0x%x", h.defView), "workerw", fmt.Sprintf("0x%x", h.workerw))
	if h.workerw == 0 {
		return errors.New("WorkerW not found")
	}
	return nil
}

func (h *Host) parent() uintptr {
	if h.raised {
		return h.progman
	}
	return h.workerw
}

type surface struct {
	h          *Host
	hwnd       uintptr // Physical display viewport; owns visibility and lifetime.
	canvasHwnd uintptr // Optional full-desktop child, clipped by hwnd.
	b          image.Rectangle
}

func (s *surface) Native() uintptr {
	if s.canvasHwnd != 0 {
		return s.canvasHwnd
	}
	return s.hwnd
}
func (s *surface) Bounds() image.Rectangle { return s.b }
func (s *surface) SetVisible(v bool) error {
	cmd := uintptr(0) // SW_HIDE
	if v {
		cmd = 8 // SW_SHOWNA
	}
	var err error
	if callErr := s.h.call(func() {
		if s.h.surfaces[s.hwnd] != s {
			err = desktop.ErrDetached
			return
		}
		pShowWindow.Call(s.hwnd, cmd)
	}); callErr != nil {
		return callErr
	}
	return err
}
func (s *surface) Close() error {
	return s.h.call(func() {
		// Explorer can destroy old windows before delayed renderer cleanup runs.
		// A reused numeric HWND must never identify a newer surface as our own.
		if s.h.surfaces[s.hwnd] == s {
			pDestroyWindow.Call(s.hwnd)
			delete(s.h.surfaces, s.hwnd)
		}
	})
}

// CreateSurface 为一块显示器建子 HWND。
func (h *Host) CreateSurface(id desktop.DisplayID) (desktop.Surface, error) {
	var s *surface
	var err error
	cerr := h.call(func() { s, err = h.createSurface(id) })
	if cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// CreateCanvasSurface gives the renderer a full-desktop child HWND while the
// existing per-display desktop window remains its clipping and ownership root.
// Standalone windows have captions and may be reduced to 1280x720, so they do
// not claim to represent the physical desktop's spanning geometry.
func (h *Host) CreateCanvasSurface(id desktop.DisplayID, canvas image.Rectangle) (desktop.Surface, error) {
	if h.o.Standalone {
		return nil, errors.New("win: spanning is unavailable in standalone mode")
	}
	var s *surface
	var err error
	if cerr := h.call(func() { s, err = h.createCanvasSurface(id, canvas) }); cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (h *Host) createCanvasSurface(id desktop.DisplayID, canvas image.Rectangle) (*surface, error) {
	var viewport image.Rectangle
	for _, m := range h.mons {
		if m.d.ID == id {
			viewport = m.d.Bounds
			break
		}
	}
	if viewport.Empty() {
		return nil, fmt.Errorf("win: unknown or empty display %s", id)
	}
	// Validate before allocating a visible native window.
	if _, _, _, _, err := desktop.CanvasFrame(canvas, viewport, float64(viewport.Dx()), float64(viewport.Dy()), true); err != nil {
		return nil, err
	}
	s, err := h.createSurface(id)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			pDestroyWindow.Call(s.hwnd)
			delete(h.surfaces, s.hwnd)
		}
	}()
	var client rect
	if r, _, callErr := pGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&client))); r == 0 {
		return nil, fmt.Errorf("win: canvas GetClientRect: %v", callErr)
	}
	// Use this window's actual native size, not another output's DPI scale.
	x, y, w, ht, err := desktop.CanvasFrame(canvas, viewport, float64(client.R-client.L), float64(client.B-client.T), true)
	if err != nil {
		return nil, err
	}
	hw, _, callErr := pCreateWindowExW.Call(wsExNoActivate, up(u16(surfaceClass)), up(u16("wallpaperd-canvas")),
		wsChild|wsVisible|wsClipChildren, uintptr(int32(math.Round(x))), uintptr(int32(math.Round(y))),
		uintptr(int32(math.Round(w))), uintptr(int32(math.Round(ht))), s.hwnd, 0, h.hInst, 0)
	if hw == 0 {
		return nil, fmt.Errorf("win: CreateWindowEx(canvas): %v", callErr)
	}
	s.canvasHwnd, s.b = hw, canvas
	ok = true
	return s, nil
}

func (h *Host) createSurface(id desktop.DisplayID) (*surface, error) {
	var m *mon
	for _, x := range h.mons {
		if x.d.ID == id {
			m = x
		}
	}
	if m == nil {
		return nil, fmt.Errorf("unknown display %s", id)
	}
	if h.o.Standalone {
		w, ht := m.r.R-m.r.L, m.r.B-m.r.T
		if w > 1280 {
			w, ht = 1280, 720
		}
		hw, _, err := pCreateWindowExW.Call(0, up(u16(surfaceClass)), up(u16("wallpaperd-standalone-"+m.d.Device)),
			wsVisible|0x00C00000 /*WS_CAPTION*/, uintptr(m.r.L), uintptr(m.r.T), uintptr(w), uintptr(ht), 0, 0, h.hInst, 0)
		if hw == 0 {
			return nil, fmt.Errorf("CreateWindowEx(standalone): %v", err)
		}
		s := &surface{h: h, hwnd: hw, b: image.Rect(int(m.r.L), int(m.r.T), int(m.r.L+w), int(m.r.T+ht))}
		h.surfaces[hw] = s
		return s, nil
	}
	if ok, _, _ := pIsWindow.Call(h.parent()); h.parent() == 0 || ok == 0 {
		if err := h.findDesktop(); err != nil {
			return nil, err
		}
	}
	parent := h.parent()
	r := m.r
	pMapWindowPoints.Call(0, parent, uintptr(unsafe.Pointer(&r)), 2)
	ex := uintptr(wsExNoActivate)
	if h.raised {
		ex |= wsExLayered
	}
	hw, _, err := pCreateWindowExW.Call(ex, up(u16(surfaceClass)), up(u16("wallpaperd")),
		wsChild|wsVisible|wsClipChildren, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), parent, 0, h.hInst, 0)
	if hw == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", err)
	}
	if h.raised {
		pSetLayeredWindowAttrs.Call(hw, 0, 255, lwaAlpha)
		pSetWindowPos.Call(hw, h.defView, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		pSetWindowPos.Call(h.workerw, 1 /*HWND_BOTTOM*/, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
	s := &surface{h: h, hwnd: hw, b: m.d.Bounds}
	h.surfaces[hw] = s
	h.log.Info("surface", "display", id, "hwnd", fmt.Sprintf("0x%x", hw), "parent", fmt.Sprintf("0x%x", parent), "layered", h.raised)
	return s, nil
}

// Health 检查所有表面是否仍挂在正确的父窗口下。
func (h *Host) Health() error {
	var err error
	if cerr := h.call(func() { err = h.health() }); cerr != nil {
		return cerr
	}
	return err
}

func (h *Host) health() error {
	if h.o.Standalone {
		return nil
	}
	want := h.parent()
	for hw, s := range h.surfaces {
		ok, _, _ := pIsWindow.Call(hw)
		p, _, _ := pGetParent.Call(hw)
		if ok == 0 || p != want {
			return fmt.Errorf("%w: hwnd=0x%x parent=0x%x want=0x%x", desktop.ErrDetached, hw, p, want)
		}
		if s.canvasHwnd != 0 {
			ok, _, _ = pIsWindow.Call(s.canvasHwnd)
			p, _, _ = pGetParent.Call(s.canvasHwnd)
			if ok == 0 || p != hw {
				return fmt.Errorf("%w: canvas=0x%x parent=0x%x want=0x%x", desktop.ErrDetached, s.canvasHwnd, p, hw)
			}
		}
	}
	return nil
}

// ---- 信号源 1：前台全屏 + 遮挡网格（按屏）----

func className(hw uintptr) string {
	buf := make([]uint16, 256)
	pGetClassNameW.Call(hw, uintptr(unsafe.Pointer(&buf[0])), 256)
	return syscall.UTF16ToString(buf)
}

var shellClasses = map[string]bool{"Progman": true, "WorkerW": true, "Shell_TrayWnd": true, "Shell_SecondaryTrayWnd": true,
	"Windows.UI.Core.CoreWindow": true, "XamlExplorerHostIslandWindow": true, surfaceClass: true, msgClass: true}

func cloaked(hw uintptr) bool {
	var c uint32
	pDwmGetWindowAttribute.Call(hw, dwmwaCloaked, uintptr(unsafe.Pointer(&c)), 4)
	return c != 0
}

func (h *Host) evalVisibility() {
	t0 := time.Now()
	fg, _, _ := pGetForegroundWindow.Call()
	var executable string
	if h.foregroundSupported {
		executable = foregroundExecutable(fg)
	}
	h.foreground.Update(executable, "win32-process", h.emit)
	fsOn := make([]bool, len(h.mons))
	if fg != 0 && !shellClasses[className(fg)] && !cloaked(fg) {
		m, _, _ := pMonitorFromWindow.Call(fg, monitorDefaultNull)
		var r rect
		pGetWindowRect.Call(fg, uintptr(unsafe.Pointer(&r)))
		var quns uint32
		pSHQueryUserNotifState.Call(uintptr(unsafe.Pointer(&quns)))
		for i, s := range h.mons {
			mr := s.r
			covers := r.L <= mr.L && r.T <= mr.T && r.R >= mr.R && r.B >= mr.B
			if h.o.FakeMonitors > 1 {
				fsOn[i] = covers
			} else {
				fsOn[i] = m == s.hmon && (covers || (quns == qunsRunningD3DFull && !h.o.Standalone))
			}
		}
	}
	cov := h.coverage()
	for i, s := range h.mons {
		if fsOn[i] != s.lastFS {
			s.lastFS = fsOn[i]
			h.emit(desktop.Event{Kind: desktop.EventSignal, Display: s.d.ID, Reason: visibility.Fullscreen, Active: fsOn[i], Source: "fg"})
		}
		if cov[i] != s.lastCov {
			s.lastCov = cov[i]
			h.emit(desktop.Event{Kind: desktop.EventSignal, Display: s.d.ID, Reason: visibility.Covered, Active: cov[i], Source: "grid"})
		}
	}
	us := time.Since(t0).Microseconds()
	h.pollN++
	h.pollSumUs += us
	h.pollMax = max(h.pollMax, us)
}

const tile = 50

type covGrid struct {
	cols, rows int
	cells      []bool
	hit        bool
}

// 遮挡判定（07 章 7.4，Lively 算法）：50 px 网格，未覆盖 tile ≤ 5%；或该屏上有最大化窗口；或单个窗口覆盖 ≥ 95%。
// EnumWindows 回调只建一次（NewCallback 不释放且总数有上限，第 19 轮修复的泄漏）。
func (h *Host) coverage() []bool {
	gs := make([]covGrid, len(h.mons))
	for i, s := range h.mons {
		w, ht := int(s.r.R-s.r.L), int(s.r.B-s.r.T)
		c, r := (w+tile-1)/tile, (ht+tile-1)/tile
		gs[i] = covGrid{cols: c, rows: r, cells: make([]bool, c*r)}
	}
	h.covGrids = gs
	if cbCover == 0 {
		cbCover = syscall.NewCallback(func(hw, _ uintptr) uintptr {
			hs := hostSingleton
			if hs == nil {
				return 0
			}
			if v, _, _ := pIsWindowVisible.Call(hw); v == 0 {
				return 1
			}
			if ic, _, _ := pIsIconic.Call(hw); ic != 0 {
				return 1
			}
			ex, _, _ := pGetWindowLongPtrW.Call(hw, gwlExStyle)
			if ex&wsExToolWindow != 0 || shellClasses[className(hw)] || cloaked(hw) {
				return 1
			}
			var r rect
			pGetWindowRect.Call(hw, uintptr(unsafe.Pointer(&r)))
			zoomed, _, _ := pIsZoomed.Call(hw)
			wm, _, _ := pMonitorFromWindow.Call(hw, monitorDefaultNull)
			for i, s := range hs.mons {
				mr := s.r
				l, t, rr, b := max(r.L, mr.L), max(r.T, mr.T), min(r.R, mr.R), min(r.B, mr.B)
				if rr <= l || b <= t {
					continue
				}
				g := &hs.covGrids[i]
				if zoomed != 0 && wm == s.hmon && hs.o.FakeMonitors <= 1 {
					g.hit = true
				}
				if int64(rr-l)*int64(b-t)*100 >= int64(mr.R-mr.L)*int64(mr.B-mr.T)*95 {
					g.hit = true
				}
				for y := int(t-mr.T) / tile; y < g.rows && y*tile < int(b-mr.T); y++ {
					for x := int(l-mr.L) / tile; x < g.cols && x*tile < int(rr-mr.L); x++ {
						g.cells[y*g.cols+x] = true
					}
				}
			}
			return 1
		})
	}
	pEnumWindows.Call(cbCover, 0)
	out := make([]bool, len(h.mons))
	for i, g := range h.covGrids {
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

// ---- 信号源 2/3：会话、电源 ----

type powerBroadcastSetting struct {
	Guid       guid
	DataLength uint32
	Data       uint32
}

func (h *Host) global(r visibility.Reason, on bool, src string) {
	h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: r, Active: on, Source: src})
}

func (h *Host) onSession(code uintptr) {
	switch code {
	case wtsSessionLock:
		h.global(visibility.Locked, true, "wts")
	case wtsSessionUnlock:
		h.global(visibility.Locked, false, "wts")
	case wtsRemoteConnect, wtsRemoteDisconnect, wtsConsoleConnect, wtsConsoleDisconnect:
		r, _, _ := pGetSystemMetrics.Call(smRemoteSession)
		h.global(visibility.Remote, r != 0, "wts")
	}
}

func (h *Host) onPower(w, l uintptr) {
	switch w {
	case pbtAPMSuspend:
		h.global(visibility.Suspend, true, "pbt")
	case pbtAPMResumeAutomatic:
		h.global(visibility.Suspend, false, "pbt")
	case pbtPowerSettingChange:
		ps := (*powerBroadcastSetting)(unsafe.Pointer(l))
		switch ps.Guid {
		case guidACDCPowerSource:
			h.onBattery = ps.Data != 0 // 0=交流，1=电池，2=短时 UPS
			h.global(visibility.Battery, h.onBattery, "power/acdc")
		case guidBatteryPercentage:
			h.emit(desktop.Event{Kind: desktop.EventBattery, Percent: int(ps.Data), Source: "power/pct"})
		case guidPowerSavingStatus:
			h.global(visibility.BatterySaver, ps.Data != 0, "power/saver")
		case guidConsoleDisplayState:
			h.global(visibility.DisplayOff, ps.Data == 0, "power/display") // 0=关，1=开，2=变暗（不暂停）
		}
	}
}

func (h *Host) pollScreensaver() {
	var running int32
	pSystemParametersInfoW.Call(spiGetScreenSaverRunning, 0, uintptr(unsafe.Pointer(&running)), 0)
	if (running != 0) != h.saver {
		h.saver = running != 0
		h.global(visibility.Screensaver, h.saver, "spi")
	}
}

func wndProc(hw, m, w, l uintptr) uintptr {
	h := hostSingleton
	if h == nil || hw != h.msgWnd {
		r, _, _ := pDefWindowProcW.Call(hw, m, w, l)
		return r
	}
	switch {
	case m == wmAppCall:
		for {
			select {
			case fn := <-h.calls:
				fn()
				continue
			default:
			}
			break
		}
		return 0
	case m == h.msgTaskbarCreated && h.msgTaskbarCreated != 0:
		h.log.Info("TaskbarCreated (Explorer restarted)")
		for s := range h.surfaces {
			pDestroyWindow.Call(s)
			delete(h.surfaces, s)
		}
		h.progman, h.workerw, h.defView = 0, 0, 0
		h.emit(desktop.Event{Kind: desktop.EventShellRestarted, Source: "TaskbarCreated"})
		return 0
	case m == wmTimer && w == timerPoll:
		h.evalVisibility()
		h.pollScreensaver()
		return 0
	case m == wmTimer && w == timerHealth:
		if len(h.surfaces) > 0 {
			if err := h.health(); err != nil {
				h.log.Warn("health", "err", err)
				for s := range h.surfaces {
					pDestroyWindow.Call(s)
					delete(h.surfaces, s)
				}
				h.emit(desktop.Event{Kind: desktop.EventShellRestarted, Source: "health"})
			}
		}
		return 0
	case m == wmTimer && w == timerDisplays:
		pKillTimer.Call(h.msgWnd, timerDisplays)
		h.refreshMonitors()
		h.emit(desktop.Event{Kind: desktop.EventDisplaysChanged, Source: "WM_DISPLAYCHANGE"})
		return 0
	case m == wmWTSSessionChange:
		h.onSession(w)
		return 0
	case m == wmPowerBroadcast:
		h.onPower(w, l)
		return 1
	case m == wmDisplayChange:
		pSetTimer.Call(h.msgWnd, timerDisplays, 500, 0) // 合并 500 ms 内的多次变化
		return 0
	case m == wmClose:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hw, m, w, l)
	return r
}

var _ desktop.Host = (*Host)(nil)
var _ desktop.CanvasHost = (*Host)(nil)
