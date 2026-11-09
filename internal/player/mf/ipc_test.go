package mf

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("IPC reader did not exit")
	}
}
func TestProtocolMismatchRejectedBeforeHandler(t *testing.T) {
	a, b := net.Pipe()
	called := make(chan struct{}, 1)
	c := newConn(a, func(string, json.RawMessage) (any, *Error) { called <- struct{}{}; return nil, nil }, nil)
	if _, err := io.WriteString(b, "{\"v\":99,\"t\":\"req\",\"m\":\"load\"}\n"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, c.Done())
	b.Close()
	if c.Err() == nil {
		t.Fatal("mismatched version accepted")
	}
	select {
	case <-called:
		t.Fatal("handler ran before protocol rejection")
	default:
	}
}
func TestCallAndDisconnect(t *testing.T) {
	a, b := net.Pipe()
	server := newConn(a, func(method string, _ json.RawMessage) (any, *Error) { return map[string]string{"method": method}, nil }, nil)
	client := newConn(b, nil, nil)
	var out map[string]string
	if err := client.Call("evidence", nil, time.Second, &out); err != nil || out["method"] != "evidence" {
		t.Fatalf("reply: %v %v", out, err)
	}
	client.Close()
	waitClosed(t, client.Done())
	waitClosed(t, server.Done())
}
func TestBlockedWriteTimesOutAndReleasesReader(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newConn(a, nil, nil)
	// Real deadline is the behavior under test; no fixed sleep is used.
	if err := c.Call("load", nil, 10*time.Millisecond, nil); err == nil {
		t.Fatal("blocked write succeeded")
	}
	c.Close()
	waitClosed(t, c.Done())
}
func TestLineLimit(t *testing.T) {
	_, err := readLimited(bufio.NewReader(strings.NewReader(strings.Repeat("x", MaxLine+1)+"\n")), MaxLine)
	if err != ErrLineTooLong {
		t.Fatal(err)
	}
}
