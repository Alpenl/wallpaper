package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type heldSink struct {
	entered, release chan struct{}
	bytes.Buffer
}

func (s *heldSink) Write(b []byte) (int, error) {
	close(s.entered)
	<-s.release
	return s.Buffer.Write(b)
}
func (s *heldSink) Close() error { return nil }
func TestChildLogDoesNotWaitForSink(t *testing.T) {
	s := &heldSink{entered: make(chan struct{}), release: make(chan struct{})}
	c := newChildLog(s)
	c.Write(bytes.Repeat([]byte("x"), ChildLogLimit))
	<-s.entered
	done := make(chan struct{})
	go func() {
		for range 100 {
			c.Write(bytes.Repeat([]byte("y"), 65536))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(s.release)
		t.Fatal("writer blocked on sink")
	}
	close(s.release)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Len() != ChildLogLimit || c.Dropped() != 100*65536 {
		t.Fatal(s.Len(), c.Dropped())
	}
}
func TestChildLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.log")
	for _, v := range []string{"first", "second", "third"} {
		c, e := NewChildLog(path)
		if e != nil {
			t.Fatal(e)
		}
		c.Write([]byte(v))
		if e = c.Close(); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(path + ".1")
	if string(b) != "second" {
		t.Fatal(string(b))
	}
	files, _ := filepath.Glob(path + "*")
	if len(files) != 2 {
		t.Fatal(files)
	}
}

func TestChildLogRotationWaitsForTransientWindowsLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.log")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(120 * time.Millisecond)
		_ = f.Close()
		close(released)
	}()
	defer func() {
		select {
		case <-released:
		default:
			_ = f.Close()
		}
	}()
	start := time.Now()
	if err := RotateChildLog(path); err != nil {
		t.Fatalf("rotation failed while the old handle closed: %v", err)
	}
	<-released
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	// On Windows the rename must have waited for the lock to clear; on Unix it
	// succeeds immediately and this only documents that the test is not blocked.
	if time.Since(start) > 2*time.Second {
		t.Fatal("rotation retry exceeded its bound")
	}
}

type failingChildSink struct{}

func (failingChildSink) Write([]byte) (int, error) { return 0, os.ErrPermission }
func (failingChildSink) Close() error              { return os.ErrClosed }
func TestChildLogFirstErrorAndFileCap(t *testing.T) {
	c := newChildLog(failingChildSink{})
	c.Write([]byte("lost"))
	if err := c.Close(); err != os.ErrPermission || c.Dropped() != 4 {
		t.Fatal(err, c.Dropped())
	}
	path := filepath.Join(t.TempDir(), "stderr.log")
	c, err := NewChildLog(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Write(bytes.Repeat([]byte("x"), ChildLogLimit+100))
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != ChildLogLimit || c.Dropped() != 100 {
		t.Fatal(st, err, c.Dropped())
	}
}
