package telemetry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const ChildLogLimit = 2 << 20

// RotateChildLog retains exactly one previous launch, never rotating live files.
func RotateChildLog(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(path, path+".1")
}

// ChildLog consumes without waiting for disk I/O. Its sole worker drains on Close.
// Both queued memory and total accepted bytes are bounded; excess is counted.
type ChildLog struct {
	mu                sync.Mutex
	queue             chan []byte
	done              chan struct{}
	sink              io.WriteCloser
	accepted, dropped int
	err               error
	closed            bool
}

func NewChildLog(path string) (*ChildLog, error) {
	if err := RotateChildLog(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return newChildLog(f), nil
}
func newChildLog(w io.WriteCloser) *ChildLog {
	c := &ChildLog{queue: make(chan []byte, 64), done: make(chan struct{}), sink: w}
	go func() {
		defer close(c.done)
		for b := range c.queue {
			n, err := w.Write(b)
			if err == nil && n != len(b) {
				err = io.ErrShortWrite
			}
			c.mu.Lock()
			c.dropped += len(b) - n
			if c.err == nil {
				c.err = err
			}
			c.mu.Unlock()
		}
		err := w.Close()
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		c.mu.Unlock()
	}()
	return c
}
func (c *ChildLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if c.closed {
		c.dropped += n
		return n, nil
	}
	keep := min(n, ChildLogLimit-c.accepted)
	if keep > 0 {
		select {
		case c.queue <- append([]byte(nil), p[:keep]...):
			c.accepted += keep
		default:
			keep = 0
		}
	}
	c.dropped += n - keep
	return n, nil
}
func (c *ChildLog) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
func (c *ChildLog) Dropped() int { c.mu.Lock(); defer c.mu.Unlock(); return c.dropped }
