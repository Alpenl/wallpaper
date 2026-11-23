// Package ipc 实现 02 章 2.3 的 IPC v1：NDJSON，每行一条信封，单条上限 1 MiB；
// 未知字段忽略；版本不一致即断开。传输层无关（只依赖 io.ReadWriteCloser）。
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
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

// DefaultEmitTimeout bounds event writes so a stalled peer cannot block Emit indefinitely.
const DefaultEmitTimeout = 5 * time.Second

// Conn 是双向连接：Call 发请求，Emit 发事件。
type Conn struct {
	lifecycleLog func(string, ...any)
	rw           io.ReadWriteCloser
	wsem         chan struct{}
	mu           sync.Mutex
	nextID       uint32
	pending      map[uint32]chan Msg
	onReq        Handler
	onEvt        EventHandler
	done         chan struct{}
	closed       chan struct{}
	closeOnce    sync.Once
	emitTimeout  atomic.Int64
	err          error
}

// New 建立连接并开始读。
func New(rw io.ReadWriteCloser, onReq Handler, onEvt EventHandler) *Conn {
	c := &Conn{
		rw:      rw,
		wsem:    make(chan struct{}, 1),
		pending: map[uint32]chan Msg{},
		onReq:   onReq,
		onEvt:   onEvt,
		done:    make(chan struct{}),
		closed:  make(chan struct{}),
	}
	c.emitTimeout.Store(int64(DefaultEmitTimeout))
	// Conn 拥有读取循环；连接关闭或协议/读取失败后退出。
	go c.readLoop()
	return c
}

// SetEmitTimeout configures the write timeout used by Emit. Non-positive values restore DefaultEmitTimeout.
func (c *Conn) SetEmitTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultEmitTimeout
	}
	c.emitTimeout.Store(int64(d))
}

// Done 在连接断开时关闭。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err 返回断开原因。
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close closes the underlying transport first to unblock any stuck Read/Write
// before acquiring cleanup locks.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	err := c.rw.Close()
	c.mu.Lock()
	if c.err == nil {
		c.err = io.ErrClosedPipe
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	return err
}

func (c *Conn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Conn) acquireWrite(deadline time.Time) error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}
	select {
	case c.wsem <- struct{}{}:
		if c.isClosed() {
			<-c.wsem
			return io.ErrClosedPipe
		}
		return nil
	default:
	}
	if deadline.IsZero() {
		select {
		case c.wsem <- struct{}{}:
			if c.isClosed() {
				<-c.wsem
				return io.ErrClosedPipe
			}
			return nil
		case <-c.closed:
			return io.ErrClosedPipe
		case <-c.done:
			return io.ErrClosedPipe
		}
	}
	rem := time.Until(deadline)
	if rem <= 0 {
		return fmt.Errorf("E_TIMEOUT: write: %w", os.ErrDeadlineExceeded)
	}
	t := time.NewTimer(rem)
	defer t.Stop()
	select {
	case c.wsem <- struct{}{}:
		if c.isClosed() {
			<-c.wsem
			return io.ErrClosedPipe
		}
		return nil
	case <-c.closed:
		return io.ErrClosedPipe
	case <-c.done:
		return io.ErrClosedPipe
	case <-t.C:
		return fmt.Errorf("E_TIMEOUT: write: %w", os.ErrDeadlineExceeded)
	}
}

func (c *Conn) writeDeadline(m Msg, deadline time.Time) error {
	m.V = ProtocolMajor
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) >= MaxLine {
		return ErrLineTooLong
	}
	if err := c.acquireWrite(deadline); err != nil {
		return err
	}
	defer func() { <-c.wsem }()
	if c.isClosed() {
		return io.ErrClosedPipe
	}
	var timedOut atomic.Bool
	var clear func()
	if !deadline.IsZero() {
		rem := time.Until(deadline)
		if rem <= 0 {
			return fmt.Errorf("E_TIMEOUT: write: %w", os.ErrDeadlineExceeded)
		}
		if w, ok := c.rw.(interface{ SetWriteDeadline(time.Time) error }); ok {
			if w.SetWriteDeadline(deadline) == nil {
				clear = func() { _ = w.SetWriteDeadline(time.Time{}) }
			}
		}
		if clear == nil {
			done := make(chan struct{})
			timer := time.AfterFunc(rem, func() { timedOut.Store(true); _ = c.rw.Close(); close(done) })
			clear = func() {
				if !timer.Stop() {
					<-done
				}
			}
		}
	}
	_, err = c.rw.Write(append(b, '\n'))
	if clear != nil {
		clear()
	}
	if timedOut.Load() || errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("E_TIMEOUT: write: %w", os.ErrDeadlineExceeded)
	}
	return err
}

// SetLifecycleLogger installs a narrow sink; only control calls are logged.
func (c *Conn) SetLifecycleLogger(log func(string, ...any)) {
	c.mu.Lock()
	c.lifecycleLog = log
	c.mu.Unlock()
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
			// Conn 拥有请求任务；handler 返回后，回复完成、断连或写入超时即退出。
			go c.serve(m)
		case "evt":
			if c.onEvt != nil {
				c.onEvt(m.M, m.P)
			}
		}
	}
}

func (c *Conn) serve(m Msg) {
	if c.isClosed() {
		return
	}
	res := Msg{T: "res", ID: m.ID}
	var r any
	e := &Error{Code: "E_STATE", Msg: "no handler"}
	if c.onReq != nil {
		r, e = c.onReq(m.M, m.P)
	}
	ok := e == nil
	res.OK = &ok
	if ok {
		var err error
		res.R, err = json.Marshal(r)
		if err != nil {
			ok = false
			res.Err = &Error{Code: "E_INTERNAL", Msg: "marshal response: " + err.Error()}
			c.mu.Lock()
			log := c.lifecycleLog
			c.mu.Unlock()
			if log != nil {
				log("response_marshal_error", "request_id", m.ID, "op", m.M, "err", err)
			}
		}
	} else {
		res.Err = e
	}
	_ = c.writeDeadline(res, time.Now().Add(5*time.Second))
}

func (c *Conn) fail(err error) {
	c.closeOnce.Do(func() { close(c.closed) })
	_ = c.rw.Close()
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
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
	deadline := time.Now().Add(timeout)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("E_PROTO: connection closed (%v)", err)
	}
	log := c.lifecycleLog
	c.nextID++
	id := c.nextID
	ch := make(chan Msg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	p, marshalErr := json.Marshal(params)
	lifecycle := method == "setPause" || method == "seek" || method == "shutdown"
	stage := func(name string, fields ...any) {
		if lifecycle && log != nil {
			log(name, append([]any{"request_id", id, "op", method}, fields...)...)
		}
	}
	begin := time.Now()
	stage("write_begin")
	err := marshalErr
	if err == nil {
		err = c.writeDeadline(Msg{T: "req", ID: id, M: method, P: p}, deadline)
	}
	stage("write_end", "elapsed_ms", time.Since(begin).Milliseconds(), "err", err)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			stage("timeout", "phase", "write")
		} else {
			stage("response", "phase", "write", "err", err)
		}
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	begin = time.Now()
	rem := time.Until(deadline)
	if rem <= 0 {
		stage("timeout", "phase", "response", "elapsed_ms", time.Since(begin).Milliseconds())
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("E_TIMEOUT: %s", method)
	}
	t := time.NewTimer(rem)
	defer t.Stop()
	select {
	case m, ok := <-ch:
		stage("response", "elapsed_ms", time.Since(begin).Milliseconds(), "err", m.Err, "closed", !ok)
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
		stage("timeout", "phase", "response", "elapsed_ms", time.Since(begin).Milliseconds())
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("E_TIMEOUT: %s", method)
	}
}

// Emit sends an event with the connection's configured emit write timeout.
func (c *Conn) Emit(method string, params any) error {
	return c.EmitTimeout(method, params, time.Duration(c.emitTimeout.Load()))
}

// EmitTimeout sends an event with a bounded total write deadline.
func (c *Conn) EmitTimeout(method string, params any, timeout time.Duration) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = DefaultEmitTimeout
	}
	return c.writeDeadline(Msg{T: "evt", M: method, P: p}, time.Now().Add(timeout))
}
