package ipc

import (
	"encoding/json"
	"errors"
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
	case <-time.After(time.Second):
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
	case <-time.After(2 * time.Second):
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
	case <-time.After(time.Second):
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
