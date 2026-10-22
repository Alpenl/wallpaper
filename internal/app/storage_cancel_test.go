package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wpapp/internal/config"
)

// Cancel at a specific walk checkpoint without relying on filesystem speed.
type walkCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *walkCancelContext) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestDiskUsageCancellation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("123"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	walk := &walkCancelContext{Context: ctx, cancel: cancel}
	size, err := diskUsage(walk, root)
	if !errors.Is(err, context.Canceled) || size != 3 || walk.checks != 3 {
		t.Fatalf("size=%d err=%v checks=%d", size, err, walk.checks)
	}
}

func TestStorageResultsNotPublishedAfterCancellation(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "scan", true: "cleanup"}[cleanup], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := t.TempDir()
			a := &App{ctx: ctx, post: make(chan func(), 4), stopped: make(chan struct{}), d: Deps{Store: &config.Store{}}}
			a.d.Store.Paths.Library = root
			a.d.Store.Paths.Cache = root
			if cleanup {
				a.ClearCache()
				(<-a.post)()
			} else {
				a.refreshStorage()
			}
			var publish func()
			select {
			case publish = <-a.post:
			case <-time.After(time.Second):
				t.Fatal("worker did not return")
			}
			beforeStorage, beforeCleaning := a.storage, a.cleaning
			cancel()
			publish()
			if a.storage != beforeStorage || a.cleaning != beforeCleaning {
				t.Fatal("worker published after cancellation")
			}
		})
	}
}
