//go:build !windows

package main

import "os/exec"

func configureRelaunch(*exec.Cmd) {}
