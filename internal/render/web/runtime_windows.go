//go:build windows

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// The official loader is preferred. Otherwise use the installed Evergreen
// runtime's client entry point, as the existing MyGo pure-Go backend does.
func runtimeCreate() (func(string, uintptr) uintptr, error) {
	exe, _ := os.Executable()
	dll := syscall.NewLazyDLL(filepath.Join(filepath.Dir(exe), "WebView2Loader.dll"))
	create := dll.NewProc("CreateCoreWebView2EnvironmentWithOptions")
	if create.Find() == nil {
		version := dll.NewProc("GetAvailableCoreWebView2BrowserVersionString")
		var v uintptr
		if version.Find() != nil {
			return nil, fmt.Errorf("WebView2Loader.dll is incomplete")
		}
		r, _, _ := version.Call(0, ptr(&v))
		if hr(r) != nil || v == 0 {
			return nil, fmt.Errorf("web capability unavailable: Microsoft Edge WebView2 Runtime is missing")
		}
		ole.NewProc("CoTaskMemFree").Call(v)
		return func(dir string, h uintptr) uintptr { r, _, _ := create.Call(0, wide(dir), 0, h); return r }, nil
	}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	if runtime.GOARCH == "386" {
		arch = "x86"
	}
	for _, machine := range []bool{false, true} {
		root := uintptr(0x80000001)
		if machine {
			root = 0x80000002
		}
		for _, base := range []string{`SOFTWARE\Microsoft\EdgeUpdate\ClientState\`, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\ClientState\`} {
			dir := registryString(root, base+`{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`, "EBWebView")
			if dir == "" {
				continue
			}
			if _, e := os.Stat(filepath.Join(dir, "msedgewebview2.exe")); e != nil {
				continue
			}
			proc := syscall.NewLazyDLL(filepath.Join(dir, "EBWebView", arch, "EmbeddedBrowserWebView.dll")).NewProc("CreateWebViewEnvironmentWithOptionsInternal")
			if proc.Find() != nil {
				continue
			}
			return func(dir string, h uintptr) uintptr { r, _, _ := proc.Call(1, 0, wide(dir), 0, h); return r }, nil
		}
	}
	return nil, fmt.Errorf("web capability unavailable: install Microsoft Edge WebView2 Runtime")
}
func registryString(root uintptr, key, name string) string {
	adv := syscall.NewLazyDLL("advapi32.dll")
	var h uintptr
	r, _, _ := adv.NewProc("RegOpenKeyExW").Call(root, wide(key), 0, 0x20019, ptr(&h))
	if r != 0 {
		return ""
	}
	defer adv.NewProc("RegCloseKey").Call(h)
	var kind uint32
	var buf [32768]uint16
	n := uint32(len(buf) * 2)
	r, _, _ = adv.NewProc("RegQueryValueExW").Call(h, wide(name), 0, ptr(&kind), uintptr(unsafe.Pointer(&buf[0])), ptr(&n))
	if r != 0 || kind != 1 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}
