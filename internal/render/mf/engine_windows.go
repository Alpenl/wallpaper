//go:build windows

package mf

// Media Foundation 后端（Q-W10 的 A/B 候选）。纯 Go、无 cgo：COM 调用全部通过 vtable 下标 + syscall.SyscallN。
// 路线：IMFMediaEngine 帧服务器模式（不给 PLAYBACK_HWND，而是 MF_MEDIA_ENGINE_DXGI_MANAGER）→
//       每个 vblank 调 OnVideoStreamTick，有新帧时 TransferVideoFrame 到我们自建子 HWND 的 DXGI 交换链后台缓冲 → Present。
// 这与 mpv 的 --wid 模式在结构上对等：都是“解码器(GPU) → 一次 GPU 拷贝/缩放 → 交换链”。
// vtable 下标与 GUID 均核对自 mingw-w64 12.0 头文件（mfmediaengine.h / mfobjects.h / dxgi*.h / d3d10.h）。
// 已知简化：不调用带 double 参数/返回值的方法（GetCurrentTime、SetCurrentTime），因为 syscall 无法正确传递浮点寄存器；
//           时间一律用 OnVideoStreamTick 返回的 100ns 时间戳。重挂后从头播放（mpv 后端同样如此）。

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
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
	vtMESetSource          = 6
	vtMELoad               = 12
	vtMESetAutoPlay        = 29
	vtMESetLoop            = 31
	vtMEPlay               = 32
	vtMEPause              = 33
	vtMESetMuted           = 35
	vtMEGetNativeVideoSize = 40
	vtMEShutdown           = 42
	vtMETransferVideoFrame = 43
	vtMEOnVideoStreamTick  = 44
	// IMFMediaEngineEx
	vtMEExGetStatistics = 46
	// ID3D10Multithread
	vtMTSetProtected = 5
	// IDXGIDevice / IDXGIObject / IDXGIFactory2 / IDXGISwapChain1 / IDXGIOutput
	vtDevGetAdapter           = 7
	vtObjGetParent            = 6
	vtFactMakeWindowAssoc     = 8
	vtFactCreateSwapChainHwnd = 15
	vtSCPresent               = 8
	vtSCGetBuffer             = 9
	vtSCGetContainingOutput   = 15
	vtOutWaitForVBlank        = 10
)

const (
	meEventError           = 5
	meEventLoadedMetadata  = 10
	meEventPlaying         = 13
	meEventCanPlay         = 14
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
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return uint32(r)
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
	path       string
	swap       string
	statsEvery time.Duration
	mu         sync.Mutex
	running    bool
	quit       chan struct{}
	done       chan struct{}
	wake       chan struct{}
	paused     atomic.Bool
	fpsCap     atomic.Int32 // F-05：>0 时按此帧率节流 Transfer+Present（MF 仍解码每一帧，省的是拷贝/呈现/合成）
	engine     atomic.Uintptr

	// 统计（render 线程写，stats 读）
	presented, appSkipped, loops atomic.Int64
	lastPTS                      atomic.Int64
	restartReq                   atomic.Bool
	engineExForStats             atomic.Uintptr
	tickErr                      atomic.Int64
	endedFallback                atomic.Int64
	readyCh                      chan error
	t0                           time.Time
	firstOnce                    sync.Once
	// 回调（由 mfhost 子进程设置）
	onFatal func(why string)
	onFirst func(ms int64)
	onStats func(st map[string]any)
}

func (b *mfEngine) onEvent(ev uint32, p1 uintptr, p2 uint32) {
	switch ev {
	case meEventError:
		logf("MF_EVENT ERROR code=%d hr=0x%08x", p1, p2)
	case meEventLoadedMetadata, meEventCanPlay:
		logf("MF_EVENT %d", ev)
		// 不依赖 AutoPlay：元数据就绪后显式 Play（Wine 下 AutoPlay 不生效，真机上重复调用无害）
		if e := b.engine.Load(); e != 0 && !b.paused.Load() {
			logf("MF Play hr=0x%08x", vcall(e, vtMEPlay))
		}
	case meEventEnded:
		// SetLoop(TRUE) 时真机不应收到 ENDED【文档】；若收到（Wine 实测会），按 HTML5 语义对已结束的媒体再 Play() 会从头播放
		b.endedFallback.Add(1)
		if e := b.engine.Load(); e != 0 && !b.paused.Load() {
			logf("MF_EVENT ENDED → Play() 兜底循环 hr=0x%08x", vcall(e, vtMEPlay))
		}
	case meEventPlaying, meEventFirstFrameReady:
		logf("MF_EVENT %d", ev)
	case meEventSeeked:
		// 循环播放时 Media Engine 在末尾内部 seek 回 0【推断，待真机日志确认；循环计数另以 pts 回退为准】
	}
}

// start 启动渲染线程并等待初始化结果（MF_READY 或失败），最长 10 s。
func (b *mfEngine) start(hwnd uintptr, w, h int32) error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil
	}
	b.running = true
	b.quit, b.done, b.wake = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	b.readyCh = make(chan error, 1)
	b.t0 = time.Now()
	b.firstOnce = sync.Once{}
	b.restartReq.Store(false)
	b.presented.Store(0)
	b.mu.Unlock()
	initNotify()
	notifySink.Store(b)
	go b.renderThread(hwnd, uint32(w), uint32(h))
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
	go b.statsLoop()
	return nil
}

func (b *mfEngine) stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	close(b.quit)
	done := b.done
	b.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logf("MF stop 超时（render 线程未在 5 s 内退出）")
	}
}

func (b *mfEngine) setPause(p bool) {
	b.paused.Store(p)
	if e := b.engine.Load(); e != 0 {
		if p {
			vcall(e, vtMEPause)
		} else {
			vcall(e, vtMEPlay)
		}
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// fatal：引擎无法继续（DEVICE_REMOVED、卡住）。子进程模式下由 mfhost 发 error{fatal} 后退出，交给主进程的 supervisor 重启。
func (b *mfEngine) fatal(why string) {
	if !b.restartReq.CompareAndSwap(false, true) {
		return
	}
	logf("MF_FATAL_RUNTIME reason=%s", why)
	if b.onFatal != nil {
		go b.onFatal(why)
	}
}

func (b *mfEngine) renderThread(hwnd uintptr, w, h uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	var dev, ctx, mt, mgr, attr, fact, engine, engineEx, dxgiDev, adapter, factory, sc, tex, output uintptr
	defer func() {
		b.engine.Store(0)
		if engine != 0 {
			vcall(engine, vtMEShutdown)
		}
		for _, p := range []*uintptr{&output, &tex, &sc, &factory, &adapter, &dxgiDev, &engineEx, &engine, &fact, &attr, &mgr, &mt, &ctx, &dev} {
			release(p)
		}
		pMFShutdown.Call()
	}()
	fail := func(step string, hr uint32) {
		logf("MF_FATAL %s hr=0x%08x", step, hr)
		select {
		case b.readyCh <- fmt.Errorf("E_LOAD: %s hr=0x%08x", step, hr):
		default:
		}
	}

	pCoInitializeEx.Call(0, 0 /*COINIT_MULTITHREADED*/)
	if r, _, _ := pMFStartup.Call(0x00020070, 0); failed(uint32(r)) {
		fail("MFStartup", uint32(r))
		return
	}
	// D3D11 设备：BGRA_SUPPORT(0x20) | VIDEO_SUPPORT(0x800)
	r, _, _ := pD3D11CreateDevice.Call(0, 1 /*HARDWARE*/, 0, 0x20|0x800, 0, 0, 7,
		uintptr(unsafe.Pointer(&dev)), 0, uintptr(unsafe.Pointer(&ctx)))
	if failed(uint32(r)) {
		fail("D3D11CreateDevice", uint32(r))
		return
	}
	// Media Engine 会在自己的线程用同一设备，必须开多线程保护
	if hr := vcall(dev, vtQueryInterface, uintptr(unsafe.Pointer(&iidID3D10Multithread)), uintptr(unsafe.Pointer(&mt))); !failed(hr) {
		vcall(mt, vtMTSetProtected, 1)
	}
	var token uint32
	if r, _, _ := pMFCreateDXGIDeviceManager.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&mgr))); failed(uint32(r)) {
		fail("MFCreateDXGIDeviceManager", uint32(r))
		return
	}
	if hr := vcall(mgr, vtMgrResetDevice, dev, uintptr(token)); failed(hr) {
		fail("ResetDevice", hr)
		return
	}
	pMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attr)), 3)
	vcall(attr, vtAttrSetUnknown, uintptr(unsafe.Pointer(&mfMediaEngineDXGIManager)), mgr)
	vcall(attr, vtAttrSetUnknown, uintptr(unsafe.Pointer(&mfMediaEngineCallback)), uintptr(unsafe.Pointer(&notifyInst)))
	vcall(attr, vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfMediaEngineVideoOutputFormat)), dxgiFormatB8G8R8A8)
	if r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMFMediaEngineClassFactory)), 0, 1, /*INPROC_SERVER*/
		uintptr(unsafe.Pointer(&iidIMFMediaEngineClassFactory)), uintptr(unsafe.Pointer(&fact))); failed(uint32(r)) {
		fail("CoCreateInstance(MFMediaEngineClassFactory)", uint32(r))
		return
	}
	if hr := vcall(fact, vtFactCreateInstance, 0, attr, uintptr(unsafe.Pointer(&engine))); failed(hr) {
		fail("CreateInstance", hr)
		return
	}
	vcall(engine, vtQueryInterface, uintptr(unsafe.Pointer(&iidIMFMediaEngineEx)), uintptr(unsafe.Pointer(&engineEx)))
	vcall(engine, vtMESetMuted, 1)
	vcall(engine, vtMESetLoop, 1)
	vcall(engine, vtMESetAutoPlay, 1)
	abs, _ := filepath.Abs(b.path)
	bstr, _, _ := pSysAllocString.Call(up(u16(abs)))
	hr := vcall(engine, vtMESetSource, bstr)
	pSysFreeString.Call(bstr)
	if failed(hr) {
		fail("SetSource", hr)
		return
	}
	vcall(engine, vtMELoad)

	// 交换链：从设备反查 IDXGIFactory2
	vcall(dev, vtQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIDevice)), uintptr(unsafe.Pointer(&dxgiDev)))
	vcall(dxgiDev, vtDevGetAdapter, uintptr(unsafe.Pointer(&adapter)))
	if hr := vcall(adapter, vtObjGetParent, uintptr(unsafe.Pointer(&iidIDXGIFactory2)), uintptr(unsafe.Pointer(&factory))); failed(hr) {
		fail("GetParent(IDXGIFactory2)", hr)
		return
	}
	type scDesc1 struct {
		Width, Height, Format, Stereo, SampleCount, SampleQuality, BufferUsage, BufferCount, Scaling, SwapEffect, AlphaMode, Flags uint32
	}
	d := scDesc1{Width: w, Height: h, Format: dxgiFormatB8G8R8A8, SampleCount: 1, BufferUsage: 0x20 /*RENDER_TARGET_OUTPUT*/, BufferCount: 2, SwapEffect: 4 /*FLIP_DISCARD*/}
	if b.swap == "blt" {
		d.BufferCount, d.SwapEffect = 1, 0 /*DISCARD*/
	}
	if hr := vcall(factory, vtFactCreateSwapChainHwnd, dev, hwnd, uintptr(unsafe.Pointer(&d)), 0, 0, uintptr(unsafe.Pointer(&sc))); failed(hr) {
		fail("CreateSwapChainForHwnd(swap="+b.swap+")，可尝试 -swap=blt", hr)
		return
	}
	vcall(factory, vtFactMakeWindowAssoc, hwnd, 2 /*NO_ALT_ENTER*/)
	if hr := vcall(sc, vtSCGetBuffer, 0, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&tex))); failed(hr) {
		fail("GetBuffer", hr)
		return
	}
	if hr := vcall(sc, vtSCGetContainingOutput, uintptr(unsafe.Pointer(&output))); failed(hr) {
		logf("MF GetContainingOutput 失败 hr=0x%08x，退化为 4 ms 轮询", hr)
		output = 0
	}
	b.engine.Store(engine)
	if b.paused.Load() {
		vcall(engine, vtMEPause)
	}
	logf("MF_READY hwnd=0x%x %dx%d swap=%s engineEx=%v", hwnd, w, h, b.swap, engineEx != 0)
	b.readyCh <- nil
	if engineEx != 0 {
		b.engineExForStats.Store(engineEx)
		defer b.engineExForStats.Store(0)
	}

	dst := rect{0, 0, int32(w), int32(h)}
	black := uint32(0xff000000)
	var lastPTS, minDelta int64 = -1, 0
	var lastPresent time.Time
	for {
		select {
		case <-b.quit:
			return
		default:
		}
		if b.paused.Load() { // 暂停：不再等 vblank、不 Present → GPU/CPU 约 0
			select {
			case <-b.quit:
				return
			case <-b.wake:
			}
			continue
		}
		if output != 0 {
			vcall(output, vtOutWaitForVBlank)
		} else {
			time.Sleep(4 * time.Millisecond)
		}
		var pts int64
		if thr := vcall(engine, vtMEOnVideoStreamTick, uintptr(unsafe.Pointer(&pts))); thr != 0 { // S_FALSE=无新帧
			if failed(thr) && b.tickErr.Add(1) == 1 {
				logf("MF OnVideoStreamTick 首次错误 hr=0x%08x", thr)
			}
			continue
		}
		if lastPTS >= 0 {
			delta := pts - lastPTS
			switch {
			case delta < 0:
				b.loops.Add(1) // 时间戳回退 = 循环回到开头
			case delta > 0 && (minDelta == 0 || delta < minDelta):
				minDelta = delta
			case minDelta > 0 && delta > minDelta*3/2:
				b.appSkipped.Add(delta/minDelta - 1) // 我们的取帧线程错过的帧（与 MF 内部丢帧分开统计）
			}
		}
		lastPTS = pts
		b.lastPTS.Store(pts)
		if c := b.fpsCap.Load(); c > 0 && time.Since(lastPresent) < time.Second/time.Duration(c)-2*time.Millisecond {
			continue // 限帧：本 vblank 不取帧
		}
		lastPresent = time.Now()
		if hr := vcall(engine, vtMETransferVideoFrame, tex, 0, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&black))); failed(hr) {
			logf("MF TransferVideoFrame hr=0x%08x", hr)
			continue
		}
		hr := vcall(sc, vtSCPresent, 0, 0) // 已在 vblank 后，syncInterval=0 避免再等一帧
		if hr == dxgiErrDeviceRemoved || hr == dxgiErrDeviceReset {
			logf("DEVICE_REMOVED hr=0x%08x → 重建 MF 后端（W-T9）", hr)
			b.fatal("device-removed")
			return
		}
		if b.presented.Add(1) == 1 && b.onFirst != nil {
			ms := time.Since(b.t0).Milliseconds()
			go b.onFirst(ms)
		}
	}
}

func (b *mfEngine) stat(id uint32) int64 {
	ex := b.engineExForStats.Load()
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
func (b *mfEngine) statsLoop() {
	t := time.NewTicker(b.statsEvery)
	defer t.Stop()
	type key struct{ loops, pts int64 }
	var last key
	same := 0
	quit := b.quit
	for {
		select {
		case <-quit:
			return
		case <-t.C:
		}
		k := key{b.loops.Load(), b.lastPTS.Load()}
		st := map[string]any{"rendered": b.stat(meStatFramesRendered), "dropped": b.stat(meStatFramesDropped),
			"corrupted": b.stat(meStatFramesCorrupted), "presented": b.presented.Load(), "appSkipped": b.appSkipped.Load(),
			"loops": k.loops, "endedFallback": b.endedFallback.Load(), "pts100ns": k.pts, "paused": b.paused.Load()}
		if b.onStats != nil {
			b.onStats(st)
		}
		logf("MFSTATS rendered=%d dropped=%d corrupted=%d presented=%d appSkipped=%d loops=%d endedFallback=%d pts100ns=%d paused=%v",
			b.stat(meStatFramesRendered), b.stat(meStatFramesDropped), b.stat(meStatFramesCorrupted),
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
