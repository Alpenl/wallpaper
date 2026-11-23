//go:build !windows

package transcode

import (
	"context"
	"io"
	"os/exec"
	"time"
)

func hide(*exec.Cmd) {}

func runCommand(ctx context.Context, stdout, stderr io.Writer, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	hide(cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	return cmd.Run()
}
