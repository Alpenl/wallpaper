//go:build windows && (amd64 || arm64)

package mf

import (
	"math"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// This is a target-runtime smoke test, not executed by Linux cross-compilation.
// Exercise the same RegisterFunc path with documented CRT functions containing
// doubles, integers and pointers. In particular arm64 must use FP registers,
// not syscall.SyscallN's integer argument slots.
func TestWindowsFloatArgumentAndReturnABI(t *testing.T) {
	dll := syscall.NewLazyDLL("ucrtbase.dll")
	ldexpProc, modfProc := dll.NewProc("ldexp"), dll.NewProc("modf")
	if err := ldexpProc.Find(); err != nil {
		t.Fatal(err)
	}
	if err := modfProc.Find(); err != nil {
		t.Fatal(err)
	}
	var ldexp func(float64, int32) float64
	var modf func(float64, *float64) float64
	purego.RegisterFunc(&ldexp, ldexpProc.Addr())
	purego.RegisterFunc(&modf, modfProc.Addr())
	if got := ldexp(1.25, 3); got != 10 {
		t.Fatalf("mixed double/integer ABI: %g", got)
	}
	var whole float64
	if frac := modf(12.75, &whole); frac != .75 || whole != 12 {
		t.Fatalf("double/pointer ABI: %g + %g", whole, frac)
	}
	if got := ldexp(-.5, -2); math.Abs(got+.125) > 1e-15 {
		t.Fatalf("signed float ABI: %g", got)
	}
}

func TestCOMMethodIndexLayout(t *testing.T) {
	// The SDK IMFMediaEngine vtable has 45 methods including IUnknown.
	if vtMEGetCurrentTime != 16 || vtMESetCurrentTime != 17 || vtMEGetDuration != 19 || vtMESetPlaybackRate != 24 || vtMEExIsPlaybackRateSupported != 50 {
		t.Fatal("Media Engine double methods do not match the Windows SDK vtable")
	}
	var r rect
	if unsafe.Sizeof(r) != 16 || unsafe.Offsetof(r.R) != 8 {
		t.Fatal("RECT ABI mismatch")
	}
}
