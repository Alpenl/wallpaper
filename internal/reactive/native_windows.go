//go:build windows

package reactive

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	ole32  = syscall.NewLazyDLL("ole32.dll")

	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pWindowFromPoint  = user32.NewProc("WindowFromPoint")
	pGetAncestor      = user32.NewProc("GetAncestor")
	pGetClassNameW    = user32.NewProc("GetClassNameW")
	pGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")

	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoUninitialize   = ole32.NewProc("CoUninitialize")
	pCoCreateInstance = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

type winPointer struct{}

func newPointer() Pointer        { return &winPointer{} }
func (*winPointer) Start() error { return nil }
func (*winPointer) Stop()        {}
func (*winPointer) Sample() PointerSample {
	var p struct{ X, Y int32 }
	ok, _, _ := pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return PointerSample{}
	}
	// WindowFromPoint identifies the actual hit window, including windows too
	// small to trigger the whole-display coverage policy. Never install a hook.
	packed := uintptr(uint64(uint32(p.X)) | uint64(uint32(p.Y))<<32)
	w, _, _ := pWindowFromPoint.Call(packed)
	w, _, _ = pGetAncestor.Call(w, 2) // GA_ROOT
	var name [256]uint16
	pGetClassNameW.Call(w, uintptr(unsafe.Pointer(&name[0])), 256)
	class := syscall.UTF16ToString(name[:])
	desktop := class == "Progman" || class == "WorkerW"
	var buttons uint32
	if desktop {
		for i, k := range []uintptr{1, 2, 4} {
			b, _, _ := pGetAsyncKeyState.Call(k)
			if b&0x8000 != 0 {
				buttons |= 1 << i
			}
		}
	}
	return PointerSample{float64(p.X), float64(p.Y), buttons, desktop}
}

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var (
	enumeratorClass = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	enumeratorID    = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	audioClientID   = guid{0x1cb9ad4c, 0xdbfa, 0x4c32, [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	captureClientID = guid{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}

	// Full 16-byte KSDATAFORMAT_SUBTYPE_PCM and KSDATAFORMAT_SUBTYPE_IEEE_FLOAT GUIDs.
	subformatPCM       = [16]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}
	subformatIEEEFloat = [16]byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}
)

const (
	hrRPCChangedMode              uint32 = 0x80010106
	hrNotFound                    uint32 = 0x80070490 // HRESULT_FROM_WIN32(ERROR_NOT_FOUND)
	hrAudclntDeviceInvalidated    uint32 = 0x88890004 // AUDCLNT_E_DEVICE_INVALIDATED
	hrAudclntDeviceInUse          uint32 = 0x8889000A // AUDCLNT_E_DEVICE_IN_USE
	hrAudclntEndpointCreateFailed uint32 = 0x8889000F // AUDCLNT_E_ENDPOINT_CREATE_FAILED
	hrAudclntServiceNotRunning    uint32 = 0x88890010 // AUDCLNT_E_SERVICE_NOT_RUNNING
	hrAudclntResourcesInvalidated uint32 = 0x88890026 // AUDCLNT_E_RESOURCES_INVALIDATED
	wasapiSilenceDecay                   = 150 * time.Millisecond
	wasapiInitialReconnectBackoff        = 250 * time.Millisecond
	wasapiMaxReconnectBackoff            = 2 * time.Second
)

func isTransientWASAPIError(code uint32) bool {
	switch code {
	case hrNotFound,
		hrAudclntDeviceInvalidated,
		hrAudclntDeviceInUse,
		hrAudclntEndpointCreateFailed,
		hrAudclntServiceNotRunning,
		hrAudclntResourcesInvalidated:
		return true
	default:
		return false
	}
}

type comObject struct {
	vtbl unsafe.Pointer
}

func comMethodAddr(p *comObject, index int) uintptr {
	return *(*uintptr)(unsafe.Add(p.vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

func method[T any](p *comObject, index int, fn *T) {
	purego.RegisterFunc(fn, comMethodAddr(p, index))
}

func releaseCOM(p *comObject) {
	if p != nil {
		syscall.SyscallN(comMethodAddr(p, 2), uintptr(unsafe.Pointer(p)))
	}
}

func hr(r int32) error {
	if r < 0 {
		return fmt.Errorf("WASAPI unsupported/unavailable: HRESULT 0x%08x", uint32(r))
	}
	return nil
}

type waveFormatSpec struct {
	rate, channels, bits, align int
	floating                    bool
}

func parseWaveFormat(hdr []byte, ext []byte) (waveFormatSpec, error) {
	if len(hdr) < 18 {
		return waveFormatSpec{}, fmt.Errorf("WASAPI unsupported mix format")
	}
	tag := binary.LittleEndian.Uint16(hdr[0:2])
	spec := waveFormatSpec{
		channels: int(binary.LittleEndian.Uint16(hdr[2:4])),
		rate:     int(binary.LittleEndian.Uint32(hdr[4:8])),
		align:    int(binary.LittleEndian.Uint16(hdr[12:14])),
		bits:     int(binary.LittleEndian.Uint16(hdr[14:16])),
	}
	cbSize := binary.LittleEndian.Uint16(hdr[16:18])
	pcm := false
	switch tag {
	case 1:
		pcm = true
	case 3:
		spec.floating = true
	case 0xfffe:
		if cbSize < 22 || len(ext) < 40 {
			return waveFormatSpec{}, fmt.Errorf("WASAPI unsupported extensible mix format")
		}
		var sub [16]byte
		copy(sub[:], ext[24:40])
		switch sub {
		case subformatPCM:
			pcm = true
		case subformatIEEEFloat:
			spec.floating = true
		default:
			return waveFormatSpec{}, fmt.Errorf("WASAPI unsupported mix subformat GUID")
		}
	default:
		return waveFormatSpec{}, fmt.Errorf("WASAPI unsupported mix format tag 0x%x", tag)
	}
	if spec.channels < 1 || spec.channels > 32 || spec.rate < 8000 || spec.rate > 192000 ||
		spec.align != spec.channels*(spec.bits/8) ||
		!(spec.floating && spec.bits == 32 || pcm && (spec.bits == 16 || spec.bits == 32)) {
		return waveFormatSpec{}, fmt.Errorf("WASAPI unsupported mix format")
	}
	return spec, nil
}

type readReply struct {
	pcm  []float32
	rate int
	err  error
}

type winAudio struct {
	mu   sync.Mutex
	reqs chan chan readReply
	stop chan struct{}
	done chan struct{}

	// Owned exclusively by the locked COM worker goroutine.
	client, capture             *comObject
	fnGetNextPacketSize         func(*comObject, *uint32) int32
	fnGetBuffer                 func(*comObject, *unsafe.Pointer, *uint32, *uint32, uintptr, uintptr) int32
	fnReleaseBuffer             func(*comObject, uint32) int32
	rate, channels, bits, align int
	floating                    bool
	ring                        []float32
	lastData                    time.Time
	nextRetry                   time.Time
	retryBackoff                time.Duration
}

func newAudio() Source { return &winAudio{} }

func (s *winAudio) Start() error {
	s.mu.Lock()
	if s.done != nil {
		s.mu.Unlock()
		return nil
	}
	reqs := make(chan chan readReply)
	stop := make(chan struct{})
	done := make(chan struct{})
	s.reqs, s.stop, s.done = reqs, stop, done
	s.mu.Unlock()

	initErr := make(chan error, 1)
	// winAudio owns this locked COM thread; Stop closes stop and joins done.
	go s.runCOMLoop(reqs, stop, done, initErr)
	if err := <-initErr; err != nil {
		<-done
		s.mu.Lock()
		if s.done == done {
			s.reqs, s.stop, s.done = nil, nil, nil
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *winAudio) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.reqs, s.stop, s.done = nil, nil, nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if done != nil {
		<-done
	}
}

func (s *winAudio) Read() ([]float32, int, error) {
	s.mu.Lock()
	reqs, stop, done := s.reqs, s.stop, s.done
	s.mu.Unlock()
	if reqs == nil {
		return nil, 0, nil
	}
	reply := make(chan readReply, 1)
	select {
	case reqs <- reply:
	case <-stop:
		return nil, 0, nil
	case <-done:
		return nil, 0, nil
	}
	select {
	case r := <-reply:
		return r.pcm, r.rate, r.err
	case <-done:
		return nil, 0, nil
	}
}

func (s *winAudio) runCOMLoop(reqs <-chan chan readReply, stop <-chan struct{}, done chan struct{}, initErr chan<- error) {
	runtime.LockOSThread()
	defer close(done)

	r, _, _ := pCoInitializeEx.Call(0, 0)
	if int32(r) >= 0 {
		defer pCoUninitialize.Call()
	} else if uint32(r) != hrRPCChangedMode {
		initErr <- hr(int32(r))
		return
	}
	defer func() {
		s.closeDevice()
		if s.ring != nil {
			clear(s.ring[:cap(s.ring)])
			s.ring = nil
		}
	}()

	if code, err := s.openDevice(); err != nil {
		if !isTransientWASAPIError(code) {
			initErr <- err
			return
		}
		s.scheduleReconnect(time.Now())
	}
	initErr <- nil

	for {
		select {
		case <-stop:
			return
		case reply := <-reqs:
			pcm, rate, err := s.readOnCOMThread(time.Now())
			reply <- readReply{pcm: pcm, rate: rate, err: err}
		}
	}
}

func (s *winAudio) scheduleReconnect(now time.Time) {
	if s.retryBackoff <= 0 {
		s.retryBackoff = wasapiInitialReconnectBackoff
	} else {
		s.retryBackoff = min(s.retryBackoff*2, wasapiMaxReconnectBackoff)
	}
	s.nextRetry = now.Add(s.retryBackoff)
}

func (s *winAudio) openDevice() (uint32, error) {
	s.closeDevice()
	var en, dev *comObject
	r, _, _ := pCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&enumeratorClass)), 0, 1,
		uintptr(unsafe.Pointer(&enumeratorID)), uintptr(unsafe.Pointer(&en)),
	)
	if err := hr(int32(r)); err != nil {
		return uint32(r), err
	}
	defer releaseCOM(en)

	var endpoint func(*comObject, int32, int32, **comObject) int32
	method(en, 4, &endpoint)
	if code := endpoint(en, 0, 1, &dev); code < 0 {
		return uint32(code), hr(code)
	}
	defer releaseCOM(dev)

	var activate func(*comObject, *guid, uint32, uintptr, **comObject) int32
	method(dev, 3, &activate)
	if code := activate(dev, &audioClientID, 23, 0, &s.client); code < 0 {
		s.closeDevice()
		return uint32(code), hr(code)
	}

	var format unsafe.Pointer
	var mix func(*comObject, *unsafe.Pointer) int32
	method(s.client, 8, &mix)
	if code := mix(s.client, &format); code < 0 {
		s.closeDevice()
		return uint32(code), hr(code)
	}
	if format == nil {
		s.closeDevice()
		return 0, fmt.Errorf("WASAPI empty mix format")
	}
	defer pCoTaskMemFree.Call(uintptr(format))

	hdr := unsafe.Slice((*byte)(format), 18)
	var ext []byte
	if binary.LittleEndian.Uint16(hdr[0:2]) == 0xfffe && binary.LittleEndian.Uint16(hdr[16:18]) >= 22 {
		ext = unsafe.Slice((*byte)(format), 40)
	}
	spec, err := parseWaveFormat(hdr, ext)
	if err != nil {
		s.closeDevice()
		return 0, err
	}
	s.rate, s.channels, s.bits, s.align, s.floating = spec.rate, spec.channels, spec.bits, spec.align, spec.floating

	var initClient func(*comObject, int32, uint32, int64, int64, unsafe.Pointer, uintptr) int32
	method(s.client, 3, &initClient)
	if code := initClient(s.client, 0, 0x20000, 1000000, 0, format, 0); code < 0 {
		s.closeDevice()
		return uint32(code), hr(code)
	}

	var service func(*comObject, *guid, **comObject) int32
	method(s.client, 14, &service)
	if code := service(s.client, &captureClientID, &s.capture); code < 0 {
		s.closeDevice()
		return uint32(code), hr(code)
	}

	// Cache hot-path COM capture methods once per opened endpoint.
	method(s.capture, 5, &s.fnGetNextPacketSize)
	method(s.capture, 3, &s.fnGetBuffer)
	method(s.capture, 4, &s.fnReleaseBuffer)

	var startClient func(*comObject) int32
	method(s.client, 10, &startClient)
	if code := startClient(s.client); code < 0 {
		s.closeDevice()
		return uint32(code), hr(code)
	}
	if len(s.ring) != 4096 {
		s.ring = make([]float32, 4096)
	} else {
		clear(s.ring)
	}
	s.retryBackoff = 0
	s.nextRetry = time.Time{}
	return 0, nil
}

func (s *winAudio) closeDevice() {
	if s.client != nil {
		var stopClient func(*comObject) int32
		method(s.client, 11, &stopClient)
		stopClient(s.client)
	}
	s.fnGetNextPacketSize = nil
	s.fnGetBuffer = nil
	s.fnReleaseBuffer = nil
	releaseCOM(s.capture)
	releaseCOM(s.client)
	s.capture = nil
	s.client = nil
}

func (s *winAudio) decaySilence(now time.Time) {
	if len(s.ring) > 0 && !s.lastData.IsZero() && now.Sub(s.lastData) > wasapiSilenceDecay {
		clear(s.ring)
	}
}

func (s *winAudio) readOnCOMThread(now time.Time) ([]float32, int, error) {
	if s.capture == nil {
		if now.Before(s.nextRetry) {
			s.decaySilence(now)
			if len(s.ring) == 0 || s.rate == 0 {
				return nil, 0, nil
			}
			return append([]float32(nil), s.ring...), s.rate, nil
		}
		if _, err := s.openDevice(); err != nil {
			s.scheduleReconnect(now)
			s.decaySilence(now)
			if len(s.ring) == 0 || s.rate == 0 {
				return nil, 0, nil
			}
			return append([]float32(nil), s.ring...), s.rate, nil
		}
	}

	any := false
	for packets := 0; packets < 256; packets++ {
		var count uint32
		if code := s.fnGetNextPacketSize(s.capture, &count); code < 0 {
			return s.handleReadError(uint32(code), now)
		}
		if count == 0 {
			break
		}
		var data unsafe.Pointer
		var frames, flags uint32
		if code := s.fnGetBuffer(s.capture, &data, &frames, &flags, 0, 0); code < 0 {
			return s.handleReadError(uint32(code), now)
		}
		if frames > uint32(s.rate) {
			s.fnReleaseBuffer(s.capture, frames)
			s.closeDevice()
			s.scheduleReconnect(now)
			return nil, 0, nil
		}
		var b []byte
		if flags&2 == 0 && data != nil {
			b = unsafe.Slice((*byte)(data), int(frames)*s.align)
		}
		if flags&1 != 0 {
			clear(s.ring)
		}
		for i := 0; i < int(frames); i++ {
			sum := 0.0
			if len(b) > 0 {
				for ch := 0; ch < s.channels; ch++ {
					off := i*s.align + ch*s.bits/8
					var sample float64
					if s.floating {
						sample = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[off:])))
					} else if s.bits == 16 {
						sample = float64(int16(binary.LittleEndian.Uint16(b[off:]))) / 32768.0
					} else {
						sample = float64(int32(binary.LittleEndian.Uint32(b[off:]))) / 2147483648.0
					}
					sum += sanitizeFloat64(sample)
				}
				sum /= float64(s.channels)
			}
			s.ring = append(s.ring, float32(sanitizeFloat64(sum)))
		}
		if len(s.ring) > 4096 {
			copy(s.ring, s.ring[len(s.ring)-4096:])
			clear(s.ring[4096:])
			s.ring = s.ring[:4096]
		}
		if code := s.fnReleaseBuffer(s.capture, frames); code < 0 {
			return s.handleReadError(uint32(code), now)
		}
		any = true
	}
	if any {
		s.lastData = now
	} else {
		// Match macOS: preserve ring across inter-packet gaps and zero only after >150ms silence.
		s.decaySilence(now)
	}
	return append([]float32(nil), s.ring...), s.rate, nil
}

func (s *winAudio) handleReadError(code uint32, now time.Time) ([]float32, int, error) {
	s.closeDevice()
	s.scheduleReconnect(now)
	s.decaySilence(now)
	if isTransientWASAPIError(code) {
		return nil, 0, nil
	}
	// Even for unexpected mid-stream COM errors (e.g. audio service restart),
	// reconnect with backoff rather than permanently disabling audio for the session.
	return nil, 0, nil
}
