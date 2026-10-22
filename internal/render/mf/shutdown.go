package mf

import (
	"fmt"
	"time"
)

// The quit signal is irreversible. A timeout here is not an ordinary rejected
// setting: the engine can stop later and the caller must replace its session.
func waitForShutdown(renderDone, statsDone <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-renderDone:
	case <-timer.C:
		return fmt.Errorf("mf: render thread did not stop within %s", timeout)
	}
	select {
	case <-statsDone:
		return nil
	case <-timer.C:
		return fmt.Errorf("mf: statistics worker did not stop within %s", timeout)
	}
}
