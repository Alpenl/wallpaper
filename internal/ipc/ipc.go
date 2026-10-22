// Package ipc 实现 02 章 2.3 的 IPC v1：NDJSON，每行一条信封，单条上限 1 MiB；
// 未知字段忽略；版本不一致即断开。传输层无关（只依赖 io.ReadWriteCloser）。
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ProtocolMajor is the NDJSON envelope protocol version.
const ProtocolMajor = 1

// MaxLine 是单条消息上限。
const MaxLine = 1 << 20

// Msg 是信封。
type Msg struct {
	V   int             `json:"v"`
	T   string          `json:"t"`
	ID  uint32          `json:"id,omitempty"`
	M   string          `json:"m,omitempty"`
	P   json.RawMessage `json:"p,omitempty"`
	OK  *bool           `json:"ok,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	Err *Error          `json:"err,omitempty"`
}

// Error 是 res 里的错误；Code 取 02 章的 E_PROTO/E_STATE/E_PATH/E_LOAD/E_GPU/E_TIMEOUT/E_INTERNAL。
type Error struct {
	Code  string `json:"code"`
	Msg   string `json:"msg"`
	Fatal bool   `json:"fatal,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// Handler 处理请求；返回值序列化到 r。
type Handler func(method string, p json.RawMessage) (any, *Error)

// EventHandler 处理事件。
type EventHandler func(method string, p json.RawMessage)

// ErrLineTooLong 在单行超过 MaxLine 时返回。
var ErrLineTooLong = errors.New("E_PROTO: line exceeds 1 MiB")

// Conn 是双向连接：Call 发请求，Emit 发事件。
type Conn struct {
	rw      io.ReadWriteCloser
	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  uint32
	pending map[uint32]chan Msg
	onReq   Handler
	onEvt   EventHandler
	done    chan struct{}
	err     error
}

// New 建立连接并开始读。
func New(rw io.ReadWriteCloser, onReq Handler, onEvt EventHandler) *Conn {
	c := &Conn{rw: rw, pending: map[uint32]chan Msg{}, onReq: onReq, onEvt: onEvt, done: make(chan struct{})}
	go c.readLoop()
	return c
}

// Done 在连接断开时关闭。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err 返回断开原因。
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close 关闭底层传输。
func (c *Conn) Close() error { return c.rw.Close() }

func (c *Conn) write(m Msg) error {
	m.V = ProtocolMajor
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) >= MaxLine {
		return ErrLineTooLong
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.rw.Write(append(b, '\n'))
	return err
}

func (c *Conn) readLoop() {
	defer close(c.done)
	r := bufio.NewReaderSize(c.rw, 64<<10)
	for {
		line, err := readLimited(r, MaxLine)
		if err != nil {
			c.fail(err)
			return
		}
		var m Msg
		if json.Unmarshal(line, &m) != nil {
			continue // 坏行忽略
		}
		if m.V != ProtocolMajor {
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
			go c.serve(m)
		case "evt":
			if c.onEvt != nil {
				c.onEvt(m.M, m.P)
			}
		}
	}
}

func (c *Conn) serve(m Msg) {
	res := Msg{T: "res", ID: m.ID}
	var r any
	e := &Error{Code: "E_STATE", Msg: "no handler"}
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
	_ = c.write(res)
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.rw.Close()
}

func readLimited(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, ErrLineTooLong
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// Call 发请求并等待回复；out 非 nil 时解码 r。
func (c *Conn) Call(method string, params any, timeout time.Duration, out any) error {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("E_PROTO: connection closed (%v)", err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Msg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	p, _ := json.Marshal(params)
	if err := c.write(Msg{T: "req", ID: id, M: method, P: p}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case m, ok := <-ch:
		if !ok {
			return fmt.Errorf("E_PROTO: connection closed (%v)", c.Err())
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
	case <-t.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("E_TIMEOUT: %s", method)
	}
}

// Emit 发事件。
func (c *Conn) Emit(method string, params any) error {
	p, _ := json.Marshal(params)
	return c.write(Msg{T: "evt", M: method, P: p})
}
