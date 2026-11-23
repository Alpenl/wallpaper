//go:build windows

package reactive

import (
	"encoding/binary"
	"testing"
	"time"
)

func makeWaveFormat(tag uint16, channels uint16, rate uint32, bits uint16, cbSize uint16, subformat *[16]byte) ([]byte, []byte) {
	align := channels * (bits / 8)
	hdr := make([]byte, 18)
	binary.LittleEndian.PutUint16(hdr[0:2], tag)
	binary.LittleEndian.PutUint16(hdr[2:4], channels)
	binary.LittleEndian.PutUint32(hdr[4:8], rate)
	binary.LittleEndian.PutUint32(hdr[8:12], rate*uint32(align))
	binary.LittleEndian.PutUint16(hdr[12:14], align)
	binary.LittleEndian.PutUint16(hdr[14:16], bits)
	binary.LittleEndian.PutUint16(hdr[16:18], cbSize)
	if subformat == nil {
		return hdr, nil
	}
	ext := make([]byte, 40)
	copy(ext, hdr)
	copy(ext[24:40], subformat[:])
	return hdr, ext
}

func TestParseWaveFormatFullGUID(t *testing.T) {
	hdr, ext := makeWaveFormat(0xfffe, 2, 48000, 32, 22, &subformatIEEEFloat)
	spec, err := parseWaveFormat(hdr, ext)
	if err != nil || !spec.floating || spec.rate != 48000 || spec.channels != 2 {
		t.Fatalf("IEEE_FLOAT extensible rejected: spec=%+v err=%v", spec, err)
	}

	hdr, ext = makeWaveFormat(0xfffe, 2, 48000, 16, 22, &subformatPCM)
	spec, err = parseWaveFormat(hdr, ext)
	if err != nil || spec.floating || spec.bits != 16 {
		t.Fatalf("PCM extensible rejected: spec=%+v err=%v", spec, err)
	}

	// Same leading 2 bytes as IEEE_FLOAT (0x0003) but different trailing GUID bytes must fail.
	badSub := subformatIEEEFloat
	badSub[15] ^= 0xff
	hdr, ext = makeWaveFormat(0xfffe, 2, 48000, 32, 22, &badSub)
	if _, err := parseWaveFormat(hdr, ext); err == nil {
		t.Fatal("spoofed SubFormat GUID with altered trailing bytes must be rejected")
	}
}

func TestWinAudioSilenceDecay(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := &winAudio{
		ring:     []float32{0.5, -0.5, 0.25},
		lastData: t0,
	}
	// Within 150ms inter-packet gap: ring must NOT be cleared.
	s.decaySilence(t0.Add(50 * time.Millisecond))
	if s.ring[0] != 0.5 || s.ring[1] != -0.5 {
		t.Fatalf("ring cleared prematurely during short packet gap: %v", s.ring)
	}
	// After >150ms silence: ring must be zeroed.
	s.decaySilence(t0.Add(200 * time.Millisecond))
	for i, v := range s.ring {
		if v != 0 {
			t.Fatalf("ring[%d] = %v after silence timeout, want 0", i, v)
		}
	}
}

func TestWinAudioTransientAndBackoff(t *testing.T) {
	if !isTransientWASAPIError(hrAudclntDeviceInvalidated) {
		t.Fatal("AUDCLNT_E_DEVICE_INVALIDATED must be transient")
	}
	if isTransientWASAPIError(0x80004005) {
		t.Fatal("E_FAIL should not be classified as transient")
	}
	s := &winAudio{}
	now := time.Unix(200, 0)
	s.scheduleReconnect(now)
	if s.retryBackoff != wasapiInitialReconnectBackoff {
		t.Fatalf("initial backoff = %v, want %v", s.retryBackoff, wasapiInitialReconnectBackoff)
	}
	for i := 0; i < 10; i++ {
		s.scheduleReconnect(now)
	}
	if s.retryBackoff != wasapiMaxReconnectBackoff {
		t.Fatalf("capped backoff = %v, want %v", s.retryBackoff, wasapiMaxReconnectBackoff)
	}
}
