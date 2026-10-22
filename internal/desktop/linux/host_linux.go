//go:build linux

// Package linux 是 Ubuntu/X11 宿主（05 章，接口级兼容）：
// 每个 X screen 一个 _NET_WM_WINDOW_TYPE_DESKTOP 窗口（纯 Go xgb），渲染器用 mpv --wid 画进去。
// MVP 范围：单显示器（X screen 根窗口尺寸）；不做 RandR 多屏、不做全屏/遮挡探针；
// 电池通过 /sys/class/power_supply 轮询；系统壁纸用 gsettings（GNOME）尽力设置。
// Wayland 会话下只会以 XWayland 普通窗口出现（05 章），启动时警告。
package linux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"wpapp/internal/desktop"
	"wpapp/internal/visibility"
)

type Options struct {
	Log *slog.Logger
	// PowerSupplyDir 默认 /sys/class/power_supply（测试可替换）
	PowerSupplyDir string
}

type Host struct {
	o                   Options
	log                 *slog.Logger
	c                   *xgb.Conn
	scr                 *xproto.ScreenInfo
	events              chan desktop.Event
	stop                chan struct{}
	mu                  sync.Mutex
	surfs               map[xproto.Window]bool
	once                sync.Once
	foreground          desktop.ForegroundTracker
	foregroundSupported bool
	foregroundAtoms     foregroundAtoms
	foregroundHostname  string
}

func New(o Options) *Host {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.PowerSupplyDir == "" {
		o.PowerSupplyDir = "/sys/class/power_supply"
	}
	return &Host{o: o, log: o.Log, events: make(chan desktop.Event, 256), stop: make(chan struct{}), surfs: map[xproto.Window]bool{}}
}

func (h *Host) Start() error {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		h.log.Warn("Wayland session: the X11 desktop window will be an ordinary XWayland window, not a wallpaper (ch.05)")
	}
	c, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("x11: %w", err)
	}
	h.c = c
	h.scr = xproto.Setup(c).DefaultScreen(c)
	h.initForeground()
	// Host 拥有事件读取循环；Close 关闭 X11 连接后退出。
	go h.drain()
	// Host 拥有信号轮询；Close 关闭 stop 后退出。
	go h.pollSignals()
	return nil
}

func (h *Host) drain() {
	for {
		ev, err := h.c.WaitForEvent()
		if ev == nil && err == nil {
			return // 连接关闭
		}
		if _, ok := ev.(xproto.ConfigureNotifyEvent); ok {
			h.emit(desktop.Event{Kind: desktop.EventDisplaysChanged, Source: "ConfigureNotify"})
		}
	}
}

func (h *Host) emit(e desktop.Event) bool {
	select {
	case h.events <- e:
		return true
	default:
		return false
	}
}

// pollSignals samples foreground identity every second and battery every five
// seconds. Keeping one probe loop avoids extra timers or child processes.
func (h *Host) pollSignals() {
	last, lastPct := false, -2
	var nextBattery time.Time
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		var executable string
		if h.foregroundSupported {
			executable = h.foregroundExecutable()
		}
		h.foreground.Update(executable, "x11-ewmh", h.emit)
		if now := time.Now(); !now.Before(nextBattery) {
			nextBattery = now.Add(5 * time.Second)
			on, pct := ReadBattery(h.o.PowerSupplyDir)
			if on != last {
				last = on
				h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: on, Source: "sysfs"})
			}
			if pct != lastPct {
				lastPct = pct
				h.emit(desktop.Event{Kind: desktop.EventBattery, Percent: pct, Source: "sysfs"})
			}
		}
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
	}
}

// ReadBattery 返回（是否电池供电，电量%；-1=无电池）。
func ReadBattery(dir string) (bool, int) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		typ, _ := os.ReadFile(filepath.Join(p, "type"))
		if strings.TrimSpace(string(typ)) != "Battery" {
			continue
		}
		st, _ := os.ReadFile(filepath.Join(p, "status"))
		cap, _ := os.ReadFile(filepath.Join(p, "capacity"))
		n, err := strconv.Atoi(strings.TrimSpace(string(cap)))
		if err != nil {
			n = -1
		}
		return strings.TrimSpace(string(st)) == "Discharging", n
	}
	return false, -1
}

func (h *Host) Displays() []desktop.Display {
	if h.scr == nil {
		return nil
	}
	return []desktop.Display{{ID: "x11-0", Device: os.Getenv("DISPLAY"), Scale: 1, Primary: true,
		Bounds: image.Rect(0, 0, int(h.scr.WidthInPixels), int(h.scr.HeightInPixels))}}
}

func (h *Host) atom(name string) xproto.Atom {
	r, err := xproto.InternAtom(h.c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return r.Atom
}

func u32le(v ...uint32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return b
}

type surface struct {
	h   *Host
	wid xproto.Window
	b   image.Rectangle
}

func (s *surface) Native() uintptr         { return uintptr(s.wid) }
func (s *surface) Bounds() image.Rectangle { return s.b }
func (s *surface) SetVisible(v bool) error {
	if v {
		xproto.MapWindow(s.h.c, s.wid)
		xproto.ConfigureWindow(s.h.c, s.wid, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeBelow})
	} else {
		xproto.UnmapWindow(s.h.c, s.wid)
	}
	s.h.c.Sync()
	return nil
}
func (s *surface) Close() error {
	s.h.mu.Lock()
	delete(s.h.surfs, s.wid)
	s.h.mu.Unlock()
	return xproto.DestroyWindowChecked(s.h.c, s.wid).Check()
}

func (h *Host) CreateSurface(id desktop.DisplayID) (desktop.Surface, error) {
	if h.c == nil {
		return nil, errors.New("x11: not started")
	}
	if id != "x11-0" {
		return nil, fmt.Errorf("x11: unknown display %s", id)
	}
	w, ht := h.scr.WidthInPixels, h.scr.HeightInPixels
	wid, err := xproto.NewWindowId(h.c)
	if err != nil {
		return nil, err
	}
	if err := xproto.CreateWindowChecked(h.c, h.scr.RootDepth, wid, h.scr.Root, 0, 0, w, ht, 0,
		xproto.WindowClassInputOutput, h.scr.RootVisual, xproto.CwBackPixel|xproto.CwEventMask,
		[]uint32{0, xproto.EventMaskStructureNotify}).Check(); err != nil {
		return nil, err
	}
	xproto.ChangeProperty(h.c, xproto.PropModeReplace, wid, h.atom("_NET_WM_WINDOW_TYPE"), xproto.AtomAtom, 32, 1, u32le(uint32(h.atom("_NET_WM_WINDOW_TYPE_DESKTOP"))))
	var st []uint32
	for _, n := range []string{"_NET_WM_STATE_BELOW", "_NET_WM_STATE_STICKY", "_NET_WM_STATE_SKIP_TASKBAR", "_NET_WM_STATE_SKIP_PAGER"} {
		st = append(st, uint32(h.atom(n)))
	}
	xproto.ChangeProperty(h.c, xproto.PropModeReplace, wid, h.atom("_NET_WM_STATE"), xproto.AtomAtom, 32, uint32(len(st)), u32le(st...))
	title := "wallpaperd-desktop"
	xproto.ChangeProperty(h.c, xproto.PropModeReplace, wid, xproto.AtomWmName, xproto.AtomString, 8, uint32(len(title)), []byte(title))
	s := &surface{h: h, wid: wid, b: image.Rect(0, 0, int(w), int(ht))}
	s.SetVisible(true)
	h.mu.Lock()
	h.surfs[wid] = true
	h.mu.Unlock()
	h.log.Info("surface", "display", id, "xid", fmt.Sprintf("0x%x", uint32(wid)))
	return s, nil
}

func (h *Host) Health() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.surfs {
		if _, err := xproto.GetGeometry(h.c, xproto.Drawable(w)).Reply(); err != nil {
			return fmt.Errorf("%w: %v", desktop.ErrDetached, err)
		}
	}
	return nil
}

func (h *Host) Events() <-chan desktop.Event { return h.events }
func (h *Host) Caps() desktop.Caps {
	return desktop.Caps{Video: true, ForegroundApp: h.foregroundSupported}
}

// SetSystemWallpaper：GNOME gsettings（尽力而为；没有 gsettings 时返回错误，app 只记日志）。
func (h *Host) SetSystemWallpaper(_ desktop.DisplayID, path string) error {
	gs, err := exec.LookPath("gsettings")
	if err != nil {
		return errors.New("gsettings not found; system wallpaper not set")
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	for _, k := range []string{"picture-uri", "picture-uri-dark"} {
		if out, err := exec.Command(gs, "set", "org.gnome.desktop.background", k, uri).CombinedOutput(); err != nil && k == "picture-uri" {
			return fmt.Errorf("gsettings: %v %s", err, out)
		}
	}
	return nil
}

func (h *Host) Close() error {
	h.once.Do(func() {
		close(h.stop)
		if h.c != nil {
			h.c.Close()
		}
	})
	return nil
}

var _ desktop.Host = (*Host)(nil)
