//go:build darwin

package reactive

import (
	"fmt"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"sync"
	"time"
	"unsafe"
	"wpapp/internal/platform/macos"
)

var sel = objc.RegisterName

func cls(s string) objc.ID { return objc.ID(objc.GetClass(s)) }

type macPointer struct {
	monitor objc.ID
	block   objc.Block
	sample  PointerSample
}

func newPointer() Pointer { return &macPointer{} }
func (p *macPointer) Start() error {
	if macos.Main == nil {
		macos.Main = func(fn func()) { fn() }
	}
	if e := macos.Load(); e != nil {
		return e
	}
	// Mouse-only masks: left/right/other down/up, moved and dragged. No key events.
	const mask = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 5) | (1 << 6) | (1 << 7) | (1 << 25) | (1 << 26) | (1 << 27)
	p.block = objc.NewBlock(func(_ objc.Block, event objc.ID) { p.update() })
	p.monitor = cls("NSEvent").Send(sel("addGlobalMonitorForEventsMatchingMask:handler:"), uint64(mask), p.block)
	if p.monitor == 0 {
		p.block.Release()
		return fmt.Errorf("mouse: NSEvent global monitor unavailable")
	}
	p.update()
	return nil
}
func (p *macPointer) update() {
	point := objc.Send[macos.CGPoint](cls("NSEvent"), sel("mouseLocation"))
	screens := cls("NSScreen").Send(sel("screens"))
	first := screens.Send(sel("firstObject"))
	frame := objc.Send[macos.CGRect](first, sel("frame"))
	p.sample = PointerSample{X: point.X, Y: frame.Size.H - point.Y, Buttons: uint32(cls("NSEvent").Send(sel("pressedMouseButtons")))}
}
func (p *macPointer) Stop() {
	if p.monitor != 0 {
		cls("NSEvent").Send(sel("removeMonitor:"), p.monitor)
		p.monitor = 0
		p.block.Release()
	}
	p.sample = PointerSample{}
}
func (p *macPointer) Sample() PointerSample {
	v := p.sample
	arr := macos.CGWindowListCopyWindowInfo(1|16, 0)
	if arr == 0 {
		return v
	}
	defer macos.CFRelease(arr)
	// On-screen windows at normal layer or above deny forwarding at the pointer,
	// including menu/popover overlays. CG window bounds require no window titles.
	for i, n := 0, macos.CFArrayGetCount(arr); i < n; i++ {
		d := macos.CFArrayGetValueAtIndex(arr, i)
		var layer int32
		if !macos.CFNumberGetValue(macos.CFDictionaryGetValue(d, macos.KLayer), 3, unsafe.Pointer(&layer)) {
			return v
		}
		if layer < 0 {
			continue
		}
		var r macos.CGRect
		if !macos.CGRectMakeWithDict(macos.CFDictionaryGetValue(d, macos.KBounds), &r) {
			return v
		}
		if v.X >= r.Origin.X && v.X < r.Origin.X+r.Size.W && v.Y >= r.Origin.Y && v.Y < r.Origin.Y+r.Size.H {
			return v
		}
	}
	v.Desktop = true
	return v
}

type macAudio struct {
	mu             sync.Mutex
	stream, output objc.ID
	ring           []float32
	active         bool
	failure        error
	lastSample     time.Time
}

func newAudio() Source { return &macAudio{} }

var audioClass objc.Class
var audioOnce sync.Once
var audioError error
var audioSources sync.Map
var sampleCount func(uintptr) int
var sampleFormat func(uintptr) uintptr
var formatASBD func(uintptr) uintptr
var copyPCM func(uintptr, int32, int32, unsafe.Pointer) int32
var preflight func() bool
var globalQueue func(int64, uintptr) uintptr

// AudioStreamBasicDescription and a mono AudioBufferList use fixed native ABI.
type asbd struct {
	Rate                                                                           float64
	Format, Flags, BytesPacket, FramesPacket, BytesFrame, Channels, Bits, Reserved uint32
}
type audioBufferList struct {
	Count, Pad      uint32
	Channels, Bytes uint32
	Data            unsafe.Pointer
}

func loadAudio() {
	audioOnce.Do(func() {
		if macos.Main == nil {
			macos.Main = func(fn func()) { fn() }
		}
		if e := macos.Load(); e != nil {
			audioError = e
			return
		}
		for _, name := range []string{"ScreenCaptureKit", "CoreMedia"} {
			h, e := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if e != nil {
				audioError = fmt.Errorf("ScreenCaptureKit unsupported: %w", e)
				return
			}
			if name == "CoreMedia" {
				purego.RegisterLibFunc(&sampleCount, h, "CMSampleBufferGetNumSamples")
				purego.RegisterLibFunc(&sampleFormat, h, "CMSampleBufferGetFormatDescription")
				purego.RegisterLibFunc(&formatASBD, h, "CMAudioFormatDescriptionGetStreamBasicDescription")
				purego.RegisterLibFunc(&copyPCM, h, "CMSampleBufferCopyPCMDataIntoAudioBufferList")
			}
		}
		cg, e := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
		if e != nil {
			audioError = e
			return
		}
		purego.RegisterLibFunc(&preflight, cg, "CGPreflightScreenCaptureAccess")
		lib, e := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		if e != nil {
			audioError = e
			return
		}
		purego.RegisterLibFunc(&globalQueue, lib, "dispatch_get_global_queue")
		audioClass, audioError = objc.RegisterClass("WPKAudioOutput", objc.GetClass("NSObject"), []*objc.Protocol{objc.GetProtocol("SCStreamOutput"), objc.GetProtocol("SCStreamDelegate")}, nil, []objc.MethodDef{
			{Cmd: sel("stream:didOutputSampleBuffer:ofType:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, b uintptr, kind int64) {
				if kind != 1 {
					return
				}
				if v, ok := audioSources.Load(self); ok {
					v.(*macAudio).receive(b)
				}
			}},
			{Cmd: sel("stream:didStopWithError:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, e objc.ID) {
				if v, ok := audioSources.Load(self); ok {
					s := v.(*macAudio)
					s.mu.Lock()
					s.failure = fmt.Errorf("ScreenCaptureKit stopped: %s", macos.GoString(e.Send(sel("localizedDescription"))))
					s.mu.Unlock()
				}
			}},
		})
	})
}
func (s *macAudio) Start() (err error) {
	loadAudio()
	if audioError != nil {
		return audioError
	}
	config := cls("SCStreamConfiguration").Send(sel("new"))
	if config == 0 {
		return fmt.Errorf("ScreenCaptureKit unsupported")
	}
	defer config.Send(sel("release"))
	if !objc.Send[bool](config, sel("respondsToSelector:"), sel("setCapturesAudio:")) {
		return fmt.Errorf("ScreenCaptureKit audio unsupported: macOS 13 or later required")
	}
	if !preflight() {
		return fmt.Errorf("ScreenCaptureKit unsupported in current TCC state: Screen Recording access not granted (undetermined or denied); authorize the app in System Settings and restart")
	}
	// Asynchronous enumeration uses a buffered channel; retained content is
	// released by the callback even if the operation has already timed out.
	type contentResult struct {
		content objc.ID
		err     error
	}
	done := make(chan contentResult)
	cancel := make(chan struct{})
	defer close(cancel)
	block := objc.NewBlock(func(_ objc.Block, content, e objc.ID) {
		var result contentResult
		if e != 0 {
			result.err = fmt.Errorf("ScreenCaptureKit content: %s", macos.GoString(e.Send(sel("localizedDescription"))))
		} else {
			result.content = content.Send(sel("retain"))
		}
		select {
		case done <- result:
		case <-cancel:
			if result.content != 0 {
				result.content.Send(sel("release"))
			}
		}
	})
	defer block.Release()
	cls("SCShareableContent").Send(sel("getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"), true, true, block)
	var content objc.ID
	select {
	case r := <-done:
		if r.err != nil {
			return r.err
		}
		content = r.content
	case <-time.After(3 * time.Second):
		return fmt.Errorf("ScreenCaptureKit content timeout")
	}
	defer content.Send(sel("release"))
	display := content.Send(sel("displays")).Send(sel("firstObject"))
	if display == 0 {
		return fmt.Errorf("ScreenCaptureKit: no display")
	}
	filter := cls("SCContentFilter").Send(sel("alloc")).Send(sel("initWithDisplay:excludingWindows:"), display, cls("NSArray").Send(sel("array")))
	defer filter.Send(sel("release"))
	config.Send(sel("setCapturesAudio:"), true)
	config.Send(sel("setExcludesCurrentProcessAudio:"), true)
	config.Send(sel("setSampleRate:"), 48000)
	config.Send(sel("setChannelCount:"), 1)
	config.Send(sel("setWidth:"), 2)
	config.Send(sel("setHeight:"), 2)
	s.output = objc.ID(audioClass).Send(sel("new"))
	audioSources.Store(s.output, s)
	s.stream = cls("SCStream").Send(sel("alloc")).Send(sel("initWithFilter:configuration:delegate:"), filter, config, s.output)
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()
	var nativeError objc.ID
	if !objc.Send[bool](s.stream, sel("addStreamOutput:type:sampleHandlerQueue:error:"), s.output, 1, globalQueue(0, 0), &nativeError) {
		return fmt.Errorf("ScreenCaptureKit audio output: %s", macos.GoString(nativeError.Send(sel("localizedDescription"))))
	}
	s.mu.Lock()
	s.active = true
	s.ring = make([]float32, 4096)
	s.failure = nil
	s.mu.Unlock()
	return streamOperation(s.stream, "startCaptureWithCompletionHandler:")
}
func streamOperation(stream objc.ID, method string) error {
	done := make(chan error, 1)
	b := objc.NewBlock(func(_ objc.Block, e objc.ID) {
		var err error
		if e != 0 {
			err = fmt.Errorf("ScreenCaptureKit: %s", macos.GoString(e.Send(sel("localizedDescription"))))
		}
		done <- err
	})
	defer b.Release()
	stream.Send(sel(method), b)
	select {
	case e := <-done:
		return e
	case <-time.After(3 * time.Second):
		return fmt.Errorf("ScreenCaptureKit operation timeout")
	}
}
func (s *macAudio) Stop() {
	s.mu.Lock()
	s.active = false
	clear(s.ring[:cap(s.ring)])
	s.ring = nil
	s.mu.Unlock()
	if s.stream != 0 {
		if e := streamOperation(s.stream, "stopCaptureWithCompletionHandler:"); e != nil {
			panic(e)
		} // Fail closed: terminate the isolated host if stopping cannot be confirmed.
		s.stream.Send(sel("release"))
		s.stream = 0
	}
	if s.output != 0 {
		audioSources.Delete(s.output)
		s.output.Send(sel("release"))
		s.output = 0
	}
}
func (s *macAudio) receive(b uintptr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	n := sampleCount(b)
	if n < 1 || n > 48000 {
		return
	}
	f := formatASBD(sampleFormat(b))
	if f == 0 {
		return
	}
	format := *(*asbd)(unsafe.Pointer(f))
	if format.Format != 0x6c70636d || format.Rate != 48000 || format.Channels != 1 || format.Bits != 32 || format.Flags&1 == 0 || format.Flags&2 != 0 {
		s.failure = fmt.Errorf("ScreenCaptureKit unsupported PCM format")
		return
	}
	pcm := make([]float32, n)
	list := audioBufferList{Count: 1, Channels: 1, Bytes: uint32(n * 4), Data: unsafe.Pointer(&pcm[0])}
	if copyPCM(b, 0, int32(n), unsafe.Pointer(&list)) != 0 {
		s.failure = fmt.Errorf("ScreenCaptureKit PCM copy failed")
		return
	}
	s.lastSample = time.Now()
	s.ring = append(s.ring, pcm...)
	if len(s.ring) > 4096 {
		copy(s.ring, s.ring[len(s.ring)-4096:])
		clear(s.ring[4096:])
		s.ring = s.ring[:4096]
	}
}
func (s *macAudio) Read() ([]float32, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastSample) > 150*time.Millisecond {
		clear(s.ring)
	}
	return append([]float32(nil), s.ring...), 48000, s.failure
}
