package ipc

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type replyProbe struct {
	net.Conn
	writing chan struct{}
	writes  atomic.Int32
}

func (p *replyProbe) Write(b []byte) (int, error) {
	p.writes.Add(1)
	select {
	case p.writing <- struct{}{}:
	default:
	}
	return p.Conn.Write(b)
}
func awaitReply(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(8 * time.Second):
		t.Fatal("reply task did not progress")
	}
}

func TestServeBlockedReplyReleased(t *testing.T) {
	for _, mode := range []string{"close", "disconnect", "deadline", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			a, b := net.Pipe()
			defer b.Close()
			p := &replyProbe{Conn: a, writing: make(chan struct{}, 1)}
			var transport io.ReadWriteCloser = p
			if mode == "fallback" {
				transport = struct{ io.ReadWriteCloser }{p}
			}
			c := New(transport, nil, nil)
			defer c.Close()
			done := make(chan struct{})
			go func() { c.serve(Msg{ID: 1}); close(done) }()
			awaitReply(t, p.writing)
			switch mode {
			case "close":
				c.Close()
			case "disconnect":
				b.Close()
			}
			awaitReply(t, done)
		})
	}
}

func TestServeHandlerReturningAfterCloseDoesNotWrite(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p := &replyProbe{Conn: a, writing: make(chan struct{}, 1)}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := New(p, func(string, json.RawMessage) (any, *Error) { close(entered); <-release; return "late", nil }, nil)
	defer c.Close()
	go func() { c.serve(Msg{ID: 1}); close(done) }()
	awaitReply(t, entered)
	c.Close()
	awaitReply(t, c.Done())
	close(release)
	awaitReply(t, done)
	if p.writes.Load() != 0 {
		t.Fatal("reply written after disconnect")
	}
}

func TestServeMarshalError(t *testing.T) {
	for _, logging := range []bool{false, true} {
		t.Run(map[bool]string{false: "no logger", true: "logger"}[logging], func(t *testing.T) {
			a, b := net.Pipe()
			server := New(a, func(string, json.RawMessage) (any, *Error) { return make(chan int), nil }, nil)
			defer server.Close()
			logged := make(chan struct{}, 1)
			if logging {
				server.SetLifecycleLogger(func(name string, fields ...any) {
					if name == "response_marshal_error" {
						logged <- struct{}{}
					}
				})
			}
			client := New(b, nil, nil)
			defer client.Close()
			err := client.Call("bad", nil, time.Second, nil)
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != "E_INTERNAL" {
				t.Fatalf("expected E_INTERNAL, got %v", err)
			}
			if logging {
				awaitReply(t, logged)
			}
		})
	}
}
