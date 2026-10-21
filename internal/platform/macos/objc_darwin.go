//go:build darwin

// Package macos 是 purego/objc 的共用胶水（desktop/mac 与 render/avf 共用），CGO_ENABLED=0。
// 所有 AppKit/AVFoundation 调用必须经 Main() 切到主线程：UI 模式下 Main = mygo.RunOnMain（cmd 里注入）。
package macos

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type CGPoint struct{ X, Y float64 }
type CGSize struct{ W, H float64 }

// CMTime 对应 CoreMedia 的 CMTime（24 字节：value int64, timescale int32, flags uint32, epoch int64）。
type CMTime struct {
	Value     int64
	Timescale int32
	Flags     uint32 // 1 = kCMTimeFlags_Valid
	Epoch     int64
}

type CGRect struct {
	Origin CGPoint
	Size   CGSize
}

// Main 在主线程同步执行 fn。必须在 Load 之前由调用方设置。
var Main func(fn func())

var (
	Sel = objc.RegisterName

	CGWindowLevelForKey        func(int32) int32
	CGWindowListCopyWindowInfo func(uint32, uint32) uintptr
	CGRectMakeWithDict         func(uintptr, *CGRect) bool
	CGDisplayBounds            func(uint32) CGRect
	CGDisplayCreateUUID        func(uint32) uintptr
	CFUUIDCreateString         func(uintptr, uintptr) uintptr
	CFArrayGetCount            func(uintptr) int
	CFArrayGetValueAtIndex     func(uintptr, int) uintptr
	CFDictionaryGetValue       func(uintptr, uintptr) uintptr
	CFNumberGetValue           func(uintptr, int, unsafe.Pointer) bool
	CFRelease                  func(uintptr)

	MsgInitWindow func(objc.ID, objc.SEL, CGRect, uint, uint, bool, objc.ID) objc.ID
	MsgSetFrame   func(objc.ID, objc.SEL, CGRect)
	MsgSetCMTime  func(objc.ID, objc.SEL, CMTime)

	KLayer, KBounds, KPID uintptr
	GravityFill           objc.ID

	once    sync.Once
	loadErr error
)

func open(p string) uintptr {
	h, err := purego.Dlopen(p, purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil && loadErr == nil {
		loadErr = err
	}
	return h
}

func symPtr(h uintptr, name string) uintptr {
	a, err := purego.Dlsym(h, name)
	if err != nil {
		if loadErr == nil {
			loadErr = err
		}
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&a))
}

// Load 打开框架并注册函数（幂等）。
func Load() error {
	once.Do(func() {
		open("/System/Library/Frameworks/AppKit.framework/AppKit")
		av := open("/System/Library/Frameworks/AVFoundation.framework/AVFoundation")
		open("/System/Library/Frameworks/QuartzCore.framework/QuartzCore")
		open("/System/Library/Frameworks/CoreMedia.framework/CoreMedia")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		lo := open("/usr/lib/libobjc.A.dylib")
		if loadErr != nil {
			return
		}
		purego.RegisterLibFunc(&CGWindowLevelForKey, cg, "CGWindowLevelForKey")
		purego.RegisterLibFunc(&CGWindowListCopyWindowInfo, cg, "CGWindowListCopyWindowInfo")
		purego.RegisterLibFunc(&CGRectMakeWithDict, cg, "CGRectMakeWithDictionaryRepresentation")
		purego.RegisterLibFunc(&CGDisplayBounds, cg, "CGDisplayBounds")
		purego.RegisterLibFunc(&CGDisplayCreateUUID, cg, "CGDisplayCreateUUIDFromDisplayID")
		purego.RegisterLibFunc(&CFUUIDCreateString, cf, "CFUUIDCreateString")
		purego.RegisterLibFunc(&CFArrayGetCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&CFArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&CFDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&CFNumberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&CFRelease, cf, "CFRelease")
		ms, err := purego.Dlsym(lo, "objc_msgSend")
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterFunc(&MsgInitWindow, ms)
		purego.RegisterFunc(&MsgSetFrame, ms)
		purego.RegisterFunc(&MsgSetCMTime, ms)
		KLayer, KBounds, KPID = symPtr(cg, "kCGWindowLayer"), symPtr(cg, "kCGWindowBounds"), symPtr(cg, "kCGWindowOwnerPID")
		GravityFill = objc.ID(symPtr(av, "AVLayerVideoGravityResizeAspectFill"))
	})
	if loadErr == nil && Main == nil {
		return errors.New("macos: Main (RunOnMain) not set")
	}
	return loadErr
}

// NSString 生成 autoreleased NSString。
func NSString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(Sel("stringWithUTF8String:"), s)
}

// GoString 把 NSString/CFString 转成 Go 字符串。
func GoString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := objc.Send[uintptr](s, Sel("UTF8String"))
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); i < 4096; i++ {
		c := *(*byte)(unsafe.Pointer(p + i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

// Class 是 objc.GetClass 的简写。
func Class(n string) objc.ID { return objc.ID(objc.GetClass(n)) }
