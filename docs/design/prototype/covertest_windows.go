//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// runCoverTest：调试工具，开一个普通顶层窗口覆盖指定矩形，用于在没有其他应用的环境（Wine/Xvfb）里触发 Covered。
func runCoverTest() {
	var x, y, w, h int32
	fmt.Sscanf(*flagCoverTest, "%d,%d,%d,%d", &x, &y, &w, &h)
	hInst, _, _ = pGetModuleHandleW.Call(0)
	proc := syscall.NewCallback(func(hw, m, wp, lp uintptr) uintptr { r, _, _ := pDefWindowProcW.Call(hw, m, wp, lp); return r })
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: proc, Instance: hInst, ClassName: u16("WpProtoCoverTest")}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hw, _, _ := pCreateWindowExW.Call(0, up(u16("WpProtoCoverTest")), up(u16("cover")), wsPopup|wsVisible,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hInst, 0)
	logf("COVERTEST hwnd=0x%x rect=%d,%d %dx%d", hw, x, y, w, h)
	d := *flagDuration
	if d == 0 {
		d = 5 * time.Second
	}
	go func() { time.Sleep(d); pPostMessageW.Call(hw, 0x0012, 0, 0) }()
	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
