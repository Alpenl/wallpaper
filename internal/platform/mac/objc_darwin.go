//go:build darwin

// Package mac owns AppKit objects. Every native call runs synchronously through
// Main, installed by the composition root as mygo.RunOnMain before App.Run.
package mac

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type CGPoint struct{ X, Y float64 }
type CGSize struct{ W, H float64 }
type CGRect struct {
	Origin CGPoint
	Size   CGSize
}
type CMTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

var Main func(func())
var Sel = objc.RegisterName
var (
	MsgInitWindow                  func(objc.ID, objc.SEL, CGRect, uint, uint, bool, objc.ID) objc.ID
	MsgSetFrame                    func(objc.ID, objc.SEL, CGRect)
	windowLevel                    func(int32) int32
	windowInfo                     func(uint32, uint32) objc.ID
	windowIsOnscreen, windowNumber objc.ID
	GravityFill                    objc.ID
	once                           sync.Once
	loadErr                        error
)

func Load() error {
	once.Do(func() {
		handles := map[string]uintptr{}
		for _, name := range []string{"AppKit", "AVFoundation", "QuartzCore", "CoreMedia", "CoreGraphics"} {
			h, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			if err != nil {
				loadErr = err
				return
			}
			handles[name] = h
		}
		h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			loadErr = err
			return
		}
		msg, err := purego.Dlsym(h, "objc_msgSend")
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterFunc(&MsgInitWindow, msg)
		purego.RegisterFunc(&MsgSetFrame, msg)
		purego.RegisterLibFunc(&windowLevel, handles["CoreGraphics"], "CGWindowLevelForKey")
		purego.RegisterLibFunc(&windowInfo, handles["CoreGraphics"], "CGWindowListCopyWindowInfo")
		for name, target := range map[string]*objc.ID{
			"kCGWindowIsOnscreen": &windowIsOnscreen,
			"kCGWindowNumber":     &windowNumber,
		} {
			symbol, err := purego.Dlsym(handles["CoreGraphics"], name)
			if err != nil {
				loadErr = err
				return
			}
			*target = *(*objc.ID)(unsafe.Pointer(symbol))
		}

		symbol, err := purego.Dlsym(handles["AVFoundation"], "AVLayerVideoGravityResizeAspectFill")
		if err != nil {
			loadErr = err
			return
		}
		GravityFill = *(*objc.ID)(unsafe.Pointer(symbol))
	})
	if loadErr == nil && Main == nil {
		return errors.New("mac: Main is not installed")
	}
	return loadErr
}
func Class(n string) objc.ID    { return objc.ID(objc.GetClass(n)) }
func NSString(s string) objc.ID { return Class("NSString").Send(Sel("stringWithUTF8String:"), s) }
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

// Pool bounds autoreleased objects on the long-running AppKit thread.
func Pool(fn func()) {
	p := Class("NSAutoreleasePool").Send(Sel("new"))
	defer p.Send(Sel("drain"))
	fn()
}
func WithoutActions(fn func()) {
	t := Class("CATransaction")
	t.Send(Sel("begin"))
	t.Send(Sel("setDisableActions:"), true)
	defer t.Send(Sel("commit"))
	fn()
}
