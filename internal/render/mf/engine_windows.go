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
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"wpapp/internal/render"
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

type mfEngine struct {
	path        string
	swap        string
	statsEvery  time.Duration
	mu          sync.Mutex
	statsDone   chan struct{}
	running     bool
	stopping    bool
	options     render.PlaybackOptions
	startAt     float64
	commands    chan engineCommand
	events      chan engineEvent
	position    atomic.Uint64
	nativeStats atomic.Value // [3]int64, sampled only by the render thread
	quit        chan struct{}
	done        chan struct{}
	paused      atomic.Bool
	fpsCap      atomic.Int32 // F-05：>0 时按此帧率节流 Transfer+Present（MF 仍解码每一帧，省的是拷贝/呈现/合成）

	// 统计（render 线程写，stats 读）
	presented, appSkipped, loops atomic.Int64
	lastPTS                      atomic.Int64
	restartReq                   atomic.Bool
	tickErr                      atomic.Int64
	endedFallback                atomic.Int64
	readyCh                      chan error
	t0                           time.Time
	// 回调（由 mfhost 子进程设置）
	onFatal func(why string)
	onFirst func(ms int64)
	onStats func(st map[string]any)
}

type engineEvent struct {
	kind uint32
	p1   uintptr
	p2   uint32
}

type engineCommand struct {
	apply  func(*playbackState, playbackAPI) error
	result chan error
}

func (b *mfEngine) onEvent(ev uint32, p1 uintptr, p2 uint32) {
	b.mu.Lock()
	events, active := b.events, b.running && !b.stopping
	b.mu.Unlock()
	if !active {
		return
	}
	switch ev {
	case meEventError, meEventLoadedMetadata, meEventCanPlay, meEventSeeking, meEventSeeked, meEventEnded, meEventFirstFrameReady:
		select {
		case events <- engineEvent{ev, p1, p2}:
		default:
			b.fatal("media-event-queue-overflow")
		}
	}
}

// start 启动渲染线程并等待初始化结果（MF_READY 或失败），最长 10 s。
func (b *mfEngine) start(hwnd uintptr, w, h int32) error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return fmt.Errorf("mf: engine is already running or still stopping")
	}
	b.running, b.stopping = true, false
	b.commands = make(chan engineCommand)
	b.events = make(chan engineEvent, 32)
	b.position.Store(math.Float64bits(b.startAt))
	b.nativeStats.Store([3]int64{-1, -1, -1})
	b.quit, b.done, b.statsDone = make(chan struct{}), make(chan struct{}), make(chan struct{})
	b.readyCh = make(chan error, 1)
	b.t0 = time.Now()
	b.restartReq.Store(false)
	b.presented.Store(0)
	o, startAt, paused := b.options, b.startAt, b.paused.Load()
	quit, statsDone := b.quit, b.statsDone
	statsStart := make(chan struct{})
	b.mu.Unlock()
	// start 拥有统计 worker；quit 关闭后退出，stop 等待 statsDone。
	go func() {
		defer close(statsDone)
		select {
		case <-quit:
			return
		case <-statsStart:
			b.statsLoop(quit)
		}
	}()
	initNotify()
	notifySink.Store(b)
	// start 拥有渲染 worker；初始化失败、运行错误或 quit 后退出，stop 等待 done。
	go b.renderThread(hwnd, uint32(w), uint32(h), o, startAt, paused)
	select {
	case err := <-b.readyCh:
		if err != nil {
			b.stop()
			return err
		}
	case <-time.After(10 * time.Second):
		b.stop()
		return fmt.Errorf("E_TIMEOUT: MF 初始化超过 10 s")
	}
	close(statsStart)
	return nil
}

func (b *mfEngine) stop() error {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return nil
	}
	if !b.stopping {
		b.stopping = true
		close(b.quit)
	}
	done, statsDone := b.done, b.statsDone
	b.mu.Unlock()
	if err := waitForShutdown(done, statsDone, 5*time.Second); err != nil {
		return err
	}
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()
	return nil
}

func (b *mfEngine) invoke(apply func(*playbackState, playbackAPI) error) error {
	b.mu.Lock()
	if !b.running || b.stopping {
		b.mu.Unlock()
		return fmt.Errorf("mf: not running")
	}
	commands, done, quit := b.commands, b.done, b.quit
	b.mu.Unlock()
	command := engineCommand{apply: apply, result: make(chan error, 1)}
	select {
	case commands <- command:
	case <-done:
		return fmt.Errorf("mf: engine closed")
	case <-quit:
		return fmt.Errorf("mf: engine stopping")
	}
	select {
	case err := <-command.result:
		return err
	case <-done:
		return fmt.Errorf("mf: engine closed during control operation")
	}
}

func (b *mfEngine) setPause(paused bool) error {
	return b.invoke(func(state *playbackState, api playbackAPI) error {
		if err := state.setPause(api, paused); err != nil {
			return err
		}
		b.paused.Store(paused)
		return nil
	})
}

func (b *mfEngine) updatePlayback(o render.PlaybackOptions) error {
	return b.invoke(func(state *playbackState, api playbackAPI) error {
		if err := state.update(api, o); err != nil {
			if errors.Is(err, errNativeStateUnknown) {
				b.fatal(err.Error())
			}
			return err
		}
		b.mu.Lock()
		b.options = state.options
		b.mu.Unlock()
		return nil
	})
}

func (b *mfEngine) currentPosition() float64 {
	return math.Float64frombits(b.position.Load())
}

// fatal：引擎无法继续（DEVICE_REMOVED、卡住）。子进程模式下由 mfhost 发 error{fatal} 后退出，交给主进程的 supervisor 重启。
func (b *mfEngine) fatal(why string) {
	if !b.restartReq.CompareAndSwap(false, true) {
		return
	}
	logf("MF_FATAL_RUNTIME reason=%s", why)
	if b.onFatal != nil {
		// 宿主拥有此一次性通知；onFatal 返回或宿主退出时结束。
		go b.onFatal(why)
	}
}

// renderThreadState 只由锁定 OS 线程的 renderThread 及其同步辅助函数访问。
// 将原来的局部变量集中保存，COM/D3D 资源仍按原顺序初始化和释放。
type renderThreadState struct {
	owner *mfEngine
	hwnd  uintptr
	w, h  uint32

	dev, ctx, mt, mgr, attr, fact              uintptr
	engine, engineEx                           uintptr
	dxgiDev, adapter, factory, sc, tex, output uintptr
	comInitialized, mfInitialized              bool

	state                   playbackState
	api                     playbackAPI
	getTime                 func(uintptr) float64
	bufferW, bufferH, black uint32
	lastPTS, minDelta       int64
	lastPresent             time.Time
}

func (b *mfEngine) renderThread(hwnd uintptr, w, h uint32, options render.PlaybackOptions, startAt float64, paused bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	s := &renderThreadState{owner: b, hwnd: hwnd, w: w, h: h}
	defer s.close()
	if !s.initDevice() || !s.initMedia(options) || !s.initSwapchain() || !s.initPlayback(options, startAt, paused) {
		return
	}
	defer s.sample() // Last exact position is preserved through an L3 teardown.
	s.run()
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

func (s *renderThreadState) fail(step string, hr uint32) {
	b := s.owner
	logf("MF_FATAL %s hr=0x%08x", step, hr)
	select {
	case b.readyCh <- fmt.Errorf("E_LOAD: %s hr=0x%08x", step, hr):
	default:
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

func (s *renderThreadState) initMedia(options render.PlaybackOptions) bool {
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
	if !options.Muted {
		muted = 0
	}
	if hr := vcall(s.engine, vtMESetMuted, muted); failed(hr) {
		s.fail("SetMuted", hr)
		return false
	}
	if hr := vcall(s.engine, vtMESetLoop, 1); failed(hr) {
		s.fail("SetLoop", hr)
		return false
	}
	// Never autoplay before the initial rate and asynchronous resume seek finish.
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
	b := s.owner
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
	if b.swap == "blt" {
		d.BufferCount, d.SwapEffect = 1, 0 /*DISCARD*/
	}
	if hr := vcall(s.factory, vtFactCreateSwapChainHwnd, s.dev, s.hwnd, uintptr(unsafe.Pointer(&d)), 0, 0, uintptr(unsafe.Pointer(&s.sc))); failed(hr) {
		s.fail("CreateSwapChainForHwnd(swap="+b.swap+")，可尝试 -swap=blt", hr)
		return false
	}
	vcall(s.factory, vtFactMakeWindowAssoc, s.hwnd, 2 /*NO_ALT_ENTER*/)
	if hr := vcall(s.sc, vtSCGetBuffer, 0, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&s.tex))); failed(hr) {
		s.fail("GetBuffer", hr)
		return false
	}
	if hr := vcall(s.sc, vtSCGetContainingOutput, uintptr(unsafe.Pointer(&s.output))); failed(hr) {
		logf("MF GetContainingOutput 失败 hr=0x%08x，退化为 4 ms 轮询", hr)
		s.output = 0
	}
	return true
}

func (s *renderThreadState) draw(fit string) error {
	b := s.owner
	var aspectW, aspectH uint32
	if err := hresult("GetVideoAspectRatio", vcall(s.engine, vtMEGetVideoAspectRatio, uintptr(unsafe.Pointer(&aspectW)), uintptr(unsafe.Pointer(&aspectH)))); err != nil {
		return err
	}
	geometry, ok := frameGeometry(fit, aspectW, aspectH, s.w, s.h)
	if !ok {
		return fmt.Errorf("mf: invalid %s geometry aspect=%dx%d canvas=%dx%d", fit, aspectW, aspectH, s.w, s.h)
	}
	if geometry.width != s.bufferW || geometry.height != s.bufferH {
		// Release direct and context-held backbuffer references before resize.
		release(&s.tex)
		vcall(s.ctx, vtCtxClearState)
		hr := vcall(s.sc, vtSCResizeBuffers, 0, uintptr(geometry.width), uintptr(geometry.height), 0, 0)
		if !failed(hr) {
			s.bufferW, s.bufferH = geometry.width, geometry.height
		}
		// Reacquire even on failure so a settings rollback can draw the old buffer.
		getErr := hresult("GetBuffer after ResizeBuffers", vcall(s.sc, vtSCGetBuffer, 0, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&s.tex))))
		if err := hresult("ResizeBuffers", hr); err != nil {
			return err
		}
		if getErr != nil {
			return getErr
		}
	}
	if s.tex == 0 {
		return fmt.Errorf("mf: no swapchain backbuffer")
	}
	dst := rect{0, 0, int32(s.bufferW), int32(s.bufferH)}
	if err := hresult("TransferVideoFrame", vcall(s.engine, vtMETransferVideoFrame, s.tex, uintptr(unsafe.Pointer(&geometry.source)), uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&s.black)))); err != nil {
		return err
	}
	hr := vcall(s.sc, vtSCPresent, 0, 0)
	if hr == dxgiErrDeviceRemoved || hr == dxgiErrDeviceReset {
		b.fatal("device-removed")
	}
	if err := hresult("Present", hr); err != nil {
		return err
	}
	if b.presented.Add(1) == 1 && b.onFirst != nil {
		// 宿主拥有此一次性通知；onFirst 返回时结束。
		go b.onFirst(time.Since(b.t0).Milliseconds())
	}
	return nil
}

func (s *renderThreadState) initPlayback(options render.PlaybackOptions, startAt float64, paused bool) bool {
	// Typed bindings are essential: "this" is an integer argument, while the
	// time/rate uses XMM1 on Windows amd64 and D0 on Windows arm64. Do not pass
	// float bits through the integer-only vcall helper.
	var setRate, setTime, setVolume func(uintptr, float64) uint32
	var getDuration func(uintptr) float64
	purego.RegisterFunc(&setRate, vmethod(s.engine, vtMESetPlaybackRate))
	purego.RegisterFunc(&setTime, vmethod(s.engine, vtMESetCurrentTime))
	purego.RegisterFunc(&setVolume, vmethod(s.engine, vtMESetVolume))
	if hr := setVolume(s.engine, float64(options.Volume)/100); failed(hr) {
		s.fail("SetVolume", hr)
		return false
	}
	purego.RegisterFunc(&s.getTime, vmethod(s.engine, vtMEGetCurrentTime))
	purego.RegisterFunc(&getDuration, vmethod(s.engine, vtMEGetDuration))
	var supportsRate func(uintptr, float64) int32
	if s.engineEx != 0 {
		purego.RegisterFunc(&supportsRate, vmethod(s.engineEx, vtMEExIsPlaybackRateSupported))
	}

	s.state = playbackState{options: options, paused: paused, start: startAt}
	s.bufferW, s.bufferH = s.w, s.h
	s.black = uint32(0xff000000)
	s.api = playbackAPI{
		rate: func(rate float64) error {
			if supportsRate != nil && supportsRate(s.engineEx, rate) == 0 {
				return fmt.Errorf("mf: this media does not support playback rate %g", rate)
			}
			return hresult("SetPlaybackRate", setRate(s.engine, rate))
		},
		mute: func(muted bool) error {
			value := uintptr(0)
			if muted {
				value = 1
			}
			return hresult("SetMuted", vcall(s.engine, vtMESetMuted, value))
		},
		volume:   func(v float64) error { return hresult("SetVolume", setVolume(s.engine, v)) },
		fit:      s.draw,
		seek:     func(seconds float64) error { return hresult("SetCurrentTime", setTime(s.engine, seconds)) },
		play:     func() error { return hresult("Play", vcall(s.engine, vtMEPlay)) },
		pause:    func() error { return hresult("Pause", vcall(s.engine, vtMEPause)) },
		duration: func() float64 { return getDuration(s.engine) },
	}
	return true
}

func (s *renderThreadState) sample() {
	b := s.owner
	if s.state.metadata && !s.state.seeking {
		if seconds := s.getTime(s.engine); validPosition(seconds) {
			b.position.Store(math.Float64bits(seconds))
		}
	}
	b.nativeStats.Store([3]int64{nativeStat(s.engineEx, meStatFramesRendered), nativeStat(s.engineEx, meStatFramesDropped), nativeStat(s.engineEx, meStatFramesCorrupted)})
}

func (s *renderThreadState) failPlayback(err error) {
	b := s.owner
	logf("MF playback error: %v", err)
	if s.state.ready {
		b.fatal(err.Error())
	} else {
		select {
		case b.readyCh <- err:
		default:
		}
	}
}

func (s *renderThreadState) handleEvent(event engineEvent) error {
	b := s.owner
	switch event.kind {
	case meEventError:
		return fmt.Errorf("mf: media error code=%d hr=0x%08x", event.p1, event.p2)
	case meEventLoadedMetadata, meEventCanPlay:
		if event.kind == meEventCanPlay {
			s.state.canPlay = true
		}
		if err := s.state.loaded(s.api); err != nil {
			return err
		}
		if s.state.seeking {
			b.position.Store(math.Float64bits(s.state.start))
		}
	case meEventSeeking:
		s.state.seeking = true
	case meEventSeeked:
		if err := s.state.seeked(s.api); err != nil {
			return err
		}
	case meEventEnded:
		b.endedFallback.Add(1)
		if s.state.ready && !s.state.paused {
			if err := s.api.play(); err != nil {
				return err
			}
		}
	}
	ready, err := s.state.startReady(s.api)
	if err != nil {
		return err
	}
	if ready {
		s.sample()
		logf("MF_READY hwnd=0x%x %dx%d swap=%s engineEx=%v fit=%s speed=%g", s.hwnd, s.w, s.h, b.swap, s.engineEx != 0, s.state.options.Fit, s.state.options.Speed)
		b.readyCh <- nil
	}
	return nil
}

func (s *renderThreadState) run() {
	b := s.owner
	progress := time.NewTicker(500 * time.Millisecond)
	defer progress.Stop()
	s.lastPTS, s.minDelta = -1, 0
	for {
		// Idle players still service commands/events and sample position.
		// Pending startup seeks service frames even with paused intent.
		if !s.state.shouldServiceFrames() {
			select {
			case <-b.quit:
				return
			case command := <-b.commands:
				command.result <- command.apply(&s.state, s.api)
			case event := <-b.events:
				if err := s.handleEvent(event); err != nil {
					s.failPlayback(err)
					return
				}
			case <-progress.C:
				s.sample()
			}
			continue
		}
		select {
		case <-b.quit:
			return
		case command := <-b.commands:
			command.result <- command.apply(&s.state, s.api)
			continue
		case event := <-b.events:
			if err := s.handleEvent(event); err != nil {
				s.failPlayback(err)
				return
			}
			continue
		case <-progress.C:
			s.sample()
		default:
		}
		if err := s.presentFrame(); err != nil {
			s.failPlayback(err)
			return
		}
	}
}

func (s *renderThreadState) presentFrame() error {
	b := s.owner
	if s.output != 0 {
		vcall(s.output, vtOutWaitForVBlank)
	} else {
		time.Sleep(4 * time.Millisecond)
	}
	var pts int64
	if hr := vcall(s.engine, vtMEOnVideoStreamTick, uintptr(unsafe.Pointer(&pts))); hr != 0 {
		if failed(hr) && b.tickErr.Add(1) == 1 {
			logf("MF OnVideoStreamTick first error hr=0x%08x", hr)
		}
		return nil
	}
	if s.lastPTS >= 0 {
		delta := pts - s.lastPTS
		switch {
		case delta < 0:
			b.loops.Add(1)
		case delta > 0 && (s.minDelta == 0 || delta < s.minDelta):
			s.minDelta = delta
		case s.minDelta > 0 && delta > s.minDelta*3/2:
			b.appSkipped.Add(delta/s.minDelta - 1)
		}
	}
	s.lastPTS = pts
	b.lastPTS.Store(pts)
	if cap := b.fpsCap.Load(); s.state.ready && cap > 0 && time.Since(s.lastPresent) < time.Second/time.Duration(cap)-2*time.Millisecond {
		return nil
	}
	if err := s.draw(s.state.options.Fit); err != nil {
		return err
	}
	s.lastPresent = time.Now()
	return nil
}

func nativeStat(ex uintptr, id uint32) int64 {
	if ex == 0 {
		return -1
	}
	var pv [24]byte // PROPVARIANT
	if failed(vcall(ex, vtMEExGetStatistics, uintptr(id), uintptr(unsafe.Pointer(&pv[0])))) {
		return -1
	}
	vt := *(*uint16)(unsafe.Pointer(&pv[0]))
	switch vt {
	case 3, 19: // VT_I4, VT_UI4
		return int64(*(*uint32)(unsafe.Pointer(&pv[8])))
	case 20, 21: // VT_I8, VT_UI8
		return *(*int64)(unsafe.Pointer(&pv[8]))
	}
	return -2 // 类型不认识，真机日志里会体现
}

// 与 mpv 后端同口径：每 -stats 记录一次；卡住判定 = (loops, pts) 连续 2 次不变且未暂停（Q-P3 的同一规则）
func (b *mfEngine) statsLoop(quit <-chan struct{}) {
	t := time.NewTicker(b.statsEvery)
	defer t.Stop()
	type key struct{ loops, pts int64 }
	var last key
	same := 0
	for {
		select {
		case <-quit:
			return
		case <-t.C:
		}
		k := key{b.loops.Load(), b.lastPTS.Load()}
		native := b.nativeStats.Load().([3]int64)
		st := map[string]any{"rendered": native[0], "dropped": native[1],
			"corrupted": native[2], "pos": b.currentPosition(), "presented": b.presented.Load(), "appSkipped": b.appSkipped.Load(),
			"loops": k.loops, "endedFallback": b.endedFallback.Load(), "pts100ns": k.pts, "paused": b.paused.Load()}
		if b.onStats != nil {
			b.onStats(st)
		}
		logf("MFSTATS rendered=%d dropped=%d corrupted=%d presented=%d appSkipped=%d loops=%d endedFallback=%d pts100ns=%d paused=%v",
			native[0], native[1], native[2],
			b.presented.Load(), b.appSkipped.Load(), k.loops, b.endedFallback.Load(), k.pts, b.paused.Load())
		if !b.paused.Load() && k == last {
			if same++; same >= 2 {
				logf("STALL mf 进度 (loops=%d,pts=%d) 连续 %d 次未变", k.loops, k.pts, same)
				b.fatal("stall")
				return
			}
		} else {
			same = 0
		}
		last = k
	}
}

// Logf 是本包的日志函数（由宿主进程设置）。
var Logf = func(format string, a ...any) {}

func logf(format string, a ...any) { Logf(format, a...) }

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func up(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

type rect struct{ L, T, R, B int32 }
