//go:build darwin

// macOS 原型：desktop 层级 NSWindow（4 种 level 可切换）+ AVQueuePlayer/AVPlayerLooper/AVPlayerLayer，
// 每个采样周期同时记录 occlusionState 与 CGWindowList 覆盖率/全屏判定和 CGWindowList 调用耗时（M1/M2/M4、Q-M6）。
// purego + objc，CGO_ENABLED=0。所有 AppKit 调用都在主线程（init 里 LockOSThread）。
package main

import (
	"math"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func init() { runtime.LockOSThread() }

func osName() string { return "darwin" }

type CGPoint struct{ X, Y float64 }
type CGSize struct{ W, H float64 }
type CGRect struct {
	Origin CGPoint
	Size   CGSize
}

var (
	sel = objc.RegisterName

	cgWindowLevelForKey       func(int32) int32
	cgWindowListCopyWindowInf func(uint32, uint32) uintptr
	cgRectMakeWithDict        func(uintptr, *CGRect) bool
	cgDisplayBounds           func(uint32) CGRect
	cfArrayGetCount           func(uintptr) int
	cfArrayGetValueAtIndex    func(uintptr, int) uintptr
	cfDictionaryGetValue      func(uintptr, uintptr) uintptr
	cfNumberGetValue          func(uintptr, int, unsafe.Pointer) bool
	cfRelease                 func(uintptr)

	msgInitWindow func(objc.ID, objc.SEL, CGRect, uint, uint, bool, objc.ID) objc.ID
	msgSetFrame   func(objc.ID, objc.SEL, CGRect)

	kLayer, kBounds, kPID uintptr
)

func mustOpen(p string) uintptr {
	h, err := purego.Dlopen(p, purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		logf("FATAL dlopen %s: %v", p, err)
		os.Exit(1)
	}
	return h
}

func symPtr(h uintptr, name string) uintptr { // 读取 extern 全局变量（如 CFStringRef 常量）
	a, err := purego.Dlsym(h, name)
	if err != nil {
		logf("FATAL dlsym %s: %v", name, err)
		os.Exit(1)
	}
	return **(**uintptr)(unsafe.Pointer(&a))
}

func nsstr(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), s)
}

func loadLibs() {
	mustOpen("/System/Library/Frameworks/AppKit.framework/AppKit")
	av := mustOpen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation")
	mustOpen("/System/Library/Frameworks/QuartzCore.framework/QuartzCore")
	cg := mustOpen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
	cf := mustOpen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	lo := mustOpen("/usr/lib/libobjc.A.dylib")
	purego.RegisterLibFunc(&cgWindowLevelForKey, cg, "CGWindowLevelForKey")
	purego.RegisterLibFunc(&cgWindowListCopyWindowInf, cg, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&cgRectMakeWithDict, cg, "CGRectMakeWithDictionaryRepresentation")
	purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
	purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	ms, _ := purego.Dlsym(lo, "objc_msgSend")
	purego.RegisterFunc(&msgInitWindow, ms) // 带 struct 参数的消息用强类型签名
	purego.RegisterFunc(&msgSetFrame, ms)
	kLayer, kBounds, kPID = symPtr(cg, "kCGWindowLayer"), symPtr(cg, "kCGWindowBounds"), symPtr(cg, "kCGWindowOwnerPID")
	gravityFill = objc.ID(symPtr(av, "AVLayerVideoGravityResizeAspectFill"))
}

var gravityFill objc.ID

func levelFor(name string) int {
	desk := int(cgWindowLevelForKey(2))  // kCGDesktopWindowLevelKey
	icon := int(cgWindowLevelForKey(18)) // kCGDesktopIconWindowLevelKey
	switch name {
	case "desktop-1":
		return desk - 1
	case "desktop+1":
		return desk + 1
	case "desktopicon-1":
		return icon - 1
	default:
		return desk
	}
}

// 采样：CGWindowList 中 layer 0、非本进程窗口对目标显示器的覆盖（50pt 网格，同 Lively 的判定）
func sampleCG(disp CGRect) (covered, fullscreen bool, n int, frac float64, cost time.Duration) {
	t0 := time.Now()
	arr := cgWindowListCopyWindowInf(1|16, 0) // OnScreenOnly | ExcludeDesktopElements
	if arr == 0 {
		return
	}
	defer cfRelease(arr)
	const tile = 50.0
	cols, rows := int(math.Ceil(disp.Size.W/tile)), int(math.Ceil(disp.Size.H/tile))
	grid := make([]bool, cols*rows)
	cnt := 0
	me := int32(os.Getpid())
	for i, total := 0, cfArrayGetCount(arr); i < total; i++ {
		d := cfArrayGetValueAtIndex(arr, i)
		var layer, pid int32
		cfNumberGetValue(cfDictionaryGetValue(d, kLayer), 3 /*kCFNumberSInt32Type*/, unsafe.Pointer(&layer))
		cfNumberGetValue(cfDictionaryGetValue(d, kPID), 3, unsafe.Pointer(&pid))
		if layer != 0 || pid == me {
			continue
		}
		var r CGRect
		if !cgRectMakeWithDict(cfDictionaryGetValue(d, kBounds), &r) {
			continue
		}
		n++
		if r.Origin.X <= disp.Origin.X+2 && r.Origin.Y <= disp.Origin.Y+2 &&
			r.Origin.X+r.Size.W >= disp.Origin.X+disp.Size.W-2 && r.Origin.Y+r.Size.H >= disp.Origin.Y+disp.Size.H-2 {
			fullscreen = true
		}
		x0, y0 := int((r.Origin.X-disp.Origin.X)/tile), int((r.Origin.Y-disp.Origin.Y)/tile)
		x1, y1 := int((r.Origin.X+r.Size.W-disp.Origin.X-1)/tile), int((r.Origin.Y+r.Size.H-disp.Origin.Y-1)/tile)
		for y := max(0, y0); y <= min(rows-1, y1); y++ {
			for x := max(0, x0); x <= min(cols-1, x1); x++ {
				if !grid[y*cols+x] {
					grid[y*cols+x] = true
					cnt++
				}
			}
		}
	}
	frac = float64(cnt) / float64(len(grid))
	covered = fullscreen || 1-frac <= 0.05
	cost = time.Since(t0)
	return
}

func run() {
	loadLibs()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	app := objc.ID(objc.GetClass("NSApplication")).Send(sel("sharedApplication"))
	app.Send(sel("setActivationPolicy:"), 1) // Accessory：无 Dock 图标
	app.Send(sel("finishLaunching"))

	screens := objc.ID(objc.GetClass("NSScreen")).Send(sel("screens"))
	if objc.Send[uint](screens, sel("count")) <= uint(*flagMonitor) {
		logf("FATAL 没有显示器 #%d", *flagMonitor)
		return
	}
	screen := screens.Send(sel("objectAtIndex:"), uint(*flagMonitor))
	frame := objc.Send[CGRect](screen, sel("frame"))
	dispID := objc.Send[uint32](screen.Send(sel("deviceDescription")).Send(sel("objectForKey:"), nsstr("NSScreenNumber")), sel("unsignedIntValue"))
	disp := cgDisplayBounds(dispID) // CG 坐标（左上原点），用于和 CGWindowList 比较
	logf("SCREEN #%d frame=%v displayID=%d cgBounds=%v", *flagMonitor, frame, dispID, disp)

	win := objc.ID(objc.GetClass("NSWindow")).Send(sel("alloc"))
	win = msgInitWindow(win, sel("initWithContentRect:styleMask:backing:defer:screen:"), frame, 0 /*Borderless*/, 2 /*Buffered*/, false, screen)
	lvl := levelFor(*flagLevel)
	win.Send(sel("setLevel:"), lvl)
	behavior := uint(1 | 16 | 64) // CanJoinAllSpaces | Stationary | IgnoresCycle
	if *flagFSAux {
		behavior |= 256 // FullScreenAuxiliary
	}
	win.Send(sel("setCollectionBehavior:"), behavior)
	win.Send(sel("setIgnoresMouseEvents:"), true)
	win.Send(sel("setHasShadow:"), false)
	win.Send(sel("setReleasedWhenClosed:"), false)
	win.Send(sel("setBackgroundColor:"), objc.ID(objc.GetClass("NSColor")).Send(sel("blackColor")))
	logf("WINDOW level=%s(%d) desktopKey=%d iconKey=%d behavior=0x%x", *flagLevel, lvl, cgWindowLevelForKey(2), cgWindowLevelForKey(18), behavior)

	// AVQueuePlayer + AVPlayerLooper + AVPlayerLayer
	url := objc.ID(objc.GetClass("NSURL")).Send(sel("fileURLWithPath:"), nsstr(*flagVideo))
	item := objc.ID(objc.GetClass("AVPlayerItem")).Send(sel("playerItemWithURL:"), url)
	player := objc.ID(objc.GetClass("AVQueuePlayer")).Send(sel("new"))
	player.Send(sel("setMuted:"), true)
	looper := objc.ID(objc.GetClass("AVPlayerLooper")).Send(sel("playerLooperWithPlayer:templateItem:"), player, item)
	looper.Send(sel("retain"))
	layer := objc.ID(objc.GetClass("AVPlayerLayer")).Send(sel("playerLayerWithPlayer:"), player)
	layer.Send(sel("setVideoGravity:"), gravityFill)
	view := win.Send(sel("contentView"))
	view.Send(sel("setWantsLayer:"), true)
	root := view.Send(sel("layer"))
	msgSetFrame(layer, sel("setFrame:"), objc.Send[CGRect](root, sel("bounds")))
	layer.Send(sel("setAutoresizingMask:"), uint(2|16)) // kCALayerWidthSizable | kCALayerHeightSizable
	root.Send(sel("addSublayer:"), layer)
	win.Send(sel("orderFront:"), objc.ID(0))
	player.Send(sel("play"))
	logf("PLAYING %s", *flagVideo)

	mode := nsstr("kCFRunLoopDefaultMode")
	mode.Send(sel("retain"))
	dateCls := objc.ID(objc.GetClass("NSDate"))
	deadline := time.Time{}
	if *flagDuration > 0 {
		deadline = time.Now().Add(*flagDuration)
	}
	nextSample := time.Now()
	var hist [3]bool
	hi, paused, lastOcc, occFlips, samples := 0, false, -1, 0, 0
	pool.Send(sel("drain"))
	for deadline.IsZero() || time.Now().Before(deadline) {
		p := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
		ev := app.Send(sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), ^uint(0),
			dateCls.Send(sel("dateWithTimeIntervalSinceNow:"), 0.05), mode, true)
		if ev != 0 {
			app.Send(sel("sendEvent:"), ev)
		}
		if time.Now().After(nextSample) {
			nextSample = time.Now().Add(*flagSample)
			samples++
			occ := objc.Send[uint](win, sel("occlusionState"))
			vis := 0
			if occ&2 != 0 { // NSWindowOcclusionStateVisible
				vis = 1
			}
			if lastOcc >= 0 && vis != lastOcc {
				occFlips++
			}
			lastOcc = vis
			cov, fs, n, frac, cost := sampleCG(disp)
			hist[hi%3] = cov
			hi++
			agree := hi >= 3 && hist[0] == hist[1] && hist[1] == hist[2]
			logf("SAMPLE occVisible=%d occFlips=%d cgWindows=%d cgCoverFrac=%.3f cgFullscreen=%v cgCovered=%v cgCostUs=%d",
				vis, occFlips, n, frac, fs, cov, cost.Microseconds())
			if agree && hist[0] != paused && !*flagNoPause {
				paused = hist[0]
				if paused {
					player.Send(sel("pause"))
				} else {
					player.Send(sel("play"))
				}
				logf("PAUSE=%v（3 次 CGWindowList 采样一致，Wallnetic 策略）", paused)
			}
		}
		p.Send(sel("drain"))
	}
	logf("SUMMARY samples=%d occFlips=%d", samples, occFlips)
	player.Send(sel("pause"))
	win.Send(sel("orderOut:"), objc.ID(0))
}
