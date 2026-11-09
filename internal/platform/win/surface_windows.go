//go:build windows

// Package win marshals all HWND operations onto the existing MyGo UI thread.
package win

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"wpapp/internal/platform"
)

var Main func(func())
var user32 = syscall.NewLazyDLL("user32.dll")
var (
	findWindow    = user32.NewProc("FindWindowW")
	findWindowEx  = user32.NewProc("FindWindowExW")
	sendTimeout   = user32.NewProc("SendMessageTimeoutW")
	enumWindows   = user32.NewProc("EnumWindows")
	getLong       = user32.NewProc("GetWindowLongPtrW")
	setPos        = user32.NewProc("SetWindowPos")
	registerClass = user32.NewProc("RegisterClassExW")
	createWindow  = user32.NewProc("CreateWindowExW")
	destroyWindow = user32.NewProc("DestroyWindow")
	defProc       = user32.NewProc("DefWindowProcW")
	mapPoints     = user32.NewProc("MapWindowPoints")
	isWindow      = user32.NewProc("IsWindow")
	isVisible     = user32.NewProc("IsWindowVisible")
	getParent     = user32.NewProc("GetParent")
	showWindow    = user32.NewProc("ShowWindow")
	getClass      = user32.NewProc("GetClassNameW")
	setAlpha      = user32.NewProc("SetLayeredWindowAttributes")
	getMonitor    = user32.NewProc("GetMonitorInfoW")
	monitorPoint  = user32.NewProc("MonitorFromPoint")
)

type rect struct{ L, T, R, B int32 }
type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}
type windowClass struct {
	Size, Style                   uint32
	Proc                          uintptr
	ClassExtra, WindowExtra       int32
	Instance, Icon, Cursor, Brush uintptr
	Menu, Name                    *uint16
	IconSmall                     uintptr
}

var classOnce sync.Once
var classErr error
var instance uintptr
var enumCallback uintptr
var foundWorker, foundIcons uintptr // only accessed on MyGo's UI thread
const className = "WallpaperM1Surface"

// Surface owns one initially hidden window; Player must close before Surface.
// Windows 24H2 uses Progman + a WorkerW sibling; older shells use WorkerW parent.
type Surface struct {
	hwnd, parent, worker, icons uintptr
	raised, closed              bool
	w, h                        int
}

func register() error {
	classOnce.Do(func() {
		instance, _, _ = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
		proc := syscall.NewCallback(func(hwnd, msg, w, l uintptr) uintptr {
			if msg == 0x84 {
				return ^uintptr(0)
			} // HTTRANSPARENT: desktop icons keep input.
			r, _, _ := defProc.Call(hwnd, msg, w, l)
			return r
		})
		wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: proc, Instance: instance, Name: U16(className)}
		if r, _, e := registerClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("win: RegisterClassEx: %v", e)
		}
		enumCallback = syscall.NewCallback(func(top, _ uintptr) uintptr {
			icons, _, _ := findWindowEx.Call(top, 0, UP(U16("SHELLDLL_DefView")), 0)
			if icons != 0 {
				foundIcons = icons
				foundWorker, _, _ = findWindowEx.Call(0, top, UP(U16("WorkerW")), 0)
			}
			return 1
		})
	})
	return classErr
}
func New(ctx context.Context) (*Surface, error) {
	var s *Surface
	var err error
	Main(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		if err = register(); err != nil {
			return
		}
		progman, _, _ := findWindow.Call(UP(U16("Progman")), 0)
		if progman == 0 {
			err = errors.New("win: Progman not found")
			return
		}
		var result uintptr
		if r, _, e := sendTimeout.Call(progman, 0x052c, 0xd, 1, 2, 1000, uintptr(unsafe.Pointer(&result))); r == 0 {
			err = fmt.Errorf("win: create WorkerW: %v", e)
			return
		}
		style, _, _ := getLong.Call(progman, ^uintptr(19))
		raised := style&0x00200000 != 0
		parent, worker, icons := uintptr(0), uintptr(0), uintptr(0)
		if raised {
			icons, _, _ = findWindowEx.Call(progman, 0, UP(U16("SHELLDLL_DefView")), 0)
			worker, _, _ = findWindowEx.Call(progman, 0, UP(U16("WorkerW")), 0)
			parent = progman
		} else {
			foundWorker, foundIcons = 0, 0
			enumWindows.Call(enumCallback, 0)
			worker, icons = foundWorker, foundIcons
			parent = worker
		}
		if worker == 0 || icons == 0 {
			err = errors.New("win: desktop WorkerW or icon view unavailable")
			return
		}
		hm, _, _ := monitorPoint.Call(0, 1) // MONITOR_DEFAULTTOPRIMARY, POINT(0,0).
		mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		if r, _, e := getMonitor.Call(hm, uintptr(unsafe.Pointer(&mi))); r == 0 {
			err = fmt.Errorf("win: GetMonitorInfo: %v", e)
			return
		}
		r := mi.Monitor
		w, h := int(r.R-r.L), int(r.B-r.T)
		mapPoints.Call(0, parent, uintptr(unsafe.Pointer(&r)), 2)
		ex := uintptr(0x08000000 | 0x80)
		if raised {
			ex |= 0x80000
		}
		hwnd, _, e := createWindow.Call(ex, UP(U16(className)), UP(U16("wallpaperd")), 0x40000000|0x02000000, uintptr(r.L), uintptr(r.T), uintptr(w), uintptr(h), parent, 0, instance, 0)
		if hwnd == 0 {
			err = fmt.Errorf("win: CreateWindowEx: %v", e)
			return
		}
		s = &Surface{hwnd: hwnd, parent: parent, worker: worker, icons: icons, raised: raised, w: w, h: h}
		if raised {
			if ok, _, e := setAlpha.Call(hwnd, 0, 255, 2); ok == 0 {
				err = fmt.Errorf("win: SetLayeredWindowAttributes: %v", e)
			}
			setPos.Call(hwnd, icons, 0, 0, 0, 0, 1|2|16)
			setPos.Call(worker, 1, 0, 0, 0, 0, 1|2|16)
		}
	})
	return s, err
}
func (s *Surface) Native() uintptr  { return s.hwnd }
func (s *Surface) Size() (int, int) { return s.w, s.h }
func (s *Surface) Show() error {
	var err error
	Main(func() {
		if !s.valid() {
			err = errors.New("win: surface detached")
			return
		}
		showWindow.Call(s.hwnd, 8)
	})
	return err
}
func windowType(hwnd uintptr) string {
	var b [256]uint16
	getClass.Call(hwnd, uintptr(unsafe.Pointer(&b[0])), 256)
	return syscall.UTF16ToString(b[:])
}
func (s *Surface) valid() bool {
	if s.closed {
		return false
	}
	ok, _, _ := isWindow.Call(s.hwnd)
	worker, _, _ := isWindow.Call(s.worker)
	parent, _, _ := getParent.Call(s.hwnd)
	if ok == 0 || worker == 0 || parent != s.parent || windowType(s.worker) != "WorkerW" || windowType(s.hwnd) != className {
		return false
	}
	if s.raised {
		p, _, _ := getParent.Call(s.worker)
		return p == s.parent && windowType(p) == "Progman"
	}
	return s.parent == s.worker
}
func (s *Surface) Evidence() (platform.SurfaceEvidence, error) {
	e := platform.SurfaceEvidence{Visible: platform.No, OnScreen: platform.Unknown, Occluded: platform.Unknown, Attached: platform.No, Window: s.hwnd, Parent: s.parent}
	Main(func() {
		valid := s.valid()
		e.Attached = platform.Known(valid)
		if !valid {
			return
		}
		visible, _, _ := isVisible.Call(s.hwnd)
		e.Visible = platform.Known(visible != 0)
		monitor, _, _ := user32.NewProc("MonitorFromWindow").Call(s.hwnd, 0)
		e.OnScreen = platform.Known(monitor != 0)
	})
	return e, nil
}
func (s *Surface) Close() error {
	var err error
	Main(func() {
		if s.closed {
			return
		}
		exists, _, _ := isWindow.Call(s.hwnd)
		if exists != 0 {
			var pid uint32
			user32.NewProc("GetWindowThreadProcessId").Call(s.hwnd, uintptr(unsafe.Pointer(&pid)))
			// A reparented window is still ours and must be destroyed. A reused
			// foreign HWND must never be destroyed during delayed cleanup.
			if pid == uint32(os.Getpid()) && windowType(s.hwnd) == className {
				if r, _, e := destroyWindow.Call(s.hwnd); r == 0 {
					err = fmt.Errorf("win: DestroyWindow: %v", e)
				}
			}
		}

		s.closed = true
	})
	return err
}
