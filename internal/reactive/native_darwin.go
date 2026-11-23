//go:build darwin

package reactive

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"wpapp/internal/platform/macos"
)

var sel = objc.RegisterName

func cls(s string) objc.ID { return objc.ID(objc.GetClass(s)) }

const (
	pointerSampleInterval = 16 * time.Millisecond

	asyncPending  int32 = 0
	asyncDone     int32 = 1
	asyncTimedOut int32 = 2

	// SCStreamErrorAttemptToStopStreamState (-3808): stream is already stopped.
	scStreamErrorAttemptToStopStreamState int64 = -3808
)

type macPointer struct {
	mu     sync.Mutex
	ready  bool
	last   PointerSample
	lastAt time.Time
}

func newPointer() Pointer { return &macPointer{} }

func (p *macPointer) Start() error {
	if e := macos.LoadFrameworks(); e != nil {
		return e
	}
	p.mu.Lock()
	p.ready = true
	p.last = PointerSample{}
	p.lastAt = time.Time{}
	p.mu.Unlock()
	return nil
}

func (p *macPointer) Stop() {
	p.mu.Lock()
	p.ready = false
	p.last = PointerSample{}
	p.lastAt = time.Time{}
	p.mu.Unlock()
}

func (p *macPointer) Sample() PointerSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ready {
		return PointerSample{}
	}
	now := time.Now()
	if !p.lastAt.IsZero() && now.Sub(p.lastAt) < pointerSampleInterval {
		return p.last
	}
	v := sampleMacPointer()
	p.last = v
	p.lastAt = now
	return v
}

func sampleMacPointer() PointerSample {
	pool := cls("NSAutoreleasePool").Send(sel("new"))
	if pool != 0 {
		defer pool.Send(sel("drain"))
	}
	screens := cls("NSScreen").Send(sel("screens"))
	if screens == 0 {
		return PointerSample{}
	}
	first := screens.Send(sel("firstObject"))
	if first == 0 {
		return PointerSample{}
	}
	frame := objc.Send[macos.CGRect](first, sel("frame"))
	point := objc.Send[macos.CGPoint](cls("NSEvent"), sel("mouseLocation"))
	v := PointerSample{
		X:       point.X,
		Y:       frame.Size.H - point.Y,
		Buttons: uint32(cls("NSEvent").Send(sel("pressedMouseButtons"))),
	}
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
		layerNum := macos.CFDictionaryGetValue(d, macos.KLayer)
		if layerNum == 0 || !macos.CFNumberGetValue(layerNum, 3, unsafe.Pointer(&layer)) {
			return v
		}
		if layer < 0 {
			continue
		}
		// Skip invisible system helper windows (alpha <= 0.01).
		if macos.KAlpha != 0 {
			if alphaNum := macos.CFDictionaryGetValue(d, macos.KAlpha); alphaNum != 0 {
				var alpha float64
				if macos.CFNumberGetValue(alphaNum, 6 /* kCFNumberFloat64Type */, unsafe.Pointer(&alpha)) && alpha <= 0.01 {
					continue
				}
			}
		}
		var r macos.CGRect
		bDict := macos.CFDictionaryGetValue(d, macos.KBounds)
		if bDict == 0 || !macos.CGRectMakeWithDict(bDict, &r) {
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
	running        bool
	failure        error
	lastSample     time.Time
}

func newAudio() Source { return &macAudio{} }

var (
	audioClass   objc.Class
	audioOnce    sync.Once
	audioError   error
	audioSources sync.Map
	sampleCount  func(uintptr) int
	sampleFormat func(uintptr) uintptr
	formatASBD   func(uintptr) *asbd
	copyPCM      func(uintptr, int32, int32, unsafe.Pointer) int32
	preflight    func() bool
	globalQueue  func(int64, uintptr) uintptr
)

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
		if e := macos.LoadFrameworks(); e != nil {
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
				pool := cls("NSAutoreleasePool").Send(sel("new"))
				if pool != 0 {
					defer pool.Send(sel("drain"))
				}
				if kind != 1 {
					return
				}
				if v, ok := audioSources.Load(self); ok {
					v.(*macAudio).receive(b)
				}
			}},
			{Cmd: sel("stream:didStopWithError:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, e objc.ID) {
				pool := cls("NSAutoreleasePool").Send(sel("new"))
				if pool != 0 {
					defer pool.Send(sel("drain"))
				}
				if v, ok := audioSources.Load(self); ok {
					s := v.(*macAudio)
					var desc string
					if e != 0 {
						desc = macos.GoString(e.Send(sel("localizedDescription")))
					}
					s.mu.Lock()
					s.running = false
					s.active = false
					if desc != "" {
						s.failure = fmt.Errorf("ScreenCaptureKit stopped: %s", desc)
					} else {
						s.failure = fmt.Errorf("ScreenCaptureKit stopped")
					}
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
	// Asynchronous enumeration transfers block ownership atomically on timeout
	// so the ObjC completion handler is never freed before it executes.
	type contentResult struct {
		content objc.ID
		err     error
	}
	done := make(chan contentResult, 1)
	var state atomic.Int32
	block := objc.NewBlock(func(self objc.Block, content, e objc.ID) {
		pool := cls("NSAutoreleasePool").Send(sel("new"))
		var result contentResult
		if e != 0 {
			result.err = fmt.Errorf("ScreenCaptureKit content: %s", macos.GoString(e.Send(sel("localizedDescription"))))
		} else if content != 0 {
			result.content = content.Send(sel("retain"))
		}
		if pool != 0 {
			pool.Send(sel("drain"))
		}
		if state.CompareAndSwap(asyncPending, asyncDone) {
			done <- result
		} else {
			if result.content != 0 {
				result.content.Send(sel("release"))
			}
			self.Release()
		}
	})
	cls("SCShareableContent").Send(sel("getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"), true, true, block)
	var content objc.ID
	select {
	case r := <-done:
		block.Release()
		if r.err != nil {
			return r.err
		}
		content = r.content
	case <-time.After(3 * time.Second):
		if state.CompareAndSwap(asyncPending, asyncTimedOut) {
			return fmt.Errorf("ScreenCaptureKit content timeout")
		}
		r := <-done
		block.Release()
		if r.err != nil {
			return r.err
		}
		content = r.content
	}
	if content == 0 {
		return fmt.Errorf("ScreenCaptureKit: empty shareable content")
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
	output := objc.ID(audioClass).Send(sel("new"))
	audioSources.Store(output, s)
	stream := cls("SCStream").Send(sel("alloc")).Send(sel("initWithFilter:configuration:delegate:"), filter, config, output)
	s.mu.Lock()
	s.output = output
	s.stream = stream
	s.running = false
	s.mu.Unlock()
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()
	if stream == 0 {
		return fmt.Errorf("ScreenCaptureKit: failed to allocate SCStream")
	}
	var nativeError objc.ID
	if !objc.Send[bool](stream, sel("addStreamOutput:type:sampleHandlerQueue:error:"), output, 1, globalQueue(0, 0), &nativeError) {
		return fmt.Errorf("ScreenCaptureKit audio output: %s", macos.GoString(nativeError.Send(sel("localizedDescription"))))
	}
	s.mu.Lock()
	s.active = true
	s.ring = make([]float32, 4096)
	s.failure = nil
	s.mu.Unlock()
	opErr, timedOut := streamOperation(stream, "startCaptureWithCompletionHandler:", false)
	if opErr != nil {
		if timedOut {
			// Stream may finish starting asynchronously after timeout; allow Stop() to attempt stopCapture.
			s.mu.Lock()
			s.running = true
			s.mu.Unlock()
		}
		return opErr
	}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	return nil
}

func streamOperation(stream objc.ID, method string, ignoreAlreadyStopped bool) (error, bool) {
	done := make(chan error, 1)
	var state atomic.Int32
	b := objc.NewBlock(func(self objc.Block, e objc.ID) {
		pool := cls("NSAutoreleasePool").Send(sel("new"))
		var err error
		if e != 0 {
			code := objc.Send[int64](e, sel("code"))
			if !(ignoreAlreadyStopped && code == scStreamErrorAttemptToStopStreamState) {
				err = fmt.Errorf("ScreenCaptureKit: %s (code %d)", macos.GoString(e.Send(sel("localizedDescription"))), code)
			}
		}
		if pool != 0 {
			pool.Send(sel("drain"))
		}
		if state.CompareAndSwap(asyncPending, asyncDone) {
			done <- err
		} else {
			// Caller timed out; callback now owns releasing the block.
			self.Release()
		}
	})
	stream.Send(sel(method), b)
	select {
	case e := <-done:
		b.Release()
		return e, false
	case <-time.After(3 * time.Second):
		if state.CompareAndSwap(asyncPending, asyncTimedOut) {
			return fmt.Errorf("ScreenCaptureKit operation timeout"), true
		}
		e := <-done
		b.Release()
		return e, false
	}
}

func (s *macAudio) Stop() {
	s.mu.Lock()
	s.active = false
	wasRunning := s.running
	s.running = false
	if s.ring != nil {
		clear(s.ring[:cap(s.ring)])
		s.ring = nil
	}
	stream := s.stream
	s.stream = 0
	output := s.output
	s.output = 0
	s.mu.Unlock()

	if stream != 0 {
		if wasRunning {
			if e, _ := streamOperation(stream, "stopCaptureWithCompletionHandler:", true); e != nil {
				slog.Warn("ScreenCaptureKit stopCapture failed", "err", e)
			}
		}
		stream.Send(sel("release"))
	}
	if output != 0 {
		audioSources.Delete(output)
		output.Send(sel("release"))
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
	if f == nil {
		return
	}
	format := *f
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
	for i, v := range pcm {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			pcm[i] = 0
		}
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
