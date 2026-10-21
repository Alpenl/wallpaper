package main

import (
	"syscall"
	"unsafe"

	
)

var (
	u32              = syscall.NewLazyDLL("user32.dll")
	pFindWindowW     = u32.NewProc("FindWindowW")
	pFindWindowExW   = u32.NewProc("FindWindowExW")
	pSendMsgTimeout  = u32.NewProc("SendMessageTimeoutW")
	pEnumWindows     = u32.NewProc("EnumWindows")
	pSetParent       = u32.NewProc("SetParent")
	pGetWindowLongPtr = u32.NewProc("GetWindowLongPtrW")
)

func w(s string) uintptr { p, _ := syscall.UTF16PtrFromString(s); return uintptr(unsafe.Pointer(p)) }

var workerw uintptr
var enumCB = syscall.NewCallback(func(top, _ uintptr) uintptr {
	if p, _, _ := pFindWindowExW.Call(top, 0, w("SHELLDLL_DefView"), 0); p != 0 {
		workerw, _, _ = pFindWindowExW.Call(0, top, w("WorkerW"), 0)
	}
	return 1
})

func attachDesktop(hwnd uintptr) {
	progman, _, _ := pFindWindowW.Call(w("Progman"), 0)
	var res uintptr
	pSendMsgTimeout.Call(progman, 0x052C, 0xD, 0x1, 0, 1000, uintptr(unsafe.Pointer(&res)))
	ex, _, _ := pGetWindowLongPtr.Call(progman, ^uintptr(19)) // GWL_EXSTYLE = -20
	if ex&0x00200000 != 0 { // 24H2 raised desktop
		workerw, _, _ = pFindWindowExW.Call(progman, 0, w("WorkerW"), 0)
	} else {
		pEnumWindows.Call(enumCB, 0)
	}
	pSetParent.Call(hwnd, workerw)
}
