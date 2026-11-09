package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedLogCapRotationAndWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.log")
	l, err := OpenBoundedLog(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 3<<20)
	if n, err := l.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("drain: %d %v", n, err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 2<<20 || l.dropped != 1<<20 {
		t.Fatalf("cap: %v %v dropped=%d", info, err, l.dropped)
	}
	next, err := OpenBoundedLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err = os.Stat(path + ".1"); err != nil || info.Size() != 2<<20 {
		t.Fatalf("rotation: %v %v", info, err)
	}
	if err = next.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = next.Write([]byte("failure")); err != nil {
		t.Fatal("must keep draining", err)
	}
	if next.first == nil || next.dropped != 7 {
		t.Fatal("write failure not retained")
	}
	if err = next.Close(); err == nil {
		t.Fatal("Close hid first I/O failure")
	}
}
