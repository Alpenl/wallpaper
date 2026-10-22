//go:build windows

package win32

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func pipePair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	ps, err := NewPipeServer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ps.Close)
	type result struct {
		f   *os.File
		err error
	}
	dial := make(chan result, 1)
	go func() { f, err := DialPipe(ps.Name, 10*time.Second); dial <- result{f, err} }()
	server, err := ps.Accept(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	var client *os.File
	select {
	case r := <-dial:
		if r.err != nil {
			t.Fatal(r.err)
		}
		client = r.f
	case <-time.After(10 * time.Second):
		t.Fatal("dial hung")
	}
	t.Cleanup(func() { client.Close() })
	return client, server
}

func awaitIO(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("I/O did not complete within 10s")
	}
}

func TestPipeConcurrentReadWrite(t *testing.T) {
	client, server := pipePair(t)
	for _, pair := range [][2]*os.File{{client, server}, {server, client}} {
		writer, peer := pair[0], pair[1]
		pending := make(chan error, 1)
		started := make(chan struct{})
		go func() { close(started); var b [1]byte; _, err := io.ReadFull(writer, b[:]); pending <- err }()
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("reader did not start")
		}
		// The reader has started; no peer traffic can complete it before the reply.
		payload := bytes.Repeat([]byte{42}, 100)
		written := make(chan error, 1)
		go func() { _, err := writer.Write(payload); written <- err }()
		received := make(chan error, 1)
		got := make([]byte, len(payload))
		go func() { _, err := io.ReadFull(peer, got); received <- err }()
		awaitIO(t, received)
		awaitIO(t, written)
		if !bytes.Equal(got, payload) {
			t.Fatal("corrupt payload")
		}
		go func() { _, err := peer.Write([]byte{1}); written <- err }()
		awaitIO(t, written)
		awaitIO(t, pending)
	}
}

func TestPipeAcceptTimeout(t *testing.T) {
	ps, err := NewPipeServer()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ps.Accept(100 * time.Millisecond); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
			t.Fatalf("Accept: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Accept timeout hung")
	}
	ps.Close()
}

func TestPipeClosePendingRead(t *testing.T) {
	for _, side := range []string{"client", "server"} {
		t.Run(side, func(t *testing.T) {
			client, server := pipePair(t)
			f := client
			if side == "server" {
				f = server
			}
			read := make(chan error, 1)
			started := make(chan struct{})
			go func() { close(started); var b [1]byte; _, err := f.Read(b[:]); read <- err }()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("reader did not start")
			}
			closed := make(chan error, 1)
			go func() { closed <- f.Close() }()
			awaitIO(t, closed)
			select {
			case err := <-read:
				if err == nil {
					t.Fatal("pending read succeeded after Close")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("pending read did not return")
			}
		})
	}
}
