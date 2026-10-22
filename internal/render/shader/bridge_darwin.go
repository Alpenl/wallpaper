//go:build darwin

package shader

import (
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"sync"
)

// Integer-only messages avoid objc.Send's reflect.MakeFunc path in the frame
// loop. Selectors and classes are resolved once, before rendering begins.
var bridge struct {
	once      sync.Once
	fn        uintptr
	selectors map[string]uintptr
	classes   map[string]uintptr
	err       error
	arguments [10]uintptr
}

func initBridge() error {
	bridge.once.Do(func() {
		lib, e := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
		if e != nil {
			bridge.err = e
			return
		}
		if _, e := purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_NOW|purego.RTLD_GLOBAL); e != nil {
			bridge.err = e
			return
		}
		bridge.fn, bridge.err = purego.Dlsym(lib, "objc_msgSend")
		bridge.selectors = map[string]uintptr{}
		for _, name := range []string{"new", "drain", "date", "currentRunLoop", "runMode:beforeDate:", "nextDrawable", "commandBuffer", "renderPassDescriptor", "colorAttachments", "objectAtIndexedSubscript:", "setTexture:", "setLoadAction:", "setStoreAction:", "renderCommandEncoderWithDescriptor:", "setRenderPipelineState:", "setFragmentBytes:length:atIndex:", "drawPrimitives:vertexStart:vertexCount:", "endEncoding", "texture", "setFragmentTexture:atIndex:", "presentDrawable:", "commit", "waitUntilCompleted", "status", "valueForKey:", "getValue:size:", "retain", "release"} {
			bridge.selectors[name] = uintptr(sel(name))
		}
		bridge.classes = map[string]uintptr{}
		for _, name := range []string{"NSAutoreleasePool", "NSDate", "NSRunLoop", "MTLRenderPassDescriptor"} {
			bridge.classes[name] = uintptr(objc.GetClass(name))
		}
	})
	return bridge.err
}
func message(id objc.ID, name string, args ...uintptr) objc.ID {
	values := &bridge.arguments
	values[0] = uintptr(id)
	values[1] = bridge.selectors[name]
	copy(values[2:], args)
	r, _, _ := purego.SyscallN(bridge.fn, values[:len(args)+2]...)
	return objc.ID(r)
}
func hotClass(name string) objc.ID { return objc.ID(bridge.classes[name]) }
