//go:build darwin

// macOS 宿主（04 章）：每块 NSScreen 一个桌面层级无边框 NSWindow（kCGDesktopWindowLevelKey，
// CanJoinAllSpaces|Stationary|IgnoresCycle，忽略鼠标），contentView 的 layer 交给 render/avf 挂 AVPlayerLayer。
// 可见性：后台 goroutine 每 250 ms 采样 CGWindowList（layer 0、非本进程窗口）做 50 pt 网格遮挡/全屏判定，
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
	o                   Options
	log                 *slog.Logger
	events              chan desktop.Event
	stop                chan struct{}
	once                sync.Once
	mu                  sync.Mutex
	scrs                []*scr
	surfs               map[objc.ID]*surface
	displaysInitialized bool

	thermal             int
	lowPower            bool
	foreground          desktop.ForegroundTracker
	foregroundSupported bool
}

func New(o Options) *Host {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Host{o: o, log: o.Log, events: make(chan desktop.Event, 256), stop: make(chan struct{}), surfs: map[objc.ID]*surface{}}
}

func (h *Host) emit(e desktop.Event) bool {
	select {
	case h.events <- e:
		return true
	default:
		return false
	}
}

func (h *Host) Start() error {
	if err := macos.Load(); err != nil {
		return err
	}
	macos.Main(func() {
		h.refresh()
		h.foregroundSupported = foregroundAvailable()
	})
	// Host 拥有采样循环；Close 关闭 stop 后退出。
	go h.sampler()
	// Host 拥有电池循环；Close 关闭 stop 后退出。
	go h.battery()
	// Host 拥有电源循环；Close 关闭 stop 后退出。
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
		bounds := image.Rect(int(b.Origin.X), int(b.Origin.Y), int(b.Origin.X+b.Size.W), int(b.Origin.Y+b.Size.H))
		var modePixels image.Point
		if mode := macos.CGDisplayCopyDisplayMode(num); mode != 0 {
			modePixels = image.Pt(int(macos.CGDisplayModeGetPixelWidth(mode)), int(macos.CGDisplayModeGetPixelHeight(mode)))
			macos.CGDisplayModeRelease(mode)
		}
		name := macos.GoString(s.Send(macos.Sel("localizedName")))
		out = append(out, &scr{cgID: num, ns: s, d: desktop.Display{ID: desktop.DisplayID("m-" + hex.EncodeToString(sum[:6])), Name: desktop.CleanName(name), Device: fmt.Sprint(num),
			Scale: scale, Primary: i == 0,
			Bounds: bounds, Pixels: displayPixels(bounds.Size(), modePixels, scale)}})
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
	preserveScreenSignals(h.scrs, out)
	previous := make([]desktop.Display, len(h.scrs))
	for i, s := range h.scrs {
		previous[i] = s.d
	}
	changed := h.displaysInitialized && !sameDisplayTopology(previous, ds)
	h.scrs = out
	h.displaysInitialized = true
	h.mu.Unlock()
	if changed {
		h.emit(desktop.Event{Kind: desktop.EventDisplaysChanged, Source: "NSScreen topology"})
	}
}

// refresh runs synchronously between sampler iterations. Preserve both the
// debounced state and in-flight counts, including across geometry changes, so
// a previous true signal can still produce its matching false notification.
func preserveScreenSignals(previous, current []*scr) {
	byID := make(map[desktop.DisplayID]*scr, len(previous))
	for _, s := range previous {
		byID[s.d.ID] = s
	}
	for _, s := range current {
		if old := byID[s.d.ID]; old != nil {
			s.fs, s.cov = old.fs, old.cov
		}
	}
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
	h      *Host
	win    objc.ID
	view   objc.ID // Renderer view; the NSWindow content view clips a spanning canvas.
	b      image.Rectangle
	closed bool // Accessed only on the AppKit main thread.
}

func (s *surface) Native() uintptr         { return uintptr(s.view) }
func (s *surface) Bounds() image.Rectangle { return s.b }
func (s *surface) SetVisible(v bool) error {
	var err error
	macos.Main(func() {
		if s.closed {
			err = errors.New("mac: surface closed")
			return
		}
		if v {
			s.win.Send(macos.Sel("orderFront:"), objc.ID(0))
		} else {
			s.win.Send(macos.Sel("orderOut:"), objc.ID(0))
		}
	})
	return err
}
func (s *surface) Close() error {
	macos.Main(func() {
		if s.closed {
			return
		}
		s.closed = true
		s.win.Send(macos.Sel("orderOut:"), objc.ID(0))
		s.win.Send(macos.Sel("close"))
		s.win.Send(macos.Sel("release"))
	})
	s.h.mu.Lock()
	if s.h.surfs[s.win] == s {
		delete(s.h.surfs, s.win)
	}
	s.h.mu.Unlock()
	return nil
}

func (h *Host) CreateSurface(id desktop.DisplayID) (desktop.Surface, error) {
	return h.createSurface(id, nil)
}

// CreateCanvasSurface preserves a physical display-sized NSWindow and clips a
// translated full-desktop rendering view inside its layer-backed content view.
func (h *Host) CreateCanvasSurface(id desktop.DisplayID, canvas image.Rectangle) (desktop.Surface, error) {
	return h.createSurface(id, &canvas)
}

func (h *Host) createSurface(id desktop.DisplayID, canvas *image.Rectangle) (desktop.Surface, error) {
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
	if canvas != nil {
		// Reject invalid geometry before allocating or touching native objects.
		if _, _, _, _, err := desktop.CanvasFrame(*canvas, sc.d.Bounds, float64(sc.d.Bounds.Dx()), float64(sc.d.Bounds.Dy()), false); err != nil {
			return nil, err
		}
	}
	var sf *surface
	var createErr error
	macos.Main(func() {
		frame := objc.Send[macos.CGRect](sc.ns, macos.Sel("frame"))
		win := macos.Class("NSWindow").Send(macos.Sel("alloc"))
		win = macos.MsgInitWindow(win, macos.Sel("initWithContentRect:styleMask:backing:defer:screen:"), frame, 0, 2, false, sc.ns)
		if win == 0 {
			createErr = errors.New("mac: cannot create desktop window")
			return
		}
		win.Send(macos.Sel("setLevel:"), int(macos.CGWindowLevelForKey(2))) // kCGDesktopWindowLevelKey
		win.Send(macos.Sel("setCollectionBehavior:"), uint(1|16|64))
		win.Send(macos.Sel("setIgnoresMouseEvents:"), true)
		win.Send(macos.Sel("setHasShadow:"), false)
		win.Send(macos.Sel("setReleasedWhenClosed:"), false)
		defer func() {
			if sf == nil {
				win.Send(macos.Sel("close"))
				win.Send(macos.Sel("release"))
			}
		}()
		win.Send(macos.Sel("setBackgroundColor:"), macos.Class("NSColor").Send(macos.Sel("blackColor")))
		view := win.Send(macos.Sel("contentView"))
		if view == 0 {
			createErr = errors.New("mac: desktop window has no content view")
			return
		}
		view.Send(macos.Sel("setWantsLayer:"), true)
		bounds := sc.d.Bounds
		if canvas != nil {
			native := objc.Send[macos.CGRect](view, macos.Sel("bounds"))
			// Desktop coordinates are top-down; an ordinary NSView's Y axis is
			// bottom-up. Do not use backingScaleFactor as a global conversion.
			x, y, w, ht, err := desktop.CanvasFrame(*canvas, sc.d.Bounds, native.Size.W, native.Size.H, false)
			if err != nil {
				createErr = err
				return
			}
			rootLayer := view.Send(macos.Sel("layer"))
			if rootLayer == 0 {
				createErr = errors.New("mac: desktop clipping layer unavailable")
				return
			}
			rootLayer.Send(macos.Sel("setMasksToBounds:"), true)
			inner := macos.Class("NSView").Send(macos.Sel("new"))
			if inner == 0 {
				createErr = errors.New("mac: cannot create canvas view")
				return
			}
			macos.MsgSetFrame(inner, macos.Sel("setFrame:"), macos.CGRect{
				Origin: macos.CGPoint{X: native.Origin.X + x, Y: native.Origin.Y + y},
				Size:   macos.CGSize{W: w, H: ht},
			})
			inner.Send(macos.Sel("setWantsLayer:"), true)
			if inner.Send(macos.Sel("layer")) == 0 {
				inner.Send(macos.Sel("release"))
				createErr = errors.New("mac: canvas rendering layer unavailable")
				return
			}
			view.Send(macos.Sel("addSubview:"), inner)
			inner.Send(macos.Sel("release")) // Retained by the content view.
			view, bounds = inner, *canvas
		}
		win.Send(macos.Sel("orderFront:"), objc.ID(0))
		sf = &surface{h: h, win: win, view: view, b: bounds}
	})
	if createErr != nil {
		return nil, createErr
	}
	h.mu.Lock()
	h.surfs[sf.win] = sf
	h.mu.Unlock()
	return sf, nil
}

func (h *Host) Health() error { return nil }

func (h *Host) Events() <-chan desktop.Event { return h.events }
func (h *Host) Caps() desktop.Caps {
	return desktop.Caps{Video: true, Web: true, Shader: true, PerDisplay: true, ForegroundApp: h.foregroundSupported, Spanning: true}
}

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
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	me := int32(os.Getpid())
	nextTopology := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
		var executable string
		if h.foregroundSupported {
			macos.Main(func() { executable = foregroundExecutable() })
		}
		h.foreground.Update(executable, "NSWorkspace", h.emit)
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
		if now := time.Now(); !now.Before(nextTopology) {
			nextTopology = now.Add(5 * time.Second)
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
var _ desktop.CanvasHost = (*Host)(nil)
