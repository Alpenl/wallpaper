//go:build windows

package mf

// Media Foundation 后端。纯 Go、无 cgo：整数 COM 调用使用 syscall.SyscallN，
// double 参数/返回值通过 purego.RegisterFunc 按 Windows amd64/arm64 ABI 绑定。
// 路线：IMFMediaEngine 帧服务器模式（不给 PLAYBACK_HWND，而是 MF_MEDIA_ENGINE_DXGI_MANAGER）→
//       每个 vblank 调 OnVideoStreamTick，有新帧时 TransferVideoFrame 到我们自建子 HWND 的 DXGI 交换链后台缓冲 → Present。
// 这与 mpv 的 --wid 模式在结构上对等：都是“解码器(GPU) → 一次 GPU 拷贝/缩放 → 交换链”。
// vtable 下标与 GUID 均核对自 mingw-w64 12.0 头文件（mfmediaengine.h / mfobjects.h / dxgi*.h / d3d10.h）。
// 所有 Media Engine 调用、实时设置和释放均串行执行于 render 线程；MF 回调仅投递事件。

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"wpapp/internal/platform"
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

var (
	clsidMFMediaEngineClassFactory = guid{0xb44392da, 0x499b, 0x446b, [8]byte{0xa4, 0xcb, 0x00, 0x5f, 0xea, 0xd0, 0xe6, 0xd5}}
	iidIMFMediaEngineClassFactory  = guid{0x4d645ace, 0x26aa, 0x4688, [8]byte{0x9b, 0xe1, 0xdf, 0x35, 0x16, 0x99, 0x0b, 0x93}}
	iidIMFMediaEngineNotify        = guid{0xfee7c112, 0xe776, 0x42b5, [8]byte{0x9b, 0xbf, 0x00, 0x48, 0x52, 0x4e, 0x2b, 0xd5}}
	iidIMFMediaEngineEx            = guid{0x83015ead, 0xb1e6, 0x40d0, [8]byte{0xa9, 0x8a, 0x37, 0x14, 0x5f, 0xfe, 0x1a, 0xd1}}
	iidIUnknown                    = guid{0x00000000, 0x0000, 0x0000, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	mfMediaEngineCallback          = guid{0xc60381b8, 0x83a4, 0x41f8, [8]byte{0xa3, 0xd0, 0xde, 0x05, 0x07, 0x68, 0x49, 0xa9}}
	mfMediaEngineDXGIManager       = guid{0x065702da, 0x1094, 0x486d, [8]byte{0x86, 0x17, 0xee, 0x7c, 0xc4, 0xee, 0x46, 0x48}}
	mfMediaEngineVideoOutputFormat = guid{0x5066893c, 0x8cf9, 0x42bc, [8]byte{0x8b, 0x8a, 0x47, 0x22, 0x12, 0xe5, 0x27, 0x26}}
	iidID3D10Multithread           = guid{0x9b7e4e00, 0x342c, 0x4106, [8]byte{0xa1, 0x9f, 0x4f, 0x27, 0x04, 0xf6, 0x89, 0xf0}}
	iidIDXGIDevice                 = guid{0x54ec77fa, 0x1377, 0x44e6, [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	iidIDXGIFactory2               = guid{0x50c83a1c, 0xe072, 0x4c48, [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
	iidID3D11Texture2D             = guid{0x6f15aaf2, 0xd208, 0x4e89, [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
)

// vtable 下标（含 IUnknown 的 0..2）
const (
	vtQueryInterface = 0
	vtRelease        = 2
	// IMFAttributes
	vtAttrSetUINT32  = 21
	vtAttrSetUnknown = 27
	// IMFDXGIDeviceManager
	vtMgrResetDevice = 7
	// IMFMediaEngineClassFactory
	vtFactCreateInstance = 3
	// IMFMediaEngine
	vtMESetSource           = 6
	vtMELoad                = 12
	vtMEGetCurrentTime      = 16
	vtMESetCurrentTime      = 17
	vtMEGetDuration         = 19
	vtMESetPlaybackRate     = 24
	vtMESetAutoPlay         = 29
	vtMESetLoop             = 31
	vtMEPlay                = 32
	vtMEPause               = 33
	vtMESetMuted            = 35
	vtMESetVolume           = 37
	vtMEGetNativeVideoSize  = 40
	vtMEGetVideoAspectRatio = 41
	vtMEShutdown            = 42
	vtMETransferVideoFrame  = 43
	vtMEOnVideoStreamTick   = 44
	// IMFMediaEngineEx
	vtMEExGetStatistics           = 46
	vtMEExIsPlaybackRateSupported = 50
	// ID3D10Multithread
	vtMTSetProtected = 5
	// IDXGIDevice / IDXGIObject / IDXGIFactory2 / IDXGISwapChain1 / IDXGIOutput
	vtDevGetAdapter           = 7
	vtObjGetParent            = 6
	vtFactMakeWindowAssoc     = 8
	vtFactCreateSwapChainHwnd = 15
	vtSCPresent               = 8
	vtSCGetBuffer             = 9
	vtSCResizeBuffers         = 13
	vtCtxClearState           = 110
	vtSCGetContainingOutput   = 15
	vtOutWaitForVBlank        = 10
)

const (
	meEventError           = 5
	meEventLoadedMetadata  = 10
	meEventPlaying         = 13
	meEventCanPlay         = 14
	meEventSeeking         = 16
	meEventSeeked          = 17
	meEventEnded           = 19
	meEventFirstFrameReady = 1009
	meStatFramesRendered   = 0
	meStatFramesDropped    = 1
	meStatFramesCorrupted  = 6
	dxgiFormatB8G8R8A8     = 87
	dxgiErrDeviceRemoved   = 0x887A0005
	dxgiErrDeviceReset     = 0x887A0007
)

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	oleaut32 = syscall.NewLazyDLL("oleaut32.dll")
	mfplat   = syscall.NewLazyDLL("mfplat.dll")
	d3d11    = syscall.NewLazyDLL("d3d11.dll")

	pCoInitializeEx            = ole32.NewProc("CoInitializeEx")
	pCoUninitialize            = ole32.NewProc("CoUninitialize")
	pCoCreateInstance          = ole32.NewProc("CoCreateInstance")
	pSysAllocString            = oleaut32.NewProc("SysAllocString")
	pSysFreeString             = oleaut32.NewProc("SysFreeString")
	pMFStartup                 = mfplat.NewProc("MFStartup")
	pMFShutdown                = mfplat.NewProc("MFShutdown")
	pMFCreateAttributes        = mfplat.NewProc("MFCreateAttributes")
	pMFCreateDXGIDeviceManager = mfplat.NewProc("MFCreateDXGIDeviceManager")
	pD3D11CreateDevice         = d3d11.NewProc("D3D11CreateDevice")
)

// go:uintptrescapes：与 syscall.(*Proc).Call 相同，保证以 uintptr 传入的 Go 指针所指对象在堆上、调用期间不移动。
//
//go:uintptrescapes
func vcall(obj uintptr, idx int, args ...uintptr) uint32 {
	fn := vmethod(obj, idx)
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return uint32(r)
}
func vmethod(obj uintptr, idx int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

func hresult(method string, hr uint32) error {
	if failed(hr) {
		return fmt.Errorf("mf: %s hr=0x%08x", method, hr)
	}
	return nil
}

func release(obj *uintptr) {
	if *obj != 0 {
		vcall(*obj, vtRelease)
		*obj = 0
	}
}
func failed(hr uint32) bool { return int32(hr) < 0 }

// ---- IMFMediaEngineNotify 的 Go 实现（静态对象，生命周期 = 进程）----
type notifyObj struct{ vtbl *[4]uintptr }

var (
	notifyVtbl [4]uintptr
	notifyInst notifyObj
	notifyOnce sync.Once
	notifySink atomic.Pointer[mfEngine]
)

func initNotify() {
	notifyOnce.Do(func() {
		notifyVtbl[0] = syscall.NewCallback(func(this, riid, ppv uintptr) uintptr {
			g := (*guid)(unsafe.Pointer(riid))
			if *g == iidIUnknown || *g == iidIMFMediaEngineNotify {
				*(*uintptr)(unsafe.Pointer(ppv)) = this
				return 0
			}
			*(*uintptr)(unsafe.Pointer(ppv)) = 0
			return 0x80004002 // E_NOINTERFACE
		})
		notifyVtbl[1] = syscall.NewCallback(func(this uintptr) uintptr { return 2 }) // 静态对象，引用计数无意义
		notifyVtbl[2] = syscall.NewCallback(func(this uintptr) uintptr { return 1 })
		notifyVtbl[3] = syscall.NewCallback(func(this, ev, p1, p2 uintptr) uintptr {
			if b := notifySink.Load(); b != nil {
				b.onEvent(uint32(ev), p1, uint32(p2))
			}
			return 0
		})
		notifyInst.vtbl = &notifyVtbl
	})
}

// mfEngine owns the locked render thread. No COM pointer crosses that thread;
// callbacks only enqueue events, Close signals quit and joins done.
type mfEngine struct {
	path     string
	quit     chan struct{}
	done     chan struct{}
	events   chan engineEvent
	once     sync.Once
	mu       sync.Mutex
	evidence platform.PlayerEvidence
	failure  error
}
type engineEvent struct {
	kind uint32
	p1   uintptr
	p2   uint32
}
type renderThreadState struct {
	owner                                      *mfEngine
	hwnd                                       uintptr
	w, h                                       uint32
	dev, ctx, mt, mgr, attr, fact              uintptr
	engine, engineEx                           uintptr
	dxgiDev, adapter, factory, sc, tex, output uintptr
	comInitialized, mfInitialized              bool
	getTime                                    func(uintptr) float64
	lastPTS                                    int64
	frames, loops                              uint64
}

func newEngine(path string, hwnd uintptr, w, h uint32) *mfEngine {
	b := &mfEngine{path: path, quit: make(chan struct{}), done: make(chan struct{}), events: make(chan engineEvent, 32), evidence: platform.PlayerEvidence{Ready: platform.Unknown}}
	go b.run(hwnd, w, h) // engine owns this thread until Close joins done.
	return b
}
func (b *mfEngine) onEvent(kind uint32, p1 uintptr, p2 uint32) {
	if kind != meEventError && kind != meEventCanPlay {
		return
	}
	select {
	case b.events <- engineEvent{kind, p1, p2}:
	default:
	}
}
func (b *mfEngine) fail(err error) {
	b.mu.Lock()
	b.failure = err
	b.evidence.Ready = platform.No
	b.mu.Unlock()
}
func (s *renderThreadState) fail(step string, hr uint32) {
	s.owner.fail(fmt.Errorf("mf: %s hr=0x%08x", step, hr))
}
func (b *mfEngine) run(hwnd uintptr, w, h uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	initNotify()
	notifySink.Store(b)
	s := &renderThreadState{owner: b, hwnd: hwnd, w: w, h: h, lastPTS: -1}
	defer s.close()
	if !s.initDevice() || !s.initMedia() || !s.initSwapchain() {
		return
	}
	purego.RegisterFunc(&s.getTime, vmethod(s.engine, vtMEGetCurrentTime))
	canPlay := false
	for {
		select {
		case <-b.quit:
			return
		case ev := <-b.events:
			if ev.kind == meEventError {
				b.fail(fmt.Errorf("mf: media error code=%d hr=0x%08x", ev.p1, ev.p2))
				return
			}
			if !canPlay {
				if err := hresult("Play", vcall(s.engine, vtMEPlay)); err != nil {
					b.fail(err)
					return
				}
				canPlay = true
			}
		default:
		}
		if !canPlay {
			select {
			case <-b.quit:
				return
			case ev := <-b.events:
				if ev.kind == meEventError {
					b.fail(fmt.Errorf("mf: media error code=%d hr=0x%08x", ev.p1, ev.p2))
					return
				}
				if err := hresult("Play", vcall(s.engine, vtMEPlay)); err != nil {
					b.fail(err)
					return
				}
				canPlay = true
			}
			continue
		}

		if s.output != 0 {
			vcall(s.output, vtOutWaitForVBlank)
		} else {
			// Bounded frame-server polling when DXGI has no output.
			timer := time.NewTimer(4 * time.Millisecond)
			select {
			case <-b.quit:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if err := s.present(); err != nil {
			b.fail(err)
			return
		}
	}
}
func (s *renderThreadState) present() error {
	var pts int64
	hr := vcall(s.engine, vtMEOnVideoStreamTick, uintptr(unsafe.Pointer(&pts)))
	if hr == 1 {
		return nil
	} // S_FALSE: no new frame.
	if err := hresult("OnVideoStreamTick", hr); err != nil {
		return err
	}
	var aw, ah uint32
	if err := hresult("GetVideoAspectRatio", vcall(s.engine, vtMEGetVideoAspectRatio, uintptr(unsafe.Pointer(&aw)), uintptr(unsafe.Pointer(&ah)))); err != nil {
		return err
	}
	crop, ok := fillSourceRect(aw, ah, s.w, s.h)
	if !ok {
		return fmt.Errorf("mf: invalid video aspect %dx%d", aw, ah)
	}
	dst := rect{0, 0, int32(s.w), int32(s.h)}
	black := uint32(0xff000000)
	if err := hresult("TransferVideoFrame", vcall(s.engine, vtMETransferVideoFrame, s.tex, uintptr(unsafe.Pointer(&crop)), uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&black)))); err != nil {
		return err
	}
	hr = vcall(s.sc, vtSCPresent, 0, 0)
	if failed(hr) {
		return hresult("Present", hr)
	}
	if hr != 0 {
		// DXGI_STATUS_OCCLUDED is expected while the surface is initially hidden.
		// A transferred frame can be revealed, but it is NOT a presented frame and
		// cannot establish progress. Once shown, successful Present must advance.
		if hr == 0x087a0001 {
			s.owner.mu.Lock()
			s.owner.evidence.Ready = platform.Yes
			s.owner.mu.Unlock()
		}
		return nil
	}
	s.frames++
	if s.lastPTS >= 0 && pts < s.lastPTS {
		s.loops++
	}
	s.lastPTS = pts
	pos := s.getTime(s.engine)
	frames, loops := s.frames, s.loops
	e := platform.PlayerEvidence{Ready: platform.Yes, Frames: &frames, Loops: &loops}
	if !math.IsNaN(pos) && !math.IsInf(pos, 0) && pos >= 0 {
		e.Position = &pos
	}
	s.owner.mu.Lock()
	s.owner.evidence = e
	s.owner.mu.Unlock()
	return nil
}
func (b *mfEngine) sample() (platform.PlayerEvidence, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.evidence, b.failure
}
func (b *mfEngine) close() error {
	b.once.Do(func() { close(b.quit) })
	select {
	case <-b.done:
		return nil
	case <-time.After(1500 * time.Millisecond):
		return fmt.Errorf("mf: render shutdown timed out")
	}
}
func (s *renderThreadState) close() {
	b := s.owner
	notifySink.CompareAndSwap(b, nil)
	if s.engine != 0 {
		vcall(s.engine, vtMEShutdown)
	}
	for _, p := range []*uintptr{&s.output, &s.tex, &s.sc, &s.factory, &s.adapter, &s.dxgiDev, &s.engineEx, &s.engine, &s.fact, &s.attr, &s.mgr, &s.mt, &s.ctx, &s.dev} {
		release(p)
	}
	if s.mfInitialized {
		pMFShutdown.Call()
	}
	if s.comInitialized {
		pCoUninitialize.Call()
	}
}

func (s *renderThreadState) initDevice() bool {
	if hr, _, _ := pCoInitializeEx.Call(0, 0 /*COINIT_MULTITHREADED*/); failed(uint32(hr)) {
		s.fail("CoInitializeEx", uint32(hr))
		return false
	}
	s.comInitialized = true
	if r, _, _ := pMFStartup.Call(0x00020070, 0); failed(uint32(r)) {
		s.fail("MFStartup", uint32(r))
		return false
	}
	s.mfInitialized = true
	// D3D11 设备：BGRA_SUPPORT(0x20) | VIDEO_SUPPORT(0x800)
	r, _, _ := pD3D11CreateDevice.Call(0, 1 /*HARDWARE*/, 0, 0x20|0x800, 0, 0, 7,
		uintptr(unsafe.Pointer(&s.dev)), 0, uintptr(unsafe.Pointer(&s.ctx)))
	if failed(uint32(r)) {
		s.fail("D3D11CreateDevice", uint32(r))
		return false
	}
	// Media Engine 会在自己的线程用同一设备，必须开多线程保护
	if hr := vcall(s.dev, vtQueryInterface, uintptr(unsafe.Pointer(&iidID3D10Multithread)), uintptr(unsafe.Pointer(&s.mt))); !failed(hr) {
		vcall(s.mt, vtMTSetProtected, 1)
	}
	var token uint32
	if r, _, _ := pMFCreateDXGIDeviceManager.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&s.mgr))); failed(uint32(r)) {
		s.fail("MFCreateDXGIDeviceManager", uint32(r))
		return false
	}
	if hr := vcall(s.mgr, vtMgrResetDevice, s.dev, uintptr(token)); failed(hr) {
		s.fail("ResetDevice", hr)
		return false
	}
	return true
}

func (s *renderThreadState) initMedia() bool {
	b := s.owner
	if hr, _, _ := pMFCreateAttributes.Call(uintptr(unsafe.Pointer(&s.attr)), 3); failed(uint32(hr)) {
		s.fail("MFCreateAttributes", uint32(hr))
		return false
	}
	if hr := vcall(s.attr, vtAttrSetUnknown, uintptr(unsafe.Pointer(&mfMediaEngineDXGIManager)), s.mgr); failed(hr) {
		s.fail("Set DXGI manager", hr)
		return false
	}
	if hr := vcall(s.attr, vtAttrSetUnknown, uintptr(unsafe.Pointer(&mfMediaEngineCallback)), uintptr(unsafe.Pointer(&notifyInst))); failed(hr) {
		s.fail("Set notify callback", hr)
		return false
	}
	if hr := vcall(s.attr, vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfMediaEngineVideoOutputFormat)), dxgiFormatB8G8R8A8); failed(hr) {
		s.fail("Set video output format", hr)
		return false
	}
	if r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMFMediaEngineClassFactory)), 0, 1, /*INPROC_SERVER*/
		uintptr(unsafe.Pointer(&iidIMFMediaEngineClassFactory)), uintptr(unsafe.Pointer(&s.fact))); failed(uint32(r)) {
		s.fail("CoCreateInstance(MFMediaEngineClassFactory)", uint32(r))
		return false
	}
	if hr := vcall(s.fact, vtFactCreateInstance, 0, s.attr, uintptr(unsafe.Pointer(&s.engine))); failed(hr) {
		s.fail("CreateInstance", hr)
		return false
	}
	vcall(s.engine, vtQueryInterface, uintptr(unsafe.Pointer(&iidIMFMediaEngineEx)), uintptr(unsafe.Pointer(&s.engineEx)))
	muted := uintptr(1)
	if hr := vcall(s.engine, vtMESetMuted, muted); failed(hr) {
		s.fail("SetMuted", hr)
		return false
	}
	if hr := vcall(s.engine, vtMESetLoop, 1); failed(hr) {
		s.fail("SetLoop", hr)
		return false
	}
	// M1 always starts at zero, muted, looping.
	if hr := vcall(s.engine, vtMESetAutoPlay, 0); failed(hr) {
		s.fail("SetAutoPlay", hr)
		return false
	}
	abs, _ := filepath.Abs(b.path)
	bstr, _, _ := pSysAllocString.Call(up(u16(abs)))
	if bstr == 0 {
		s.fail("SysAllocString", 0x8007000e)
		return false
	}
	hr := vcall(s.engine, vtMESetSource, bstr)
	pSysFreeString.Call(bstr)
	if failed(hr) {
		s.fail("SetSource", hr)
		return false
	}
	if hr := vcall(s.engine, vtMELoad); failed(hr) {
		s.fail("Load", hr)
		return false
	}
	return true
}

func (s *renderThreadState) initSwapchain() bool {
	// 交换链：从设备反查 IDXGIFactory2
	if hr := vcall(s.dev, vtQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIDevice)), uintptr(unsafe.Pointer(&s.dxgiDev))); failed(hr) {
		s.fail("Query IDXGIDevice", hr)
		return false
	}
	if hr := vcall(s.dxgiDev, vtDevGetAdapter, uintptr(unsafe.Pointer(&s.adapter))); failed(hr) {
		s.fail("GetAdapter", hr)
		return false
	}
	if hr := vcall(s.adapter, vtObjGetParent, uintptr(unsafe.Pointer(&iidIDXGIFactory2)), uintptr(unsafe.Pointer(&s.factory))); failed(hr) {
		s.fail("GetParent(IDXGIFactory2)", hr)
		return false
	}
	type scDesc1 struct {
		Width, Height, Format, Stereo, SampleCount, SampleQuality, BufferUsage, BufferCount, Scaling, SwapEffect, AlphaMode, Flags uint32
	}
	d := scDesc1{Width: s.w, Height: s.h, Format: dxgiFormatB8G8R8A8, SampleCount: 1, BufferUsage: 0x20 /*RENDER_TARGET_OUTPUT*/, BufferCount: 2, SwapEffect: 4 /*FLIP_DISCARD*/}
	if hr := vcall(s.factory, vtFactCreateSwapChainHwnd, s.dev, s.hwnd, uintptr(unsafe.Pointer(&d)), 0, 0, uintptr(unsafe.Pointer(&s.sc))); failed(hr) {
		s.fail("CreateSwapChainForHwnd", hr)
		return false
	}
	vcall(s.factory, vtFactMakeWindowAssoc, s.hwnd, 2 /*NO_ALT_ENTER*/)
	if hr := vcall(s.sc, vtSCGetBuffer, 0, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&s.tex))); failed(hr) {
		s.fail("GetBuffer", hr)
		return false
	}
	if hr := vcall(s.sc, vtSCGetContainingOutput, uintptr(unsafe.Pointer(&s.output))); failed(hr) {
		logf("MF GetContainingOutput failed hr=0x%08x; polling every 4 ms", hr)
		s.output = 0
	}
	return true
}

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
func u16(s string) *uint16         { p, _ := syscall.UTF16PtrFromString(s); return p }
func up(p *uint16) uintptr         { return uintptr(unsafe.Pointer(p)) }

type rect struct{ L, T, R, B int32 }
