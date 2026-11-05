package app

import (
	"reflect"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func TestScreenOpsCoalescesWithoutCrossingBarrier(t *testing.T) {
	ops := newScreenOps()
	var got []int
	for value := 0; value < 1000; value++ {
		ops.replaceLast("fps", func() { got = append(got, value) })
	}
	ops.enqueue(func() { got = append(got, -1) })
	ops.replaceLast("fps", func() { got = append(got, 1000) })
	quit := make(chan struct{})
	close(quit)
	go ops.run(quit)
	<-ops.done
	if !reflect.DeepEqual(got, []int{999, -1, 1000}) {
		t.Fatalf("operation order: %v", got)
	}
}

func TestScreenOpsPreservesQueuedCleanupOrder(t *testing.T) {
	ops := newScreenOps()
	entered, release, quit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ops.enqueue(func() { close(entered); <-release })
	go ops.run(quit)
	<-entered
	var got []int
	for value := 0; value < 256; value++ {
		ops.enqueue(func() { got = append(got, value) })
	}
	close(quit)
	close(release)
	select {
	case <-ops.done:
	case <-time.After(time.Second):
		t.Fatal("worker failed to drain cleanup operations")
	}
	if len(got) != 256 {
		t.Fatalf("drained %d operations", len(got))
	}
	for index, value := range got {
		if index != value {
			t.Fatalf("operation %d executed at %d", value, index)
		}
	}
}

func TestScreenOpsOverloadKeepsCleanupAndBarrier(t *testing.T) {
	ops := newScreenOps()
	for index := 0; index < maxScreenOps*10; index++ {
		ops.enqueue(func() {})
	}
	if ops.accepting() || len(ops.pending) != maxScreenOps {
		t.Fatalf("overload was not bounded: %d", len(ops.pending))
	}
	cleaned := false
	ops.cleanup(func() { cleaned = true })
	barrier := ops.barrier()
	if barrier != ops.barrier() {
		t.Fatal("barriers allocated additional queue entries")
	}
	quit := make(chan struct{})
	close(quit)
	go ops.run(quit)
	<-barrier
	if !cleaned {
		t.Fatal("cleanup did not run before barrier")
	}
	<-ops.done
}

func TestShutdownUnblocksFullCallbackQueue(t *testing.T) {
	application := &App{post: make(chan func(), 1), closing: make(chan struct{}), stopped: make(chan struct{})}
	application.post <- func() {}
	returned := make(chan struct{})
	go func() { application.Do(func() {}); close(returned) }()
	close(application.closing)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("callback remained blocked during shutdown")
	}
}

func TestShutdownDrainsWorkerWithFullCallbackQueue(t *testing.T) {
	e := setup(t, 1, true)
	entered, release := make(chan struct{}), make(chan struct{})
	e.a.Do(func() {
		e.a.screens["A"].ops.enqueue(func() {
			close(entered)
			<-release
			e.a.Do(func() {})
		})
	})
	<-entered
	finished := make(chan struct{})
	e.a.Do(func() {
		for len(e.a.post) < cap(e.a.post) {
			e.a.post <- func() {}
		}
		close(release)
		e.a.shutdown()
		select {
		case <-e.a.screens["A"].ops.done:
		default:
			t.Error("shutdown timed out instead of draining the worker")
		}
		delete(e.a.screens, "A")
		close(finished)
	})
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestOverloadRetiresOnlyAffectedScreen(t *testing.T) {
	e := setup(t, 2, true)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	e.a.Do(func() { e.a.screens["A"].ops.enqueue(func() { close(entered); <-release }) })
	<-entered
	e.a.Do(func() {
		screen := e.a.screens["A"]
		for index := 0; index < maxScreenOps*10; index++ {
			screen.ops.enqueue(func() {})
		}
	})
	eventually(t, "affected screen retired", func() bool {
		view := e.a.View()
		return len(view.Displays) == 2 && view.LastError != "" && view.Displays[1].Backend != ""
	})
	checked := make(chan struct{})
	e.a.Do(func() {
		screen := e.a.screens["A"]
		if screen.r != nil || !screen.levelExhausted || !e.a.screens["B"].ops.accepting() {
			t.Error("overload not isolated")
		}
		for index := 0; index < 1000; index++ {
			e.a.closeScreen(screen, false)
			e.a.startScreen(screen, 0)
		}
		if len(screen.ops.pending) > maxScreenOps+1 {
			t.Errorf("cleanup grew after overload: %d", len(screen.ops.pending))
		}
		close(checked)
	})
	<-checked
}

func TestBlockedWorkerDoesNotBlockEventLoop(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "renderer started", func() bool {
		return len(e.a.View().Displays) == 1 && e.a.View().Displays[0].Backend != ""
	})
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	e.a.Do(func() {
		e.a.screens["A"].ops.enqueue(func() { close(entered); <-release })
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	finished := make(chan struct{})
	e.a.Do(func() {
		s := e.a.screens["A"]
		for index := 0; index < 1000; index++ {
			s.machCap = 15 + index%2
			e.a.applyCap(s)
		}
		old, generation := s.r, s.gen
		e.a.closeScreen(s, true)
		e.a.onStarted(s, generation, old, visibility.L0, nil)
		delete(e.a.screens, "A")
		close(finished)
	})
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("FPS updates, teardown or stale start blocked the event loop")
	}
}

type heldClose struct {
	*fakeR
	entered chan struct{}
	release chan struct{}
}

func (renderer *heldClose) Close() error {
	close(renderer.entered)
	<-renderer.release
	return renderer.fakeR.Close()
}

func TestStaleStartDoesNotCloseRendererTwice(t *testing.T) {
	renderer := &heldClose{fakeR: &fakeR{done: make(chan error, 1)}, entered: make(chan struct{}), release: make(chan struct{})}
	e := setupWith(t, 1, true, func(deps *Deps) {
		deps.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer { return renderer }
	})
	defer close(renderer.release)
	queued := make(chan struct{})
	e.a.Do(func() {
		s := e.a.screens["A"]
		generation := s.gen
		e.a.closeScreen(s, false)
		e.a.onStarted(s, generation, renderer, visibility.L0, nil)
		close(queued)
	})
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("stale completion blocked on renderer close")
	}
	select {
	case <-renderer.entered:
	case <-time.After(time.Second):
		t.Fatal("serial worker did not close retired renderer")
	}
}
