//go:build darwin

package nativeav

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}
type cmTimeRange struct{ Start, Duration cmTime }
type cgSize struct{ Width, Height float64 }
type cgAffineTransform struct{ A, B, C, D, TX, TY float64 }
type cgPoint struct{ X, Y float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

var (
	sel                               = objc.RegisterName
	nativeOnce                        sync.Once
	nativeErr                         error
	constants                         = map[string]objc.ID{}
	cfRelease                         func(uintptr)
	cgImageGetWidth, cgImageGetHeight func(uintptr) uintptr
	cgImageRelease                    func(uintptr)
	cgColorSpaceCreateDeviceRGB       func() uintptr
	cgColorSpaceRelease               func(uintptr)
	cgBitmapContextCreate             func(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uint32) uintptr
	cgContextRelease                  func(uintptr)
	cgContextDrawImage                func(uintptr, cgRect, uintptr)
	cgContextSetInterpolationQuality  func(uintptr, int32)
	cvPoolCreatePixelBuffer           func(uintptr, uintptr, uintptr, *uintptr) int32
	cvPixelBufferLockBaseAddress      func(uintptr, uint64) int32
	cvPixelBufferUnlockBaseAddress    func(uintptr, uint64) int32
	cvPixelBufferGetBaseAddress       func(uintptr) uintptr
	cvPixelBufferGetBytesPerRow       func(uintptr) uintptr
	cvPixelBufferGetHeight            func(uintptr) uintptr
	cmTimeAdd                         func(cmTime, cmTime) cmTime
	msgSetTime                        func(objc.ID, objc.SEL, cmTime)
	msgSetSize                        func(objc.ID, objc.SEL, cgSize)
	msgCopyImage                      func(objc.ID, objc.SEL, cmTime, *cmTime, *objc.ID) uintptr
	msgAppendPixelBuffer              func(objc.ID, objc.SEL, uintptr, cmTime) bool
)

func loadNative() error {
	nativeOnce.Do(func() {
		// RegisterLibFunc panics for missing symbols. Report unavailable rather than
		// crashing the UI's capability check on an unsupported system installation.
		defer func() {
			if p := recover(); p != nil {
				nativeErr = fmt.Errorf("nativeav: framework registration: %v", p)
			}
		}()
		open := func(name string) uintptr {
			h, e := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			if e != nil {
				panic(e)
			}
			return h
		}
		open("Foundation")
		av := open("AVFoundation")
		cm := open("CoreMedia")
		cv := open("CoreVideo")
		cg := open("CoreGraphics")
		cf := open("CoreFoundation")
		for _, entry := range []struct {
			handle uintptr
			names  []string
		}{
			{av, []string{"AVFileTypeMPEG4", "AVMediaTypeVideo", "AVVideoCodecKey", "AVVideoCodecTypeH264", "AVVideoWidthKey", "AVVideoHeightKey", "AVVideoCompressionPropertiesKey", "AVVideoAverageBitRateKey", "AVVideoExpectedSourceFrameRateKey", "AVVideoMaxKeyFrameIntervalKey", "AVVideoMaxKeyFrameIntervalDurationKey", "AVVideoAllowFrameReorderingKey", "AVVideoProfileLevelKey", "AVVideoProfileLevelH264HighAutoLevel"}},
			{cv, []string{"kCVPixelBufferPixelFormatTypeKey", "kCVPixelBufferWidthKey", "kCVPixelBufferHeightKey", "kCVPixelBufferCGImageCompatibilityKey", "kCVPixelBufferCGBitmapContextCompatibilityKey", "kCVPixelBufferIOSurfacePropertiesKey", "kCVPixelBufferPoolAllocationThresholdKey"}},
		} {
			for _, name := range entry.names {
				p, e := purego.Dlsym(entry.handle, name)
				if e != nil {
					panic(e)
				}
				v := *(*objc.ID)(unsafe.Pointer(p))
				if v == 0 {
					panic("nil framework constant " + name)
				}
				constants[name] = v
			}
		}
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cgImageGetWidth, cg, "CGImageGetWidth")
		purego.RegisterLibFunc(&cgImageGetHeight, cg, "CGImageGetHeight")
		purego.RegisterLibFunc(&cgImageRelease, cg, "CGImageRelease")
		purego.RegisterLibFunc(&cgColorSpaceCreateDeviceRGB, cg, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&cgColorSpaceRelease, cg, "CGColorSpaceRelease")
		purego.RegisterLibFunc(&cgBitmapContextCreate, cg, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&cgContextRelease, cg, "CGContextRelease")
		purego.RegisterLibFunc(&cgContextDrawImage, cg, "CGContextDrawImage")
		purego.RegisterLibFunc(&cgContextSetInterpolationQuality, cg, "CGContextSetInterpolationQuality")
		purego.RegisterLibFunc(&cvPoolCreatePixelBuffer, cv, "CVPixelBufferPoolCreatePixelBufferWithAuxAttributes")
		purego.RegisterLibFunc(&cvPixelBufferLockBaseAddress, cv, "CVPixelBufferLockBaseAddress")
		purego.RegisterLibFunc(&cvPixelBufferUnlockBaseAddress, cv, "CVPixelBufferUnlockBaseAddress")
		purego.RegisterLibFunc(&cvPixelBufferGetBaseAddress, cv, "CVPixelBufferGetBaseAddress")
		purego.RegisterLibFunc(&cvPixelBufferGetBytesPerRow, cv, "CVPixelBufferGetBytesPerRow")
		purego.RegisterLibFunc(&cvPixelBufferGetHeight, cv, "CVPixelBufferGetHeight")
		purego.RegisterLibFunc(&cmTimeAdd, cm, "CMTimeAdd")
		lo, e := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if e != nil {
			panic(e)
		}
		msg, e := purego.Dlsym(lo, "objc_msgSend")
		if e != nil {
			panic(e)
		}
		purego.RegisterFunc(&msgSetTime, msg)
		purego.RegisterFunc(&msgSetSize, msg)
		purego.RegisterFunc(&msgCopyImage, msg)
		purego.RegisterFunc(&msgAppendPixelBuffer, msg)
		for _, name := range []string{"NSAutoreleasePool", "NSURL", "AVURLAsset", "AVAssetImageGenerator", "AVAssetWriter", "AVAssetWriterInput", "AVAssetWriterInputPixelBufferAdaptor"} {
			if objc.GetClass(name) == 0 {
				panic("missing Objective-C class " + name)
			}
		}
	})
	return nativeErr
}

// Available tests framework/class availability only. Codec availability and
// support for a particular source are checked when Encode actually runs.
func Available() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return loadNative() == nil
}
func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
func ns(s string) objc.ID       { return class("NSString").Send(sel("stringWithUTF8String:"), s) }
func number(n int64) objc.ID    { return class("NSNumber").Send(sel("numberWithLongLong:"), n) }
func boolean(v bool) objc.ID    { return class("NSNumber").Send(sel("numberWithBool:"), v) }
func dict(pairs ...objc.ID) objc.ID {
	d := class("NSMutableDictionary").Send(sel("dictionary"))
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Send(sel("setObject:forKey:"), pairs[i+1], pairs[i])
	}
	return d
}
func textOf(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := objc.Send[uintptr](s, sel("UTF8String"))
	if p == 0 {
		return ""
	}
	b := make([]byte, 0, 256)
	for i := uintptr(0); i < 4096; i++ {
		v := *(*byte)(unsafe.Pointer(p + i))
		if v == 0 {
			break
		}
		b = append(b, v)
	}
	return string(b)
}
func nativeError(prefix string, e objc.ID) error {
	if e == 0 {
		return fmt.Errorf("nativeav: %s", prefix)
	}
	return fmt.Errorf("nativeav: %s: %s", prefix, textOf(e.Send(sel("localizedDescription"))))
}
func frameTime(n int) cmTime { return cmTime{Value: int64(n), Timescale: FPS, Flags: 1} }
func validTime(t cmTime) bool {
	return t.Flags&1 != 0 && t.Flags&(4|8|16) == 0 && t.Timescale > 0 && t.Epoch == 0
}
