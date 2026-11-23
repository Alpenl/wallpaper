//go:build windows

package transcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestJobHelperProcess(t *testing.T) {
	role := os.Getenv("TRANSCODE_JOB_TEST")
	switch role {
	case "":
		return
	case "shim":
		exe, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		cmd := exec.Command(exe, "-test.run=^TestJobHelperProcess$")
		cmd.Env = append(os.Environ(), "TRANSCODE_JOB_TEST=grandchild")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(3)
		}
		select {}
	case "grandchild":
		fmt.Printf("GRANDCHILD_PID=%d\n", os.Getpid())
		select {}
	}
}

// M-06: Cancelling a Windows transcode started via a shim must immediately
// terminate the grandchild process via the KILL_ON_JOB_CLOSE Job Object.
func TestWindowsJobObjectKillsGrandchildTree(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRANSCODE_JOB_TEST", "shim")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		err := runCommand(ctx, pw, pw, exe, "-test.run=^TestJobHelperProcess$")
		_ = pw.Close()
		runDone <- err
	}()

	pidCh := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			if !sent {
				line := strings.TrimSpace(sc.Text())
				if rest, ok := strings.CutPrefix(line, "GRANDCHILD_PID="); ok {
					if pid, err := strconv.Atoi(rest); err == nil {
						pidCh <- pid
						sent = true
					}
				}
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	var grandchildPID int
	select {
	case grandchildPID = <-pidCh:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("timed out waiting for grandchild PID")
	}

	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(grandchildPID))
	if err != nil {
		cancel()
		t.Fatalf("OpenProcess(%d): %v", grandchildPID, err)
	}
	defer syscall.CloseHandle(h)

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runCommand did not return promptly after cancel")
	}

	ev, err := syscall.WaitForSingleObject(h, 3000)
	if err != nil || ev != syscall.WAIT_OBJECT_0 {
		t.Fatalf("grandchild process %d survived Job Object termination (ev=%d, err=%v)", grandchildPID, ev, err)
	}
}
