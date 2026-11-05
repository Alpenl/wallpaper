package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"time"

	"wpapp/internal/wescene"
)

func runSceneProbeHelper(input string) int {
	// A soft Go heap limit plus parser budgets, not an OS memory limit or sandbox.
	debug.SetMemoryLimit(128 << 20)
	r := wescene.ProbeInput(input)
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "write scene report:", err)
		return 3
	}
	// A failed probe still transports a valid report. The supervising CLI maps its
	// status to a nonzero exit; an abnormal helper exit is a transport failure.
	return 0
}
func runSceneProbe(input string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-helper", "sceneprobe", "-in", input)
	hideConsole(cmd)
	r, err := superviseSceneProbe(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if r.Status == "failed" {
		return 3
	}
	return 0
}

type probeOutput struct {
	// 使用具名字段，避免 ReadFrom 提升后绕过 Write 的预算检查。
	buffer   bytes.Buffer
	limit    int
	overflow bool
	kill     func()
}

func (w *probeOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := w.limit - w.buffer.Len()
	if n > left {
		_, _ = w.buffer.Write(p[:left])
		w.overflow = true
		w.kill()
		return n, io.ErrShortWrite
	}
	return w.buffer.Write(p)
}

// exec 负责等待 stdout/stderr 的复制协程，两路输出使用独立的有界缓冲区。
// 超限立即终止子进程，Run 始终等待并回收它。
func superviseSceneProbe(cmd *exec.Cmd) (wescene.Report, error) {
	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	out := &probeOutput{limit: wescene.MaxReportBytes, kill: kill}
	stderr := &probeOutput{limit: 16 << 10, kill: kill}
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if out.overflow || stderr.overflow {
		return wescene.Report{}, fmt.Errorf("sceneprobe helper output exceeded budget")
	}
	if err != nil {
		return wescene.Report{}, fmt.Errorf("sceneprobe helper failed: %w", err)
	}
	return wescene.ValidateReport(out.buffer.Bytes())
}
