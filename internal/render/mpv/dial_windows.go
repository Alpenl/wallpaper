//go:build windows

package mpv

import (
	"io"
	"os/exec"
	"syscall"
	"time"

	"wpapp/internal/platform/win32"
)

func dial(name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	return win32.DialPipe(name, timeout)
}

// hideWindow：mpv.exe 是控制台子系统程序时不弹出控制台窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}
}
