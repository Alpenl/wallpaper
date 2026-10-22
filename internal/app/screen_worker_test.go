package app

import (
	"runtime"
	"testing"

	"wpapp/internal/visibility"
)

// Both rendition paths copy the entire screen on the event loop while the ops
// worker can be changing its acknowledged level. Exercise those copies with a
// continuously active worker so -race detects any inline worker-owned state.
func TestScreenCopiesWhileWorkerChangesLevel(t *testing.T) {
	e := setup(t, 1, true)
	started, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e.a.Do(func() {
		s := e.a.screens["A"]
		s.ops <- func() {
			defer close(done)
			close(started)
			for {
				select {
				case <-stop:
					return
				default:
					s.worker.level = visibility.L0
					s.worker.level = visibility.L1
					runtime.Gosched()
				}
			}
		}
	})
	<-started
	defer func() { close(stop); <-done }()
	for i := 0; i < 100; i++ {
		e.a.OptimizeDisplays("x")
		checked := make(chan struct{})
		e.a.Do(func() {
			defer close(checked)
			views := e.a.renditionViews()
			if len(views) != 1 || views[0].ItemID != "x" || views[0].DisplayID != "A" {
				t.Errorf("unexpected rendition views: %+v", views)
			}
		})
		<-checked
	}
}
