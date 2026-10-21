//go:build !windows

package mpv

import (
	"io"
	"net"
	"os/exec"
	"time"
)

func dial(name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.Dial("unix", name)
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func hideWindow(*exec.Cmd) {}
