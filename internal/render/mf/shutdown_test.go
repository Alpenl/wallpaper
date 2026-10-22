package mf

import (
	"strings"
	"testing"
	"time"
)

func TestShutdownCompletesOnlyAfterBothWorkers(t *testing.T) {
	renderDone, statsDone := make(chan struct{}), make(chan struct{})
	close(renderDone)
	close(statsDone)
	if err := waitForShutdown(renderDone, statsDone, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleTimeoutRequiresHostRestart(t *testing.T) {
	for _, phase := range []string{"render thread", "statistics worker"} {
		t.Run(phase, func(t *testing.T) {
			renderDone, statsDone := make(chan struct{}), make(chan struct{})
			if phase == "statistics worker" {
				close(renderDone)
			}
			err := waitForShutdown(renderDone, statsDone, time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), phase) {
				t.Fatalf("expected %s timeout, got %v", phase, err)
			}
			response := lifecycleFailure("enter L3", err)
			if !response.Fatal || !uncertainControlError(response) {
				t.Fatal("typed lifecycle rejection left a stopping host alive")
			}
			// Even if shutdown finishes after the timeout, the already-sent
			// fatal response must still require a new session, not old-level reuse.
			if phase == "render thread" {
				close(renderDone)
			}
			close(statsDone)
			if !uncertainControlError(response) {
				t.Fatal("late shutdown completion made old session reusable")
			}
		})
	}
}
