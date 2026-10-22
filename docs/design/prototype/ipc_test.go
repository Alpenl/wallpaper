package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIPCRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	evts := make(chan string, 4)
	host := newIPC(b, func(m string, p json.RawMessage) (any, *ipcErr) {
		switch m {
		case "hello":
			return map[string]any{"pid": 42, "caps": []string{"video"}}, nil
		case "setPause":
			var x struct{ Level int }
			json.Unmarshal(p, &x)
			if x.Level > 3 {
				return nil, &ipcErr{Code: "E_STATE", Msg: "level"}
			}
			return struct{}{}, nil
		}
		return nil, &ipcErr{Code: "E_PROTO", Msg: "unknown " + m}
	}, nil)
	ctl := newIPC(a, nil, func(m string, p json.RawMessage) { evts <- m + string(p) })
	var r struct {
		Pid  int
		Caps []string
	}
	if err := ctl.call("hello", map[string]any{"proto": 1, "app": "t", "extra": "ignored"}, time.Second, &r); err != nil || r.Pid != 42 {
		t.Fatal(err, r)
	}
	if err := ctl.call("setPause", map[string]int{"level": 9}, time.Second, nil); err == nil || !strings.Contains(err.Error(), "E_STATE") {
		t.Fatal("应返回 E_STATE", err)
	}
	if err := ctl.call("nope", nil, time.Second, nil); err == nil {
		t.Fatal("未知方法应报错")
	}
	host.emit("firstFrame", map[string]int{"ms": 412})
	if e := <-evts; e != `firstFrame{"ms":412}` {
		t.Fatal(e)
	}
	host.Close()
	<-ctl.Done()
	if err := ctl.call("ping", nil, time.Second, nil); err == nil {
		t.Fatal("断开后调用应失败")
	}
}

func TestIPCVersionAndLimit(t *testing.T) {
	a, b := net.Pipe()
	c := newIPC(a, nil, nil)
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
	c2 := newIPC(a2, nil, nil)
	go b2.Write([]byte(strings.Repeat("x", ipcMaxLine+10) + "\n"))
	select {
	case <-c2.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("超长行应断开")
	}
	if c2.Err() != errLineTooLong {
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
	c := newIPC(a, nil, nil)
	if err := c.call("ping", nil, 50*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "E_TIMEOUT") {
		t.Fatal(err)
	}
}
