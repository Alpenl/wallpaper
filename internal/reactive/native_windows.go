//go:build windows

package reactive

import (
	"encoding/binary"
	"fmt"
	"github.com/ebitengine/purego"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var ole32 = syscall.NewLazyDLL("ole32.dll")

type winPointer struct{}

func newPointer() Pointer        { return &winPointer{} }
func (*winPointer) Start() error { return nil }
func (*winPointer) Stop()        {}
func (*winPointer) Sample() PointerSample {
	var p struct{ X, Y int32 }
	ok, _, _ := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return PointerSample{}
	}
	// WindowFromPoint identifies the actual hit window, including windows too
	// small to trigger the whole-display coverage policy. Never install a hook.
	packed := uintptr(uint64(uint32(p.X)) | uint64(uint32(p.Y))<<32)
	w, _, _ := user32.NewProc("WindowFromPoint").Call(packed)
	w, _, _ = user32.NewProc("GetAncestor").Call(w, 2) // GA_ROOT
	var name [256]uint16
	user32.NewProc("GetClassNameW").Call(w, uintptr(unsafe.Pointer(&name[0])), 256)
	class := syscall.UTF16ToString(name[:])
	desktop := class == "Progman" || class == "WorkerW"
	var buttons uint32
	if desktop {
		for i, k := range []uintptr{1, 2, 4} {
			b, _, _ := user32.NewProc("GetAsyncKeyState").Call(k)
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

var enumeratorClass = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
var enumeratorID = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
var audioClientID = guid{0x1cb9ad4c, 0xdbfa, 0x4c32, [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
var captureClientID = guid{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}

func method[T any](p uintptr, index int, fn *T) {
	v := *(*uintptr)(unsafe.Pointer(p))
	purego.RegisterFunc(fn, *(*uintptr)(unsafe.Pointer(v + uintptr(index)*unsafe.Sizeof(p))))
}
func releaseCOM(p uintptr) {
	if p != 0 {
		var f func(uintptr) uint32
		method(p, 2, &f)
		f(p)
	}
}
func hr(r int32) error {
	if r < 0 {
		return fmt.Errorf("WASAPI unsupported/unavailable: HRESULT 0x%08x", uint32(r))
	}
	return nil
}

type winAudio struct {
	client, capture             uintptr
	initialized                 bool
	rate, channels, bits, align int
	floating                    bool
	ring                        []float32
}

func newAudio() Source { return &winAudio{} }
func (s *winAudio) Start() (err error) {
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()
	r, _, _ := ole32.NewProc("CoInitializeEx").Call(0, 0)
	if int32(r) >= 0 {
		s.initialized = true
	} else if uint32(r) != 0x80010106 {
		return hr(int32(r))
	}
	var en, dev uintptr
	var create func(*guid, uintptr, uint32, *guid, *uintptr) int32
	purego.RegisterFunc(&create, ole32.NewProc("CoCreateInstance").Addr())
	if err = hr(create(&enumeratorClass, 0, 1, &enumeratorID, &en)); err != nil {
		return
	}
	defer releaseCOM(en)
	var endpoint func(uintptr, int32, int32, *uintptr) int32
	method(en, 4, &endpoint)
	if err = hr(endpoint(en, 0, 1, &dev)); err != nil {
		return
	}
	defer releaseCOM(dev)
	var activate func(uintptr, *guid, uint32, uintptr, *uintptr) int32
	method(dev, 3, &activate)
	if err = hr(activate(dev, &audioClientID, 23, 0, &s.client)); err != nil {
		return
	}
	var format uintptr
	var mix func(uintptr, *uintptr) int32
	method(s.client, 8, &mix)
	if err = hr(mix(s.client, &format)); err != nil {
		return
	}
	defer ole32.NewProc("CoTaskMemFree").Call(format)
	b := unsafe.Slice((*byte)(unsafe.Pointer(format)), 18)
	tag := binary.LittleEndian.Uint16(b)
	s.channels = int(binary.LittleEndian.Uint16(b[2:]))
	s.rate = int(binary.LittleEndian.Uint32(b[4:]))
	s.align = int(binary.LittleEndian.Uint16(b[12:]))
	s.bits = int(binary.LittleEndian.Uint16(b[14:]))
	s.floating = tag == 3
	if tag == 0xfffe && binary.LittleEndian.Uint16(b[16:]) >= 22 {
		ext := unsafe.Slice((*byte)(unsafe.Pointer(format)), 40)
		tag = uint16(binary.LittleEndian.Uint32(ext[24:]))
		s.floating = tag == 3
	}
	if s.channels < 1 || s.channels > 32 || s.rate < 8000 || s.rate > 192000 || s.align != s.channels*(s.bits/8) || !(s.floating && s.bits == 32 || tag == 1 && (s.bits == 16 || s.bits == 32)) {
		return fmt.Errorf("WASAPI unsupported mix format")
	}
	var init func(uintptr, int32, uint32, int64, int64, uintptr, uintptr) int32
	method(s.client, 3, &init)
	if err = hr(init(s.client, 0, 0x20000, 1000000, 0, format, 0)); err != nil {
		return
	}
	var service func(uintptr, *guid, *uintptr) int32
	method(s.client, 14, &service)
	if err = hr(service(s.client, &captureClientID, &s.capture)); err != nil {
		return
	}
	var start func(uintptr) int32
	method(s.client, 10, &start)
	err = hr(start(s.client))
	s.ring = make([]float32, 4096)
	return
}
func (s *winAudio) Stop() {
	if s.client != 0 {
		var stop func(uintptr) int32
		method(s.client, 11, &stop)
		stop(s.client)
	}
	releaseCOM(s.capture)
	releaseCOM(s.client)
	s.capture = 0
	s.client = 0
	clear(s.ring[:cap(s.ring)])
	s.ring = nil
	if s.initialized {
		ole32.NewProc("CoUninitialize").Call()
		s.initialized = false
	}
}
func (s *winAudio) Read() ([]float32, int, error) {
	var next func(uintptr, *uint32) int32
	method(s.capture, 5, &next)
	var get func(uintptr, *uintptr, *uint32, *uint32, uintptr, uintptr) int32
	method(s.capture, 3, &get)
	var release func(uintptr, uint32) int32
	method(s.capture, 4, &release)
	any := false
	for packets := 0; packets < 256; packets++ {
		var count uint32
		if e := hr(next(s.capture, &count)); e != nil {
			return nil, 0, e
		}
		if count == 0 {
			break
		}
		var data uintptr
		var frames, flags uint32
		if e := hr(get(s.capture, &data, &frames, &flags, 0, 0)); e != nil {
			return nil, 0, e
		}
		if frames > uint32(s.rate) {
			release(s.capture, frames)
			return nil, 0, fmt.Errorf("WASAPI oversized packet")
		}
		var b []byte
		if flags&2 == 0 && data != 0 {
			b = unsafe.Slice((*byte)(unsafe.Pointer(data)), int(frames)*s.align)
		}
		if flags&1 != 0 {
			clear(s.ring)
		}
		for i := 0; i < int(frames); i++ {
			v := float32(0)
			if len(b) > 0 {
				for ch := 0; ch < s.channels; ch++ {
					off := i*s.align + ch*s.bits/8
					if s.floating {
						v += math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
					} else if s.bits == 16 {
						v += float32(int16(binary.LittleEndian.Uint16(b[off:]))) / 32768
					} else {
						v += float32(int32(binary.LittleEndian.Uint32(b[off:]))) / 2147483648
					}
				}
				v /= float32(s.channels)
			}
			s.ring = append(s.ring, v)
		}
		if len(s.ring) > 4096 {
			copy(s.ring, s.ring[len(s.ring)-4096:])
			clear(s.ring[4096:])
			s.ring = s.ring[:4096]
		}
		if e := hr(release(s.capture, frames)); e != nil {
			return nil, 0, e
		}
		any = true
	}
	if !any {
		clear(s.ring)
	}
	runtime.KeepAlive(s)
	return s.ring, s.rate, nil
}
