package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"wpapp/internal/wescene"
)

func TestSceneProbeChild(t *testing.T) {
	mode := os.Getenv("WP_SCENEPROBE_TEST")
	if mode == "" {
		return
	}
	r := wescene.Probe("missing.pkg")
	b, _ := json.Marshal(r)
	switch mode {
	case "valid":
		fmt.Print(string(b))
	case "boundary":
		// JSON 尾部空白仍是合法报告，让两路输出恰好达到各自预算。
		fmt.Print(string(b) + strings.Repeat(" ", wescene.MaxReportBytes-len(b)))
		fmt.Fprint(os.Stderr, strings.Repeat("x", 16<<10))
	case "timeout":
		// sleep: 模拟挂起的子进程，由父进程的期限终止并回收。
		time.Sleep(time.Minute)
	case "stdout", "stdout-hang":
		fmt.Print(string(b) + strings.Repeat(" ", wescene.MaxReportBytes+1-len(b)))
	case "stderr", "stderr-hang":
		fmt.Print(string(b))
		fmt.Fprint(os.Stderr, strings.Repeat("x", (16<<10)+1))
	case "invalid":
		fmt.Print("not json")
	case "multiple":
		fmt.Print(string(b) + string(b))
	case "abnormal":
		fmt.Print(string(b))
		os.Exit(7)
	}
	if strings.HasSuffix(mode, "-hang") {
		// sleep: 超限后保持存活，验证预算会在父进程的 5 秒期限前终止它。
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
func TestSuperviseSceneProbe(t *testing.T) {
	for _, mode := range []string{"valid", "boundary", "timeout", "stdout", "stderr", "stdout-hang", "stderr-hang", "invalid", "multiple", "abnormal"} {
		t.Run(mode, func(t *testing.T) {
			duration := 5 * time.Second
			if mode == "timeout" {
				duration = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSceneProbeChild$")
			cmd.Env = append(os.Environ(), "WP_SCENEPROBE_TEST="+mode)
			r, err := superviseSceneProbe(cmd)
			if cmd.ProcessState == nil {
				t.Fatal("child was not reaped")
			}
			switch mode {
			case "valid", "boundary":
				if err != nil || r.Status != "failed" {
					t.Fatalf("%+v %v", r, err)
				}
			case "stdout", "stderr", "stdout-hang", "stderr-hang":
				if err == nil || !strings.Contains(err.Error(), "exceeded budget") {
					t.Errorf("expected output budget error, got %v", err)
				}
			default:
				if err == nil {
					t.Fatal("accepted failed helper")
				}
			}
			if strings.HasSuffix(mode, "-hang") && (ctx.Err() != nil || cmd.ProcessState.Success()) {
				t.Errorf("overflow did not kill child before deadline: context=%v state=%v", ctx.Err(), cmd.ProcessState)
			}
			if mode == "timeout" && ctx.Err() != context.DeadlineExceeded {
				t.Fatal("deadline did not expire")
			}
		})
	}
}

func TestProbeOutputCopyPipe(t *testing.T) {
	for _, stream := range []struct {
		name  string
		limit int
	}{
		{"stdout", wescene.MaxReportBytes},
		{"stderr", 16 << 10},
	} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/extra=%d", stream.name, extra), func(t *testing.T) {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				writeDone := make(chan error, 1)
				go func() {
					_, writeErr := io.WriteString(writer, strings.Repeat("x", stream.limit+extra))
					writeDone <- errors.Join(writeErr, writer.Close())
				}()
				kills := 0
				out := &probeOutput{limit: stream.limit, kill: func() { kills++ }}
				// 直接传入真实管道，保留 io.Copy 的可选接口分派路径。
				_, copyErr := io.Copy(out, reader)
				// 超限可能提前结束读取；关闭读端并等待生产者，避免遗留阻塞。
				_ = reader.Close()
				writeErr := <-writeDone
				if extra == 0 {
					if copyErr != nil || writeErr != nil {
						t.Fatalf("boundary copy failed: copy=%v write=%v", copyErr, writeErr)
					}
				} else if !errors.Is(copyErr, io.ErrShortWrite) {
					t.Errorf("expected budget write error, got %v", copyErr)
				}
				if out.overflow != (extra != 0) || kills != extra {
					t.Errorf("overflow=%v kills=%d, want overflow=%v kills=%d", out.overflow, kills, extra != 0, extra)
				}
				if out.buffer.Len() != stream.limit {
					t.Errorf("buffer has %d bytes, want %d", out.buffer.Len(), stream.limit)
				}
			})
		}
	}
}
