package app

import "sync"

type screenOp struct {
	key  string
	run  func()
	done chan struct{}
}

type screenOps struct {
	mu      sync.Mutex
	pending []screenOp
	wake    chan struct{}
	done    chan struct{}
	tail    <-chan struct{}
	failed  bool
}

const maxScreenOps = 256

func newScreenOps() *screenOps {
	return &screenOps{wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (ops *screenOps) enqueue(run func()) {
	ops.replaceLast("", run)
}

func (ops *screenOps) replaceLast(key string, run func()) {
	ops.mu.Lock()
	if ops.failed {
		ops.mu.Unlock()
		return
	}
	if count := len(ops.pending); key != "" && count > 0 && ops.pending[count-1].key == key {
		ops.pending[count-1].run = run
	} else {
		if len(ops.pending) >= maxScreenOps {
			ops.failed = true
			ops.mu.Unlock()
			return
		}
		ops.append(key, run)
	}
	ops.mu.Unlock()
	ops.notify()
}

func (ops *screenOps) append(key string, run func()) {
	done := make(chan struct{})
	ops.pending = append(ops.pending, screenOp{key: key, run: run, done: done})
	ops.tail = done
}

func (ops *screenOps) cleanup(run func()) {
	ops.mu.Lock()
	ops.append("", run)
	ops.mu.Unlock()
	ops.notify()
}

func (ops *screenOps) barrier() <-chan struct{} {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if ops.tail == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return ops.tail
}

func (ops *screenOps) accepting() bool {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.pending) >= maxScreenOps {
		ops.failed = true
	}
	return !ops.failed
}

func (ops *screenOps) notify() {
	select {
	case ops.wake <- struct{}{}:
	default:
	}
}

func (ops *screenOps) take() func() {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.pending) == 0 {
		return nil
	}
	op := ops.pending[0]
	ops.pending[0] = screenOp{}
	ops.pending = ops.pending[1:]
	if len(ops.pending) == 0 {
		ops.pending = nil
	}
	return func() {
		defer close(op.done)
		op.run()
	}
}

func (ops *screenOps) run(quit <-chan struct{}) {
	defer close(ops.done)
	for {
		if run := ops.take(); run != nil {
			run()
			continue
		}
		select {
		case <-ops.wake:
		case <-quit:
			for run := ops.take(); run != nil; run = ops.take() {
				run()
			}
			return
		}
	}
}
