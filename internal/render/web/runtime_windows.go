//go:build windows

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	iidEnvironmentOptions = syscall.GUID{Data1: 0x2fde08a8, Data2: 0x1e9a, Data3: 0x4766, Data4: [8]byte{0x8c, 0x05, 0x95, 0xa9, 0xce, 0xb9, 0xd1, 0xc5}}
	targetBrowserVersion  = utf16Slice("86.0.616.0")

	envOptionsVtbl [11]uintptr
	envOptionsOnce sync.Once
	envOptionsMu   sync.Mutex
	envOptionsLive = map[uintptr]*envOptions{}
)

type envOptions struct {
	vtbl *[11]uintptr
	refs int32
	args []uint16
}

func utf16Slice(s string) []uint16 {
	u, _ := syscall.UTF16FromString(s)
	return u
}

func coTaskString(s []uint16) uintptr {
	if len(s) == 0 {
		return 0
	}
	p, _, _ := ole.NewProc("CoTaskMemAlloc").Call(uintptr(len(s) * 2))
	if p == 0 {
		return 0
	}
	copy(unsafe.Slice((*uint16)(rawPtr(p)), len(s)), s)
	return p
}

func envOptionsOf(this uintptr) *envOptions {
	envOptionsMu.Lock()
	defer envOptionsMu.Unlock()
	return envOptionsLive[this]
}

func envOptionsRef(this uintptr, delta int32) int32 {
	envOptionsMu.Lock()
	defer envOptionsMu.Unlock()
	o := envOptionsLive[this]
	if o == nil {
		return 0
	}
	o.refs += delta
	if o.refs <= 0 {
		delete(envOptionsLive, this)
		return 0
	}
	return o.refs
}

// newEnvOptions creates an ICoreWebView2EnvironmentOptions COM object carrying
// AdditionalBrowserArguments. The caller owns the initial reference.
func newEnvOptions(args string) uintptr {
	envOptionsOnce.Do(func() {
		strGetter := func(get func(*envOptions) []uint16) uintptr {
			return syscall.NewCallback(func(this, out uintptr) uintptr {
				if out == 0 {
					return 0x80004003 // E_POINTER
				}
				*(*uintptr)(rawPtr(out)) = coTaskString(get(envOptionsOf(this)))
				return 0
			})
		}
		ignore := syscall.NewCallback(func(this, value uintptr) uintptr { return 0 })
		envOptionsVtbl = [11]uintptr{
			syscall.NewCallback(func(this, riid, out uintptr) uintptr {
				if out == 0 {
					return 0x80004003 // E_POINTER
				}
				g := *(*syscall.GUID)(rawPtr(riid))
				if g != iidIUnknown && g != iidEnvironmentOptions {
					*(*uintptr)(rawPtr(out)) = 0
					return 0x80004002 // E_NOINTERFACE
				}
				*(*uintptr)(rawPtr(out)) = this
				envOptionsRef(this, 1)
				return 0
			}),
			syscall.NewCallback(func(this uintptr) uintptr { return uintptr(envOptionsRef(this, 1)) }),
			syscall.NewCallback(func(this uintptr) uintptr { return uintptr(envOptionsRef(this, -1)) }),
			strGetter(func(o *envOptions) []uint16 {
				if o == nil {
					return nil
				}
				return o.args
			}),
			ignore, // put_AdditionalBrowserArguments
			strGetter(func(*envOptions) []uint16 { return nil }), // get_Language
			ignore, // put_Language
			strGetter(func(*envOptions) []uint16 { return targetBrowserVersion }), // get_TargetCompatibleBrowserVersion
			ignore, // put_TargetCompatibleBrowserVersion
			syscall.NewCallback(func(this, out uintptr) uintptr {
				if out == 0 {
					return 0x80004003 // E_POINTER
				}
				*(*int32)(rawPtr(out)) = 0
				return 0
			}),
			ignore, // put_AllowSingleSignOnUsingOSPrimaryAccount
		}
	})
	o := &envOptions{vtbl: &envOptionsVtbl, refs: 1}
	if args != "" {
		o.args = utf16Slice(args)
	}
	p := ptr(o)
	envOptionsMu.Lock()
	envOptionsLive[p] = o
	envOptionsMu.Unlock()
	return p
}

// The official loader is preferred. Otherwise use the installed Evergreen
// runtime's client entry point, as the existing MyGo pure-Go backend does.
func runtimeCreate() (func(string, uintptr, uintptr) uintptr, error) {
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
		return func(dir string, opts, h uintptr) uintptr { r, _, _ := create.Call(0, wide(dir), opts, h); return r }, nil
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
			return func(dir string, opts, h uintptr) uintptr { r, _, _ := proc.Call(1, 0, wide(dir), opts, h); return r }, nil
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
