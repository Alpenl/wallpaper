//go:build windows

package shader

import (
	"fmt"
	"github.com/ebitengine/purego"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var iidFactory = guid{0x50c83a1c, 0xe072, 0x4c48, [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
var iidTexture = guid{0x6f15aaf2, 0xd208, 0x4e89, [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
var iidSwap2 = guid{0xa8be2ac4, 0x199f, 0x4946, [8]byte{0xb3, 0x31, 0x79, 0x59, 0x9f, 0xb9, 0x8d, 0xe7}}

// The renderer owns one native event thread per child process.
var comArguments [16]uintptr

func com(p uintptr, index int, args ...uintptr) uintptr {
	v := *(*uintptr)(unsafe.Pointer(p))
	fn := *(*uintptr)(unsafe.Pointer(v + uintptr(index)*unsafe.Sizeof(p)))
	call := &comArguments
	call[0] = p
	copy(call[1:], args)
	r, _, _ := syscall.SyscallN(fn, call[:len(args)+1]...)
	return r
}

// Native calls are serialized on the child event thread. Retain pointer
// arguments through the call, and force local descriptors onto stable Go heap
// storage before converting them to uintptr. No interface payload allocation is
// needed for pointer values. The ring exceeds the maximum native argument count.
var nativePins [32]any
var nextPin uint

func ptr[T any](p *T) uintptr {
	nativePins[nextPin%32] = p
	nextPin++
	return uintptr(unsafe.Pointer(p))
}
func release(p *uintptr) {
	if *p != 0 {
		com(*p, 2)
		*p = 0
	}
}
func hr(name string, r uintptr) error {
	if int32(r) < 0 {
		return fmt.Errorf("shader: %s failed (HRESULT %#x)", name, uint32(r))
	}
	return nil
}

type swapDesc struct{ Width, Height, Format, Stereo, SampleCount, SampleQuality, Usage, Buffers, Scaling, Effect, Alpha, Flags uint32 }
type textureDesc struct{ Width, Height, Mips, Array, Format, SampleCount, SampleQuality, Usage, Bind, CPU, Misc uint32 }
type bufferDesc struct{ Bytes, Usage, Bind, CPU, Misc, Stride uint32 }
type viewport struct{ X, Y, W, H, Min, Max float32 }
type querySet struct {
	disjoint, start, end uintptr
	pending              bool
}
type windowMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type d3dGPU struct {
	message          windowMessage
	peekArgs         [5]uintptr
	dispatchArgs     [1]uintptr
	viewport         viewport
	drawTarget, zero uintptr
	waitArgs         [2]uintptr
	timing           struct {
		Frequency         uint64
		Disjoint, Padding uint32
	}
	ticks [2]uint64

	device, context, swap, swap2, wait, window, vs, ps, blit, buffer, sampler, back, texture, target, srv uintptr
	w, h, tw, th                                                                                          int
	queries                                                                                               [3]querySet
	queryIndex                                                                                            int
	blitUniforms                                                                                          Uniforms
}

var waitForFrame = syscall.NewLazyDLL("kernel32.dll").NewProc("WaitForSingleObject")
var user32 = syscall.NewLazyDLL("user32.dll")

func runHost(p pipeIO) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return serve(p, &d3dGPU{})
}

var peekMessage = user32.NewProc("PeekMessageW")
var translateMessage = user32.NewProc("TranslateMessage")
var dispatchMessage = user32.NewProc("DispatchMessageW")

func (g *d3dGPU) Pump() {
	if g.window == 0 {
		return
	}
	for i := 0; i < 64; i++ {
		r, _, _ := peekMessage.Call(g.peekArgs[:]...)
		if r == 0 {
			return
		}
		translateMessage.Call(g.dispatchArgs[:]...)
		dispatchMessage.Call(g.dispatchArgs[:]...)
	}
}

var compilerOnce sync.Once
var compilerDLL *syscall.DLL // Keep the code behind returned ID3DBlob vtables loaded.
var compilerError error
var compileD3D func(unsafe.Pointer, uintptr, string, uintptr, uintptr, string, string, uint32, uint32, *uintptr, *uintptr) int32

func compileHLSL(source, entry, profile string) (uintptr, error) {
	compilerOnce.Do(func() {
		compilerDLL, compilerError = syscall.LoadDLL("d3dcompiler_47.dll")
		if compilerError != nil {
			return
		}
		var proc *syscall.Proc
		proc, compilerError = compilerDLL.FindProc("D3DCompile")
		if compilerError == nil {
			purego.RegisterFunc(&compileD3D, proc.Addr())
		}
	})
	if compilerError != nil {
		return 0, compilerError
	}
	b := []byte(source)
	var blob, errors uintptr
	r := compileD3D(unsafe.Pointer(&b[0]), uintptr(len(b)), "wallpaper.glsl", 0, 0, entry, profile, 1<<15, 0, &blob, &errors)
	defer release(&errors)
	if r < 0 {
		message := "HLSL compile failed"
		if errors != 0 {
			p := com(errors, 3)
			n := com(errors, 4)
			if n > 0 && n < 1<<20 {
				message = string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
			}
		}
		release(&blob)
		return 0, fmt.Errorf("shader: %s", message)
	}
	return blob, nil
}
func (g *d3dGPU) shader(source, entry, profile string, index int, out *uintptr) error {
	b, e := compileHLSL(source, entry, profile)
	if e != nil {
		return e
	}
	defer release(&b)
	return hr("CreateShader", com(g.device, index, com(b, 3), com(b, 4), 0, ptr(out)))
}
func (g *d3dGPU) Load(x loadRequest, source string) error {
	if x.Native == 0 && !x.Preview {
		return fmt.Errorf("shader: missing desktop HWND")
	}
	g.w = x.Bounds.Dx()
	g.h = x.Bounds.Dy()
	g.peekArgs = [5]uintptr{ptr(&g.message), 0, 0, 0, 1}
	g.dispatchArgs = [1]uintptr{ptr(&g.message)}
	if !x.Preview {
		class, _ := syscall.UTF16PtrFromString("STATIC")
		create := user32.NewProc("CreateWindowExW")
		g.window, _, _ = create.Call(0, ptr(class), 0, 0x50000000, 0, 0, uintptr(g.w), uintptr(g.h), x.Native, 0, 0, 0)
		if g.window == 0 {
			return fmt.Errorf("shader: child HWND creation failed")
		}
	}
	var feature uint32
	r, _, _ := syscall.NewLazyDLL("d3d11.dll").NewProc("D3D11CreateDevice").Call(0, 1, 0, 0x20, 0, 0, 7, ptr(&g.device), ptr(&feature), ptr(&g.context))
	if e := hr("D3D11CreateDevice", r); e != nil {
		return e
	}
	if feature < 0xb000 {
		return fmt.Errorf("shader: shader model 5.0 is required")
	}
	if !x.Preview {
		var factory uintptr
		r, _, _ = syscall.NewLazyDLL("dxgi.dll").NewProc("CreateDXGIFactory1").Call(ptr(&iidFactory), ptr(&factory))
		if e := hr("CreateDXGIFactory1", r); e != nil {
			return e
		}
		defer release(&factory)
		desc := swapDesc{Width: uint32(g.w), Height: uint32(g.h), Format: 28, SampleCount: 1, Usage: 32, Buffers: 2, Effect: 4, Flags: 64}
		if e := hr("CreateSwapChainForHwnd", com(factory, 15, g.device, g.window, ptr(&desc), 0, 0, ptr(&g.swap))); e != nil {
			return e
		}
		if e := hr("IDXGISwapChain2", com(g.swap, 0, ptr(&iidSwap2), ptr(&g.swap2))); e != nil {
			return e
		}
		if e := hr("SetMaximumFrameLatency", com(g.swap2, 31, 1)); e != nil {
			return e
		}
		g.wait = com(g.swap2, 33)
		g.waitArgs = [2]uintptr{g.wait, 1000}
		if g.wait == 0 {
			return fmt.Errorf("shader: missing frame latency wait handle")
		}
		var back uintptr
		if e := hr("GetBuffer", com(g.swap, 9, 0, ptr(&iidTexture), ptr(&back))); e != nil {
			return e
		}
		defer release(&back)
		if e := hr("CreateRenderTargetView", com(g.device, 9, back, 0, ptr(&g.back))); e != nil {
			return e
		}
	}
	if e := g.shader(source, "wp_vertex", "vs_5_0", 12, &g.vs); e != nil {
		return e
	}
	if e := g.shader(source, "wp_fragment", "ps_5_0", 15, &g.ps); e != nil {
		return e
	}
	if e := g.shader(hlslBlit, "wp_blit", "ps_5_0", 15, &g.blit); e != nil {
		return e
	}
	bd := bufferDesc{Bytes: MaxUniformBytes, Bind: 4}
	if e := hr("CreateBuffer", com(g.device, 3, ptr(&bd), 0, ptr(&g.buffer))); e != nil {
		return e
	}
	// D3D11_SAMPLER_DESC: MIN_MAG_MIP_LINEAR, CLAMP on all axes.
	sd := struct {
		Filter, U, V, W        uint32
		Bias                   float32
		Anisotropy, Comparison uint32
		Border                 [4]float32
		Min, Max               float32
	}{Filter: 21, U: 3, V: 3, W: 3, Anisotropy: 1, Comparison: 1, Max: 3.402823466e38}
	if e := hr("CreateSamplerState", com(g.device, 23, ptr(&sd), ptr(&g.sampler))); e != nil {
		return e
	}
	for i := range g.queries {
		q := &g.queries[i]
		for _, v := range []struct {
			kind uint32
			out  *uintptr
		}{{3, &q.disjoint}, {2, &q.start}, {2, &q.end}} {
			d := [2]uint32{v.kind, 0}
			if e := hr("CreateQuery", com(g.device, 24, ptr(&d), ptr(v.out))); e != nil {
				return e
			}
		}
	}
	return nil
}
func (g *d3dGPU) offscreen(w, h int) error {
	if w == g.tw && h == g.th {
		return nil
	}
	release(&g.srv)
	release(&g.target)
	release(&g.texture)
	d := textureDesc{Width: uint32(w), Height: uint32(h), Mips: 1, Array: 1, Format: 28, SampleCount: 1, Bind: 40}
	if e := hr("CreateTexture2D", com(g.device, 5, ptr(&d), 0, ptr(&g.texture))); e != nil {
		return e
	}
	if e := hr("CreateRenderTargetView", com(g.device, 9, g.texture, 0, ptr(&g.target))); e != nil {
		return e
	}
	if e := hr("CreateShaderResourceView", com(g.device, 7, g.texture, 0, ptr(&g.srv))); e != nil {
		return e
	}
	g.tw = w
	g.th = h
	return nil
}
func (g *d3dGPU) draw(target, ps uintptr, w, h int, u *Uniforms) {
	g.viewport = viewport{W: float32(w), H: float32(h), Max: 1}
	g.drawTarget = target
	com(g.context, 44, 1, ptr(&g.viewport))
	com(g.context, 33, 1, ptr(&g.drawTarget), 0)
	com(g.context, 48, g.buffer, 0, 0, ptr(&u.Bytes[0]), 0, 0)
	com(g.context, 16, 0, 1, ptr(&g.buffer))
	com(g.context, 11, g.vs, 0, 0)
	com(g.context, 9, ps, 0, 0)
	com(g.context, 24, 4)
	com(g.context, 13, 3, 0)
}
func (g *d3dGPU) Frame(u *Uniforms, scale float64) (float64, error) {
	if g.swap != 0 {
		wait, _, _ := waitForFrame.Call(g.waitArgs[:]...)
		if wait != 0 {
			return 0, fmt.Errorf("shader: swapchain wait failed (%d)", wait)
		}
	}
	if e := g.offscreen(max(1, int(float64(g.w)*scale)), max(1, int(float64(g.h)*scale))); e != nil {
		return 0, e
	}
	q := &g.queries[g.queryIndex]
	ms := 0.0
	if q.pending {
		if com(g.context, 29, q.disjoint, ptr(&g.timing), 16, 1) == 0 && com(g.context, 29, q.start, ptr(&g.ticks[0]), 8, 1) == 0 && com(g.context, 29, q.end, ptr(&g.ticks[1]), 8, 1) == 0 {
			if g.timing.Disjoint == 0 && g.timing.Frequency > 0 && g.ticks[1] >= g.ticks[0] {
				ms = float64(g.ticks[1]-g.ticks[0]) * 1000 / float64(g.timing.Frequency)
			}
			q.pending = false
		}
	}
	measure := !q.pending
	if measure {
		com(g.context, 27, q.disjoint)
		com(g.context, 28, q.start)
	}
	g.draw(g.target, g.ps, g.tw, g.th, u)
	if g.swap != 0 {
		com(g.context, 33, 0, 0, 0)
		com(g.context, 8, 0, 1, ptr(&g.srv))
		com(g.context, 10, 0, 1, ptr(&g.sampler))
		g.blitUniforms.put(0, 0, float32(g.w))
		g.blitUniforms.put(0, 1, float32(g.h))
		g.draw(g.back, g.blit, g.w, g.h, &g.blitUniforms)
		com(g.context, 8, 0, 1, ptr(&g.zero))
	}
	if measure {
		com(g.context, 28, q.end)
		com(g.context, 28, q.disjoint)
		q.pending = true
	}
	g.queryIndex = (g.queryIndex + 1) % len(g.queries)
	if g.swap == 0 {
		com(g.context, 111)
		return ms, nil
	}
	return ms, hr("Present", com(g.swap, 8, 1, 0))
}
func (g *d3dGPU) Close() {
	if g.context != 0 {
		com(g.context, 110)
		com(g.context, 111)
	}
	for i := range g.queries {
		release(&g.queries[i].disjoint)
		release(&g.queries[i].start)
		release(&g.queries[i].end)
	}
	for _, p := range []*uintptr{&g.srv, &g.target, &g.texture, &g.back, &g.sampler, &g.buffer, &g.vs, &g.ps, &g.blit, &g.swap2, &g.swap, &g.context, &g.device} {
		release(p)
	}
	if g.wait != 0 {
		syscall.CloseHandle(syscall.Handle(g.wait))
		g.wait = 0
	}
	if g.window != 0 {
		user32.NewProc("DestroyWindow").Call(g.window)
		g.window = 0
	}
}

const hlslBlit = `cbuffer Size : register(b0) { float4 size; }; Texture2D source : register(t0); SamplerState linearSampler : register(s0);
float4 wp_blit(float4 pos : SV_Position) : SV_Target { return source.Sample(linearSampler, pos.xy/size.xy); }
`

func nativeTarget() Target { return HLSL }
func platformPreview(x loadRequest, dst string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return renderPreview(&d3dGPU{}, x, dst)
}
func (g *d3dGPU) Snapshot(dst string) error {
	d := textureDesc{Width: uint32(g.tw), Height: uint32(g.th), Mips: 1, Array: 1, Format: 28, SampleCount: 1, Usage: 3, CPU: 0x20000}
	var staging uintptr
	if e := hr("Create staging texture", com(g.device, 5, ptr(&d), 0, ptr(&staging))); e != nil {
		return e
	}
	defer release(&staging)
	com(g.context, 47, staging, g.texture)
	var mapped struct {
		Data       uintptr
		Row, Depth uint32
	}
	if e := hr("Map preview", com(g.context, 14, staging, 0, 1, 0, ptr(&mapped))); e != nil {
		return e
	}
	defer com(g.context, 15, staging, 0)
	return writePreview(dst, unsafe.Slice((*byte)(unsafe.Pointer(mapped.Data)), int(mapped.Row)*g.th), g.tw, g.th, int(mapped.Row), false)
}
