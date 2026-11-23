package ipc

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIPCRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	evts := make(chan string, 4)
	host := New(b, func(m string, p json.RawMessage) (any, *Error) {
		switch m {
		case "hello":
			return map[string]any{"pid": 42, "caps": []string{"video"}}, nil
		case "setPause":
			var x struct{ Level int }
			json.Unmarshal(p, &x)
			if x.Level > 3 {
				return nil, &Error{Code: "E_STATE", Msg: "level"}
			}
			return struct{}{}, nil
		}
		return nil, &Error{Code: "E_PROTO", Msg: "unknown " + m}
	}, nil)
	ctl := New(a, nil, func(m string, p json.RawMessage) { evts <- m + string(p) })
	var r struct {
		Pid  int
		Caps []string
	}
	if err := ctl.Call("hello", map[string]any{"proto": 1, "app": "t", "extra": "ignored"}, time.Second, &r); err != nil || r.Pid != 42 {
		t.Fatal(err, r)
	}
	if err := ctl.Call("setPause", map[string]int{"level": 9}, time.Second, nil); err == nil || !strings.Contains(err.Error(), "E_STATE") {
		t.Fatal("应返回 E_STATE", err)
	}
	if err := ctl.Call("nope", nil, time.Second, nil); err == nil {
		t.Fatal("未知方法应报错")
	}
	host.Emit("firstFrame", map[string]int{"ms": 412})
	if e := <-evts; e != `firstFrame{"ms":412}` {
		t.Fatal(e)
	}
	host.Close()
	<-ctl.Done()
	if err := ctl.Call("ping", nil, time.Second, nil); err == nil {
		t.Fatal("断开后调用应失败")
	}
}

func TestIPCVersionAndLimit(t *testing.T) {
	a, b := net.Pipe()
	c := New(a, nil, nil)
	go b.Write([]byte(`{"v":2,"t":"evt","m":"x"}` + "\n"))
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("版本不符应断开")
	}
	if c.Err() == nil || !strings.Contains(c.Err().Error(), "E_PROTO") {
		t.Fatal(c.Err())
	}
	a2, b2 := net.Pipe()
	c2 := New(a2, nil, nil)
	go b2.Write([]byte(strings.Repeat("x", MaxLine+10) + "\n"))
	select {
	case <-c2.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("超长行应断开")
	}
	if c2.Err() != ErrLineTooLong {
		t.Fatal(c2.Err())
	}
}

func TestIPCTimeout(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}() // 读走但不回复
	c := New(a, nil, nil)
	if err := c.Call("ping", nil, 50*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
		t.Fatal(err)
	}
}

func TestIPCBadLineIgnoredAndUnknownFields(t *testing.T) {
	a, b := net.Pipe()
	got := make(chan string, 1)
	New(a, nil, func(m string, p json.RawMessage) { got <- m })
	go b.Write([]byte("not json\n" + `{"v":1,"t":"evt","m":"ready","zzz":1}` + "\n"))
	select {
	case m := <-got:
		if m != "ready" {
			t.Fatal(m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("坏行之后的消息应照常处理")
	}
}

func TestIPCConcurrentCalls(t *testing.T) {
	a, b := net.Pipe()
	New(b, func(m string, p json.RawMessage) (any, *Error) { return string(p), nil }, nil)
	c := New(a, nil, nil)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			var out string
			err := c.Call("echo", i, time.Second, &out)
			if err == nil && out != strings.TrimSpace(string(rune('0'+i%10))) && len(out) == 0 {
				err = errors.New("empty")
			}
			errs <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

type blockingWritePipe struct {
	net.Conn
	inWrite chan struct{}
}

func (p *blockingWritePipe) Write(b []byte) (int, error) {
	select {
	case p.inWrite <- struct{}{}:
	default:
	}
	return p.Conn.Write(b)
}

func TestIPCStalledPipeCallEmitAndClose(t *testing.T) {
	t.Run("non-lifecycle Call times out on stalled write", func(t *testing.T) {
		for _, fallback := range []bool{false, true} {
			a, b := net.Pipe()
			defer b.Close()
			var rw io.ReadWriteCloser = a
			if fallback {
				rw = struct{ io.ReadWriteCloser }{a}
			}
			c := New(rw, nil, nil)
			defer c.Close()
			done := make(chan error, 1)
			go func() { done <- c.Call("hello", nil, 80*time.Millisecond, nil) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
					t.Fatalf("fallback=%v: expected E_TIMEOUT, got %v", fallback, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("fallback=%v: Call hung on stalled pipe", fallback)
			}
		}
	})

	t.Run("Emit times out on stalled pipe", func(t *testing.T) {
		a, b := net.Pipe()
		defer b.Close()
		c := New(a, nil, nil)
		defer c.Close()
		c.SetEmitTimeout(80 * time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- c.Emit("heartbeat", map[string]int{"n": 1}) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
				t.Fatalf("expected E_TIMEOUT from Emit, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Emit hung on stalled pipe")
		}
	})

	t.Run("concurrent waiter respects deadline instead of blocking forever on write lock", func(t *testing.T) {
		a, b := net.Pipe()
		defer b.Close()
		probe := &blockingWritePipe{Conn: a, inWrite: make(chan struct{}, 1)}
		c := New(probe, nil, nil)
		defer c.Close()

		firstDone := make(chan error, 1)
		go func() { firstDone <- c.Call("load", nil, 5*time.Second, nil) }()
		select {
		case <-probe.inWrite:
		case <-time.After(5 * time.Second):
			t.Fatal("first writer never entered Write")
		}

		// Second caller arrives while first holds the write semaphore; it must time out promptly.
		secondDone := make(chan error, 1)
		go func() { secondDone <- c.Call("ping", nil, 80*time.Millisecond, nil) }()
		select {
		case err := <-secondDone:
			if err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
				t.Fatalf("expected E_TIMEOUT while waiting for write lock, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("second Call deadlocked on write lock")
		}

		// Close must return promptly and unblock the stalled first writer without deadlocking.
		closed := make(chan error, 1)
		go func() { closed <- c.Close() }()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("Close deadlocked while Write was stalled")
		}
		select {
		case err := <-firstDone:
			if err == nil {
				t.Fatal("expected error from cancelled Call")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("first Call did not unblock after Close")
		}
	})
}
