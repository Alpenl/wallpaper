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
