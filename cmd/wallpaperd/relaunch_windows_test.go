//go:build windows

package main

import (
	"slices"
	"testing"
)

func TestRelaunchCmdSetsBreakawayFromJobOnWindows(t *testing.T) {
	cmd := relaunchCmd("wallpaperd.exe", []string{"-debug"})
	if cmd.SysProcAttr == nil {
		t.Fatal("expected SysProcAttr to be set on Windows")
	}
	if cmd.SysProcAttr.CreationFlags&createBreakawayFromJob == 0 {
		t.Fatalf("expected CREATE_BREAKAWAY_FROM_JOB (0x%x), got 0x%x", createBreakawayFromJob, cmd.SysProcAttr.CreationFlags)
	}
	if !slices.Contains(cmd.Env, "WPAPP_RELAUNCH_WAIT=1") {
		t.Fatal("expected WPAPP_RELAUNCH_WAIT=1 in relaunch env")
	}
}
