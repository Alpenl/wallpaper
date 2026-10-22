package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The channels hold a write open at a known point, independent of filesystem
// speed. The timeout only detects an unexpected completion while it is held.
func requireSaverBlocked[T any](t *testing.T, ch <-chan T, message string) {
	t.Helper()
	select {
	case result := <-ch:
		t.Fatalf("%s: %v", message, result)
	// sleep: assert no completion while the write is deliberately held.
	case <-time.After(25 * time.Millisecond):
	}
}

func awaitSaverResult[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for saver")
		var zero T
		return zero
	}
}

func saverValue(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSaverSerializesWritesAndCoalescesWhileWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	started := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s := &Saver{Path: path, Delay: time.Hour}
	s.writeAtomic = func(path string, value any) error {
		n := value.(int)
		started <- n
		if n == 1 {
			<-release
		}
		return WriteAtomic(path, value)
	}
	s.Schedule(func() any { return 1 })
	first := make(chan error, 1)
	go func() { first <- s.Flush() }()
	if got := awaitSaverResult(t, started); got != 1 {
		t.Fatalf("first write = %d, want 1", got)
	}
	// Scheduling must remain nonblocking, and only the newest pending snapshot
	// should be evaluated when a waiting Flush finally gets the writer lock.
	var superseded atomic.Bool
	s.Schedule(func() any { superseded.Store(true); return 2 })
	s.Schedule(func() any { return 3 })
	second := make(chan error, 1)
	go func() { second <- s.Flush() }()
	requireSaverBlocked(t, started, "second write overlapped the first")
	requireSaverBlocked(t, second, "second Flush returned while first write was blocked")
	unblock()
	if err := awaitSaverResult(t, first); err != nil {
		t.Fatal(err)
	}
	if err := awaitSaverResult(t, second); err != nil {
		t.Fatal(err)
	}
	if got := awaitSaverResult(t, started); got != 3 {
		t.Fatalf("second write = %d, want latest value 3", got)
	}
	if superseded.Load() {
		t.Fatal("superseded pending snapshot was evaluated")
	}
	if got := saverValue(t, path); got != 3 {
		t.Fatalf("saved value = %d, want 3", got)
	}
	if got := saverValue(t, path+".bak"); got != 1 {
		t.Fatalf("backup value = %d, want previous value 1", got)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestSaverFlushAllWaitsForTimerWriteWithoutPendingSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s := &Saver{Path: path, Delay: time.Nanosecond}
	s.writeAtomic = func(path string, value any) error {
		close(started)
		<-release
		return WriteAtomic(path, value)
	}
	s.Schedule(func() any { return 7 })
	awaitSaverResult(t, started)
	s.mu.Lock()
	noPending := s.pending == nil
	s.mu.Unlock()
	if !noPending {
		t.Fatal("timer should have consumed the pending snapshot")
	}
	store := &Store{ConfigSaver: s, LibrarySaver: &Saver{}, AssignSaver: &Saver{}, StateSaver: &Saver{}}
	done := make(chan struct{})
	go func() {
		store.FlushAll()
		close(done)
	}()
	requireSaverBlocked(t, done, "FlushAll returned before the timer write finished")
	unblock()
	awaitSaverResult(t, done)
	if got := saverValue(t, path); got != 7 {
		t.Fatalf("saved value = %d, want 7", got)
	}
}

func TestSaverErrorCallbackCanFlushAgain(t *testing.T) {
	wantErr := errors.New("first write failed")
	s := &Saver{Path: filepath.Join(t.TempDir(), "config.json"), Delay: time.Hour}
	var writes int
	s.writeAtomic = func(path string, value any) error {
		writes++
		if writes == 1 {
			return wantErr
		}
		return WriteAtomic(path, value)
	}
	callbackErr := make(chan error, 1)
	s.OnError = func(err error) {
		if !errors.Is(err, wantErr) {
			callbackErr <- err
			return
		}
		s.Schedule(func() any { return 2 })
		callbackErr <- s.Flush()
	}
	s.Schedule(func() any { return 1 })
	done := make(chan error, 1)
	go func() { done <- s.Flush() }()
	if err := awaitSaverResult(t, done); !errors.Is(err, wantErr) {
		t.Fatalf("Flush error = %v, want %v", err, wantErr)
	}
	if err := awaitSaverResult(t, callbackErr); err != nil {
		t.Fatalf("error callback could not flush again: %v", err)
	}
	if got := saverValue(t, s.Path); got != 2 {
		t.Fatalf("saved value = %d, want 2", got)
	}
}

func TestSaverConcurrentScheduleAndFlush(t *testing.T) {
	s := &Saver{Path: filepath.Join(t.TempDir(), "config.json"), Delay: time.Nanosecond}
	const workers = 8
	errs := make(chan error, workers*10)
	s.OnError = func(err error) { errs <- err }
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := range 10 {
				value := worker*10 + iteration
				s.Schedule(func() any { return value })
				_ = s.Flush()
			}
		}()
	}
	wg.Wait()
	// Once producers finish, the last scheduled value must be durable when
	// Flush returns, even with timer callbacks still contending for the lock.
	s.Schedule(func() any { return 1000 })
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatalf("concurrent write failed: %v", err)
	default:
	}
	if got := saverValue(t, s.Path); got != 1000 {
		t.Fatalf("saved value = %d, want final value 1000", got)
	}
}
