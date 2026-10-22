//go:build darwin

package shader

import (
	"fmt"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"runtime"
	"unsafe"
	"wpapp/internal/desktop/mac"
	"wpapp/internal/platform/macos"
)

type metalGPU struct {
	timingKeys                                                                   [2]objc.ID
	times                                                                        [2]float64
	blitSize                                                                     [2]float32
	runMode                                                                      objc.ID
	device, queue, library, pipeline, blitPipeline, layer, window, view, texture objc.ID
	w, h, tw, th                                                                 int
}

func cls(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
func runHost(p pipeIO) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, name := range []string{"AppKit", "QuartzCore"} {
		if _, e := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL); e != nil {
			return e
		}
	}
	lib, e := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	if e != nil {
		return e
	}
	purego.RegisterLibFunc(&macos.MsgInitWindow, lib, "objc_msgSend")
	cg, e := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
	if e != nil {
		return e
	}
	purego.RegisterLibFunc(&macos.CGWindowLevelForKey, cg, "CGWindowLevelForKey")
	pool := cls("NSAutoreleasePool").Send(sel("new"))
	defer pool.Send(sel("drain"))
	app := cls("NSApplication").Send(sel("sharedApplication"))
	app.Send(sel("setActivationPolicy:"), 2)
	app.Send(sel("finishLaunching"))
	return serve(p, &metalGPU{})
}
func (g *metalGPU) Pump() {
	if bridge.fn == 0 {
		return
	}
	pool := message(hotClass("NSAutoreleasePool"), "new")
	defer message(pool, "drain")
	date := message(hotClass("NSDate"), "date")
	loop := message(hotClass("NSRunLoop"), "currentRunLoop")
	message(loop, "runMode:beforeDate:", uintptr(g.runMode), uintptr(date))
}
func (g *metalGPU) makePipeline(vertex, fragment string) (objc.ID, error) {
	v := g.library.Send(sel("newFunctionWithName:"), ns(vertex))
	defer v.Send(sel("release"))
	f := g.library.Send(sel("newFunctionWithName:"), ns(fragment))
	defer f.Send(sel("release"))
	d := cls("MTLRenderPipelineDescriptor").Send(sel("new"))
	defer d.Send(sel("release"))
	d.Send(sel("setVertexFunction:"), v)
	d.Send(sel("setFragmentFunction:"), f)
	d.Send(sel("colorAttachments")).Send(sel("objectAtIndexedSubscript:"), 0).Send(sel("setPixelFormat:"), uintptr(80))
	var failure objc.ID
	p := g.device.Send(sel("newRenderPipelineStateWithDescriptor:error:"), d, unsafe.Pointer(&failure))
	if p == 0 {
		return 0, fmt.Errorf("shader: Metal render pipeline creation failed")
	}
	return p, nil
}
func (g *metalGPU) Load(x loadRequest, source string) error {
	var e error
	g.device, e = metalDevice()
	if e != nil {
		return e
	}
	g.library, e = compileMetal(g.device, source+metalBlit)
	if e != nil {
		return e
	}
	g.pipeline, e = g.makePipeline("wp_vertex", "wp_fragment")
	if e != nil {
		return e
	}
	g.blitPipeline, e = g.makePipeline("wp_vertex", "wp_blit")
	if e != nil {
		return e
	}
	if e := initBridge(); e != nil {
		return e
	}
	g.runMode = ns("NSDefaultRunLoopMode").Send(sel("retain"))
	g.timingKeys = [2]objc.ID{ns("GPUStartTime").Send(sel("retain")), ns("GPUEndTime").Send(sel("retain"))}
	g.queue = g.device.Send(sel("newCommandQueue"))
	if g.queue == 0 {
		return fmt.Errorf("shader: Metal command queue unavailable")
	}
	g.w = x.Bounds.Dx()
	g.h = x.Bounds.Dy()
	if x.Preview {
		return nil
	}
	g.window, g.view, e = mac.RendererWindow(x.Bounds)
	if e != nil {
		return e
	}
	g.view.Send(sel("setWantsLayer:"), true)
	g.layer = cls("CAMetalLayer").Send(sel("new"))
	g.layer.Send(sel("setDevice:"), g.device)
	g.layer.Send(sel("setPixelFormat:"), uintptr(80))
	g.layer.Send(sel("setFramebufferOnly:"), true)
	g.layer.Send(sel("setDisplaySyncEnabled:"), true)
	backing := objc.Send[float64](g.window, sel("backingScaleFactor"))
	if backing < 1 {
		backing = 1
	}
	g.w = int(float64(x.Bounds.Dx()) * backing)
	g.h = int(float64(x.Bounds.Dy()) * backing)
	g.layer.Send(sel("setContentsScale:"), backing)
	g.layer.Send(sel("setDrawableSize:"), macos.CGSize{W: float64(g.w), H: float64(g.h)})
	g.view.Send(sel("setLayer:"), g.layer)
	g.window.Send(sel("orderFrontRegardless"))
	return nil
}
func (g *metalGPU) target(w, h int) error {
	if w == g.tw && h == g.th {
		return nil
	}
	if g.texture != 0 {
		g.texture.Send(sel("release"))
		g.texture = 0
	}
	d := cls("MTLTextureDescriptor").Send(sel("texture2DDescriptorWithPixelFormat:width:height:mipmapped:"), uintptr(80), uintptr(w), uintptr(h), false)
	d.Send(sel("setUsage:"), uintptr(5))
	storage := uintptr(2)
	if g.window == 0 {
		storage = 0
	}
	d.Send(sel("setStorageMode:"), storage)
	g.texture = g.device.Send(sel("newTextureWithDescriptor:"), d)
	if g.texture == 0 {
		return fmt.Errorf("shader: offscreen texture allocation failed")
	}
	g.tw = w
	g.th = h
	return nil
}
func encoder(command, texture, pipeline objc.ID) objc.ID {
	pass := message(hotClass("MTLRenderPassDescriptor"), "renderPassDescriptor")
	color := message(message(pass, "colorAttachments"), "objectAtIndexedSubscript:", 0)
	message(color, "setTexture:", uintptr(texture))
	message(color, "setLoadAction:", 0)
	message(color, "setStoreAction:", 1)
	enc := message(command, "renderCommandEncoderWithDescriptor:", uintptr(pass))
	message(enc, "setRenderPipelineState:", uintptr(pipeline))
	return enc
}
func (g *metalGPU) Frame(u *Uniforms, scale float64) (float64, error) {
	pool := message(hotClass("NSAutoreleasePool"), "new")
	defer message(pool, "drain")
	if e := g.target(max(1, int(float64(g.w)*scale)), max(1, int(float64(g.h)*scale))); e != nil {
		return 0, e
	}
	var drawable objc.ID
	if g.layer != 0 {
		drawable = message(g.layer, "nextDrawable")
		if drawable == 0 {
			return 0, fmt.Errorf("shader: Metal drawable unavailable")
		}
	}
	cmd := message(g.queue, "commandBuffer")
	if cmd == 0 {
		return 0, fmt.Errorf("shader: command buffer unavailable")
	}
	enc := encoder(cmd, g.texture, g.pipeline)
	if enc == 0 {
		return 0, fmt.Errorf("shader: command encoder unavailable")
	}
	message(enc, "setFragmentBytes:length:atIndex:", uintptr(unsafe.Pointer(&u.Bytes[0])), uintptr(u.Size), 0)
	message(enc, "drawPrimitives:vertexStart:vertexCount:", 3, 0, 3)
	message(enc, "endEncoding")
	if drawable != 0 {
		enc = encoder(cmd, message(drawable, "texture"), g.blitPipeline)
		if enc == 0 {
			return 0, fmt.Errorf("shader: upscale encoder unavailable")
		}
		message(enc, "setFragmentTexture:atIndex:", uintptr(g.texture), 0)
		g.blitSize = [2]float32{float32(g.w), float32(g.h)}
		message(enc, "setFragmentBytes:length:atIndex:", uintptr(unsafe.Pointer(&g.blitSize[0])), 8, 0)
		message(enc, "drawPrimitives:vertexStart:vertexCount:", 3, 0, 3)
		message(enc, "endEncoding")
		message(cmd, "presentDrawable:", uintptr(drawable))
	}
	message(cmd, "commit")
	message(cmd, "waitUntilCompleted")
	if message(cmd, "status") == 5 {
		return 0, fmt.Errorf("shader: Metal command buffer failed")
	}
	// KVC boxes these two double properties as NSNumber/NSValue. Copying their
	// bytes avoids building floating-point reflection trampolines every frame.
	for i, key := range g.timingKeys {
		value := message(cmd, "valueForKey:", uintptr(key))
		if value == 0 {
			return 0, nil
		}
		message(value, "getValue:size:", uintptr(unsafe.Pointer(&g.times[i])), 8)
	}
	return (g.times[1] - g.times[0]) * 1000, nil
}
func (g *metalGPU) Close() {
	if g.window != 0 {
		g.window.Send(sel("close"))
	}
	for _, id := range []objc.ID{g.texture, g.pipeline, g.blitPipeline, g.library, g.queue, g.layer, g.window, g.device, g.runMode, g.timingKeys[0], g.timingKeys[1]} {
		if id != 0 {
			id.Send(sel("release"))
		}
	}
}

const metalBlit = `
fragment float4 wp_blit(float4 pos [[position]], texture2d<float> source [[texture(0)]], constant float2 &size [[buffer(0)]]) {
 constexpr sampler linearSampler(coord::normalized, address::clamp_to_edge, filter::linear);
 return source.sample(linearSampler, pos.xy/size);
}
`

func nativeTarget() Target { return MSL }
func platformPreview(x loadRequest, dst string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := initBridge(); e != nil {
		return e
	}
	pool := message(hotClass("NSAutoreleasePool"), "new")
	defer message(pool, "drain")
	return renderPreview(&metalGPU{}, x, dst)
}
func (g *metalGPU) Snapshot(dst string) error {
	if g.window != 0 {
		return fmt.Errorf("shader: readback requires offscreen preview")
	}
	pixels := make([]byte, g.tw*g.th*4)
	region := struct{ X, Y, Z, W, H, D uintptr }{W: uintptr(g.tw), H: uintptr(g.th), D: 1}
	g.texture.Send(sel("getBytes:bytesPerRow:fromRegion:mipmapLevel:"), unsafe.Pointer(&pixels[0]), uintptr(g.tw*4), region, uintptr(0))
	return writePreview(dst, pixels, g.tw, g.th, g.tw*4, true)
}

func (g *metalGPU) RasterSize() (int, int) { return g.w, g.h }
