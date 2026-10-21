package main

// IPC v1（02 章 2.3）的最小实现：NDJSON，每行一条信封，单条上限 1 MiB；未知字段忽略；版本不符断开。
// 平台无关（只依赖 io.ReadWriteCloser），所以在 Linux 上用 net.Pipe 做单元测试，在 Windows 上跑在命名管道上。

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const ipcMaxLine = 1 << 20

type ipcMsg struct {
	V   int             `json:"v"`
	T   string          `json:"t"`
	ID  uint32          `json:"id,omitempty"`
	M   string          `json:"m,omitempty"`
	P   json.RawMessage `json:"p,omitempty"`
	OK  *bool           `json:"ok,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	Err *ipcErr         `json:"err,omitempty"`
}

type ipcErr struct {
	Code  string `json:"code"`
	Msg   string `json:"msg"`
	Fatal bool   `json:"fatal,omitempty"`
}

func (e *ipcErr) Error() string { return e.Code + ": " + e.Msg }

// ipcConn：双向。请求方用 call，处理方用 onReq；事件用 emit / onEvt。
type ipcConn struct {
	rw      io.ReadWriteCloser
	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  uint32
	pending map[uint32]chan ipcMsg
	onReq   func(m string, p json.RawMessage) (any, *ipcErr)
	onEvt   func(m string, p json.RawMessage)
	done    chan struct{}
	err     error
}

func newIPC(rw io.ReadWriteCloser, onReq func(string, json.RawMessage) (any, *ipcErr), onEvt func(string, json.RawMessage)) *ipcConn {
	c := &ipcConn{rw: rw, pending: map[uint32]chan ipcMsg{}, onReq: onReq, onEvt: onEvt, done: make(chan struct{})}
	go c.readLoop()
	return c
}

func (c *ipcConn) Done() <-chan struct{} { return c.done }
func (c *ipcConn) Err() error            { return c.err }
func (c *ipcConn) Close() error          { return c.rw.Close() }

func (c *ipcConn) write(m ipcMsg) error {
	m.V = 1
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.rw.Write(append(b, '\n'))
	return err
}

func (c *ipcConn) readLoop() {
	defer close(c.done)
	r := bufio.NewReaderSize(c.rw, 64<<10)
	for {
		line, err := readLimited(r, ipcMaxLine)
		if err != nil {
			c.fail(err)
			return
		}
		var m ipcMsg
		if json.Unmarshal(line, &m) != nil {
			continue // 坏行忽略
		}
		if m.V != 1 {
			c.fail(fmt.Errorf("E_PROTO: version %d", m.V))
			return
		}
		switch m.T {
		case "res":
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case "req":
			go func(m ipcMsg) {
				res := ipcMsg{T: "res", ID: m.ID}
				var r any
				var e *ipcErr = &ipcErr{Code: "E_STATE", Msg: "no handler"}
				if c.onReq != nil {
					r, e = c.onReq(m.M, m.P)
				}
				ok := e == nil
				res.OK = &ok
				if ok {
					res.R, _ = json.Marshal(r)
				} else {
					res.Err = e
				}
				c.write(res)
			}(m)
		case "evt":
			if c.onEvt != nil {
				c.onEvt(m.M, m.P)
			}
		}
	}
}

func (c *ipcConn) fail(err error) {
	c.err = err
	c.rw.Close()
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

var errLineTooLong = errors.New("E_PROTO: line exceeds 1 MiB")

func readLimited(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, errLineTooLong
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

func (c *ipcConn) call(method string, params any, timeout time.Duration, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan ipcMsg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	p, _ := json.Marshal(params)
	if err := c.write(ipcMsg{T: "req", ID: id, M: method, P: p}); err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return fmt.Errorf("E_PROTO: connection closed (%v)", c.err)
		}
		if m.OK == nil || !*m.OK {
			if m.Err != nil {
				return m.Err
			}
			return errors.New("E_INTERNAL: bad response")
		}
		if out != nil && len(m.R) > 0 {
			return json.Unmarshal(m.R, out)
		}
		return nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("E_TIMEOUT: %s", method)
	}
}

func (c *ipcConn) emit(method string, params any) error {
	p, _ := json.Marshal(params)
	return c.write(ipcMsg{T: "evt", M: method, P: p})
}
