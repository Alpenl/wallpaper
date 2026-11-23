//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const createBreakawayFromJob = 0x01000000

func configureRelaunch(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createBreakawayFromJob
}
