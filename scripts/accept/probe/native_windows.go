//go:build windows

package main

import (
	"fmt"
	"syscall"
)

func permissionCheck() error { return nil }
func windowGone(window uintptr) error {
	r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("IsWindow").Call(window)
	if r != 0 {
		return fmt.Errorf("wallpaper window still exists")
	}
	return nil
}
