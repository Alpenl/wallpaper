//go:build !linux && !windows

package mpv

import "os/exec"

func configureChildProc(*exec.Cmd) {}
