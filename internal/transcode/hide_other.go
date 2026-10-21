//go:build !windows

package transcode

import "os/exec"

func hide(*exec.Cmd) {}
