//go:build windows

package win

import (
	"syscall"
	"unsafe"

	"wpapp/internal/desktop"
)

// foregroundExecutable requests only PROCESS_QUERY_LIMITED_INFORMATION. No
// process memory, window text, elevation, or long-lived process handle is used.
// A disappearing, protected, or otherwise inaccessible process clears the rule.
func foregroundExecutable(window uintptr) string {
	if window == 0 {
		return ""
	}
	var pid uint32
	if r, _, _ := pGetWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&pid))); r == 0 || pid == 0 {
		return ""
	}
	handle, _, _ := pOpenProcess.Call(0x1000 /*PROCESS_QUERY_LIMITED_INFORMATION*/, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(handle))
	var path [32768]uint16
	n := uint32(len(path))
	if r, _, _ := pQueryFullProcessImageName.Call(handle, 0, uintptr(unsafe.Pointer(&path[0])), uintptr(unsafe.Pointer(&n))); r == 0 || n == 0 || n > uint32(len(path)) {
		return ""
	}
	return desktop.ExecutableName(syscall.UTF16ToString(path[:n]))
}
