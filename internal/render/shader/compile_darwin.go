//go:build darwin

package shader

import (
	"fmt"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"sync"
	"unsafe"
)

var metalOnce sync.Once
var metalErr error
var createDevice func() objc.ID
var sel = objc.RegisterName

func ns(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), s)
}
func metalDevice() (objc.ID, error) {
	metalOnce.Do(func() {
		var lib uintptr
		lib, metalErr = purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if metalErr == nil {
			purego.RegisterLibFunc(&createDevice, lib, "MTLCreateSystemDefaultDevice")
		}
	})
	if metalErr != nil {
		return 0, metalErr
	}
	d := createDevice()
	if d == 0 {
		return 0, fmt.Errorf("shader: no Metal device available")
	}
	return d, nil
}
func compileMetal(device objc.ID, source string) (objc.ID, error) {
	var failure objc.ID
	lib := device.Send(sel("newLibraryWithSource:options:error:"), ns(source), objc.ID(0), unsafe.Pointer(&failure))
	if lib == 0 {
		message := "Metal compilation failed"
		if failure != 0 {
			str := failure.Send(sel("localizedDescription"))
			ptr := str.Send(sel("UTF8String"))
			if ptr != 0 {
				b := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 16384)
				for i, c := range b {
					if c == 0 {
						message = string(b[:i])
						break
					}
				}
			}
		}
		return 0, fmt.Errorf("shader: %s", message)
	}
	return lib, nil
}
