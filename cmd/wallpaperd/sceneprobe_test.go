package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	case "timeout":
		// sleep: simulate a hung child; the parent's deadline must kill and reap it.
		time.Sleep(time.Minute)
	case "stdout":
		fmt.Print(strings.Repeat("x", wescene.MaxReportBytes+1))
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", (16<<10)+1))
	case "invalid":
		fmt.Print("not json")
	case "multiple":
		fmt.Print(string(b) + string(b))
	case "abnormal":
		fmt.Print(string(b))
		os.Exit(7)
	}
	os.Exit(0)
}
func TestSuperviseSceneProbe(t *testing.T) {
	for _, mode := range []string{"valid", "timeout", "stdout", "stderr", "invalid", "multiple", "abnormal"} {
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
			if mode == "valid" {
				if err != nil || r.Status != "failed" {
					t.Fatalf("%+v %v", r, err)
				}
			} else if err == nil {
				t.Fatal("accepted failed helper")
			}
			if cmd.ProcessState == nil {
				t.Fatal("child was not reaped")
			}
			if mode == "timeout" && ctx.Err() != context.DeadlineExceeded {
				t.Fatal("deadline did not expire")
			}
		})
	}
}
