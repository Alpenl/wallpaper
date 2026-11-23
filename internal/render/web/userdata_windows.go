//go:build windows

package web

import (
	"syscall"
	"unsafe"
)

var (
	kernel32DLL        = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess    = kernel32DLL.NewProc("OpenProcess")
	procGetExitCode    = kernel32DLL.NewProc("GetExitCodeProcess")
	procCreateEventW   = kernel32DLL.NewProc("CreateEventW")
	procSetEvent       = kernel32DLL.NewProc("SetEvent")
	procCloseHandle    = kernel32DLL.NewProc("CloseHandle")
	procMsgWaitObjects = user.NewProc("MsgWaitForMultipleObjects")
)

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	defer procCloseHandle.Call(h)
	var code uint32
	r, _, _ := procGetExitCode.Call(h, uintptr(unsafe.Pointer(&code)))
	return r != 0 && code == 259 // STILL_ACTIVE
}
