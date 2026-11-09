//go:build darwin

package main

import (
	"fmt"

	"github.com/ebitengine/purego"
)

func permissionCheck() error {
	h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	var allowed func() bool
	purego.RegisterLibFunc(&allowed, h, "CGPreflightScreenCaptureAccess")
	if !allowed() {
		return fmt.Errorf("BLOCKED: screen recording permission unavailable; grant Terminal screen recording access")
	}
	return nil
}
func windowGone(window uintptr) error {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	var list func(uint32, uint32) uintptr
	var count func(uintptr) int
	var release func(uintptr)
	purego.RegisterLibFunc(&list, cg, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&count, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	a := list(8, uint32(window))
	if a == 0 {
		return fmt.Errorf("BLOCKED: cannot query native window list")
	}
	defer release(a)
	if count(a) > 0 {
		return fmt.Errorf("wallpaper window still exists")
	}
	return nil
}
