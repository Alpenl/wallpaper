//go:build darwin

// macOS 宿主（04 章）：每块 NSScreen 一个桌面层级无边框 NSWindow（kCGDesktopWindowLevelKey，
// CanJoinAllSpaces|Stationary|IgnoresCycle，忽略鼠标），contentView 的 layer 交给 render/avf 挂 AVPlayerLayer。
// 可见性：后台 goroutine 每秒采样 CGWindowList（layer 0、非本进程窗口）做 50 pt 网格遮挡/全屏判定，
// 连续 3 次一致才上报（Wallnetic 策略，原型 proto_darwin.go）。电源：IOKit 1 s 采样 AC/电池，NSProcessInfo 1 s 采样低电量模式与 thermalState（power_darwin.go），pmset -g batt 30 s 报电量。
// 锁屏/睡眠由 UI 层用 mygo.Power 转发（见 internal/ui）。
// 未在真机验证（盒子上无 macOS）：只保证交叉编译通过。
package mac

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"wpapp/internal/desktop"
	"wpapp/internal/platform/macos"
	"wpapp/internal/visibility"
)

type Options struct{ Log *slog.Logger }

type scr struct {
	d      desktop.Display
	cgID   uint32
	ns     objc.ID
	cov    macDeb
	fs     macDeb
	frameK string
}
type macDeb = Debounce3

type Host struct {
	o      Options
	log    *slog.Logger
	events chan desktop.Event
	stop   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	scrs   []*scr
	surfs  map[objc.ID]*surface

	thermal  int
	lowPower bool
}

func New(o Options) *Host {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Host{o: o, log: o.Log, events: make(chan desktop.Event, 256), stop: make(chan struct{}), surfs: map[objc.ID]*surface{}}
}

func (h *Host) emit(e desktop.Event) {
	select {
	case h.events <- e:
	default:
	}
}

func (h *Host) Start() error {
	if err := macos.Load(); err != nil {
		return err
	}
	macos.Main(h.refresh)
	go h.sampler()
	go h.battery()
	go h.powerLoop()
	return nil
}

// refresh 在主线程上枚举 NSScreen。
func (h *Host) refresh() {
	screens := macos.Class("NSScreen").Send(macos.Sel("screens"))
	n := objc.Send[uint](screens, macos.Sel("count"))
	var out []*scr
	for i := uint(0); i < n; i++ {
		s := screens.Send(macos.Sel("objectAtIndex:"), i)
		num := objc.Send[uint32](s.Send(macos.Sel("deviceDescription")).Send(macos.Sel("objectForKey:"), macos.NSString("NSScreenNumber")), macos.Sel("unsignedIntValue"))
		key := fmt.Sprint(num)
		if u := macos.CGDisplayCreateUUID(num); u != 0 {
			if cs := macos.CFUUIDCreateString(0, u); cs != 0 {
				key = macos.GoString(objc.ID(cs))
				macos.CFRelease(cs)
			}
			macos.CFRelease(u)
		}
		sum := sha256.Sum256([]byte(key))
		b := macos.CGDisplayBounds(num)
		scale := objc.Send[float64](s, macos.Sel("backingScaleFactor"))
		name := macos.GoString(s.Send(macos.Sel("localizedName")))
		out = append(out, &scr{cgID: num, ns: s, d: desktop.Display{ID: desktop.DisplayID("m-" + hex.EncodeToString(sum[:6])), Name: desktop.CleanName(name), Device: fmt.Sprint(num),
			Scale: scale, Primary: i == 0,
			Bounds: image.Rect(int(b.Origin.X), int(b.Origin.Y), int(b.Origin.X+b.Size.W), int(b.Origin.Y+b.Size.H))}})
	}
	// 按物理排列排序；两块同型号、EDID 无序列号的屏 UUID 可能相同，去重后加 #2。
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].d.Bounds.Min, out[j].d.Bounds.Min
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
	ds := make([]desktop.Display, len(out))
	for i := range out {
		ds[i] = out[i].d
	}
	desktop.Dedupe(ds)
	for i := range out {
		out[i].d.ID = ds[i].ID
	}
	h.mu.Lock()
	h.scrs = out
	h.mu.Unlock()
}

func (h *Host) Displays() []desktop.Display {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ds []desktop.Display
	for _, s := range h.scrs {
		ds = append(ds, s.d)
	}
	return ds
}

type surface struct {
	h    *Host
	win  objc.ID
	view objc.ID
	b    image.Rectangle
}

func (s *surface) Native() uintptr         { return uintptr(s.view) }
func (s *surface) Bounds() image.Rectangle { return s.b }
func (s *surface) SetVisible(v bool) error {
	macos.Main(func() {
		if v {
			s.win.Send(macos.Sel("orderFront:"), objc.ID(0))
		} else {
			s.win.Send(macos.Sel("orderOut:"), objc.ID(0))
		}
	})
	return nil
}
func (s *surface) Close() error {
	macos.Main(func() {
		s.win.Send(macos.Sel("orderOut:"), objc.ID(0))
		s.win.Send(macos.Sel("close"))
		s.win.Send(macos.Sel("release"))
	})
	s.h.mu.Lock()
	delete(s.h.surfs, s.win)
	s.h.mu.Unlock()
	return nil
}

func (h *Host) CreateSurface(id desktop.DisplayID) (desktop.Surface, error) {
	h.mu.Lock()
	var sc *scr
	for _, s := range h.scrs {
		if s.d.ID == id {
			sc = s
		}
	}
	h.mu.Unlock()
	if sc == nil {
		return nil, fmt.Errorf("mac: unknown display %s", id)
	}
	var sf *surface
	macos.Main(func() {
		frame := objc.Send[macos.CGRect](sc.ns, macos.Sel("frame"))
		win := macos.Class("NSWindow").Send(macos.Sel("alloc"))
		win = macos.MsgInitWindow(win, macos.Sel("initWithContentRect:styleMask:backing:defer:screen:"), frame, 0, 2, false, sc.ns)
		win.Send(macos.Sel("setLevel:"), int(macos.CGWindowLevelForKey(2))) // kCGDesktopWindowLevelKey
		win.Send(macos.Sel("setCollectionBehavior:"), uint(1|16|64))
		win.Send(macos.Sel("setIgnoresMouseEvents:"), true)
		win.Send(macos.Sel("setHasShadow:"), false)
		win.Send(macos.Sel("setReleasedWhenClosed:"), false)
		win.Send(macos.Sel("setBackgroundColor:"), macos.Class("NSColor").Send(macos.Sel("blackColor")))
		view := win.Send(macos.Sel("contentView"))
		view.Send(macos.Sel("setWantsLayer:"), true)
		win.Send(macos.Sel("orderFront:"), objc.ID(0))
		sf = &surface{h: h, win: win, view: view, b: sc.d.Bounds}
	})
	h.mu.Lock()
	h.surfs[sf.win] = sf
	h.mu.Unlock()
	return sf, nil
}

func (h *Host) Health() error { return nil }

func (h *Host) Events() <-chan desktop.Event { return h.events }
func (h *Host) Caps() desktop.Caps           { return desktop.Caps{Video: true, PerDisplay: true} }

// SetSystemWallpaper：NSWorkspace setDesktopImageURL:forScreen:options:error:
func (h *Host) SetSystemWallpaper(id desktop.DisplayID, path string) error {
	h.mu.Lock()
	var sc *scr
	for _, s := range h.scrs {
		if s.d.ID == id {
			sc = s
		}
	}
	h.mu.Unlock()
	if sc == nil {
		return errors.New("mac: unknown display")
	}
	ok := false
	macos.Main(func() {
		ws := macos.Class("NSWorkspace").Send(macos.Sel("sharedWorkspace"))
		url := macos.Class("NSURL").Send(macos.Sel("fileURLWithPath:"), macos.NSString(path))
		opts := macos.Class("NSDictionary").Send(macos.Sel("dictionary"))
		ok = objc.Send[bool](ws, macos.Sel("setDesktopImageURL:forScreen:options:error:"), url, sc.ns, opts, objc.ID(0))
	})
	if !ok {
		return errors.New("setDesktopImageURL failed")
	}
	return nil
}

func (h *Host) sampler() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	me := int32(os.Getpid())
	lastN := -1
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
		var wins []Rect
		if arr := macos.CGWindowListCopyWindowInfo(1|16, 0); arr != 0 {
			for i, n := 0, macos.CFArrayGetCount(arr); i < n; i++ {
				d := macos.CFArrayGetValueAtIndex(arr, i)
				var layer, pid int32
				macos.CFNumberGetValue(macos.CFDictionaryGetValue(d, macos.KLayer), 3, unsafe.Pointer(&layer))
				macos.CFNumberGetValue(macos.CFDictionaryGetValue(d, macos.KPID), 3, unsafe.Pointer(&pid))
				if layer != 0 || pid == me {
					continue
				}
				var r macos.CGRect
				if macos.CGRectMakeWithDict(macos.CFDictionaryGetValue(d, macos.KBounds), &r) {
					wins = append(wins, Rect{r.Origin.X, r.Origin.Y, r.Size.W, r.Size.H})
				}
			}
			macos.CFRelease(arr)
		}
		h.mu.Lock()
		scrs := h.scrs
		h.mu.Unlock()
		if lastN >= 0 && lastN != len(scrs) {
			h.emit(desktop.Event{Kind: desktop.EventDisplaysChanged, Source: "poll"})
		}
		lastN = len(scrs)
		for _, s := range scrs {
			b := macos.CGDisplayBounds(s.cgID)
			cov, fs := Covered(Rect{b.Origin.X, b.Origin.Y, b.Size.W, b.Size.H}, wins)
			if s.fs.Push(fs) {
				h.emit(desktop.Event{Kind: desktop.EventSignal, Display: s.d.ID, Reason: visibility.Fullscreen, Active: s.fs.State, Source: "cgwindowlist"})
			}
			if s.cov.Push(cov) {
				h.emit(desktop.Event{Kind: desktop.EventSignal, Display: s.d.ID, Reason: visibility.Covered, Active: s.cov.State, Source: "cgwindowlist"})
			}
		}
		// 屏幕增删：每 5 s 在主线程重新枚举
		if time.Now().Unix()%5 == 0 {
			macos.Main(h.refresh)
		}
	}
}

func (h *Host) battery() {
	last, lastPct := false, -2
	for {
		out, err := exec.Command("/usr/bin/pmset", "-g", "batt").Output()
		if err == nil {
			on, pct := ParsePmset(string(out))
			if on != last && !loadIOKit() { // IOKit 可用时由 powerLoop 1 s 采样上报，pmset 只报电量
				last = on
				h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: on, Source: "pmset"})
			}
			if pct != lastPct {
				lastPct = pct
				h.emit(desktop.Event{Kind: desktop.EventBattery, Percent: pct, Source: "pmset"})
			}
		}
		select {
		case <-h.stop:
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// Emit 允许 UI 层转发 mygo.Power 的锁屏/睡眠事件。
func (h *Host) Emit(e desktop.Event) { h.emit(e) }

func (h *Host) Close() error {
	h.once.Do(func() { close(h.stop) })
	h.mu.Lock()
	var ss []*surface
	for _, s := range h.surfs {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	for _, s := range ss {
		s.Close()
	}
	return nil
}

var _ desktop.Host = (*Host)(nil)
