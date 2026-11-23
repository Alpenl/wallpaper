//go:build windows

package telemetry

import (
	"errors"
	"syscall"
)

const (
	winProcessQueryLimitedInformation = 0x1000
	winStillActive                    = 259
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(winProcessQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == winStillActive
}
