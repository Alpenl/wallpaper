// Package mpv 是 Windows/Linux 的视频渲染器：mpv 子进程（--wid 画到我们的表面），通过 mpv 自带的 JSON IPC 控制（02 章 2.3）。
// mpv 是独立的 GPL 程序，只通过命令行参数和 IPC 通信，不链接进主程序（ADR-004）。
package mpv

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"wpapp/internal/render"
)

// client 是 mpv JSON IPC 的最小客户端（每行一个 JSON，带 request_id）。
type client struct {
	log     render.LogFunc
	rw      io.ReadWriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	next    int
	pending map[int]chan resp
	loaded  chan struct{}
	loops   atomic.Int64 // playback-restart 事件计数（循环回到开头会触发）
	first   chan struct{}
	once    sync.Once
	closed  chan struct{}
}

type resp struct {
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	RequestID int             `json:"request_id"`
	Event     string          `json:"event"`
	Prefix    string          `json:"prefix"`
	Level     string          `json:"level"`
	Text      string          `json:"text"`
}

func newClient(rw io.ReadWriteCloser, logs ...render.LogFunc) *client {
	c := &client{rw: rw, pending: map[int]chan resp{}, first: make(chan struct{}), closed: make(chan struct{})}
	if len(logs) > 0 {
		c.log = logs[0]
	}
	go c.readLoop()
	return c
}

func (c *client) readLoop() {
	defer close(c.closed)
	sc := bufio.NewScanner(c.rw)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r resp
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.Event != "" {
			if r.Event == "log-message" {
				c.log.Log("mpv_log", "level", r.Level, "prefix", r.Prefix, "text", r.Text)
			}
			if r.Event == "file-loaded" {
				c.mu.Lock()
				if c.loaded != nil {
					close(c.loaded)
					c.loaded = nil
				}
				c.mu.Unlock()
			}
			if r.Event == "playback-restart" {
				c.loops.Add(1)
				c.once.Do(func() { close(c.first) })
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[r.RequestID]
		delete(c.pending, r.RequestID)
		c.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

var errClosed = errors.New("mpv: ipc closed")

func (c *client) cmd(args ...any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan resp, 1)
	c.pending[id] = ch
	b, _ := json.Marshal(map[string]any{"command": args, "request_id": id})
	c.mu.Unlock()
	lifecycle := len(args) > 0 && (args[0] == "loadfile" || args[0] == "stop" || (args[0] == "set_property" && len(args) > 1 && args[1] == "pause"))
	stage := func(name string, fields ...any) {
		if lifecycle {
			c.log.Log(name, append([]any{"request_id", id, "op", args[0]}, fields...)...)
		}
	}
	begin := time.Now()
	stage("write_begin")
	c.writeMu.Lock()
	var timedOut atomic.Bool
	// Serialize deadline setup/reset with the write it protects.
	var clearDeadline func()
	if rw, ok := c.rw.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := rw.SetWriteDeadline(time.Now().Add(2 * time.Second)); err == nil {
			clearDeadline = func() { _ = rw.SetWriteDeadline(time.Time{}) }
		}
	}
	if clearDeadline == nil {
		done := make(chan struct{})
		guard := time.AfterFunc(2*time.Second, func() { timedOut.Store(true); _ = c.rw.Close(); close(done) })
		clearDeadline = func() {
			if !guard.Stop() {
				<-done
			}
		}
	}
	_, err := c.rw.Write(append(b, '\n'))
	clearDeadline()
	c.writeMu.Unlock()
	stage("write_end", "elapsed_ms", time.Since(begin).Milliseconds(), "err", err)
	if err != nil {
		if timedOut.Load() || errors.Is(err, os.ErrDeadlineExceeded) {
			stage("timeout", "phase", "write")
		} else {
			stage("response", "phase", "write", "err", err)
		}
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	begin = time.Now()
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case r, ok := <-ch:
		stage("response", "elapsed_ms", time.Since(begin).Milliseconds(), "error", r.Error, "closed", !ok)
		if !ok {
			return nil, errClosed
		}
		if r.Error != "success" {
			return r.Data, errors.New("mpv: " + r.Error)
		}
		return r.Data, nil
	case <-t.C:
		stage("timeout", "phase", "response", "elapsed_ms", time.Since(begin).Milliseconds())
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, errors.New("mpv: ipc timeout")
	}
}

func (c *client) getString(p string) (string, error) {
	d, err := c.cmd("get_property_string", p)
	if err != nil {
		return "", err
	}
	var s string
	_ = json.Unmarshal(d, &s)
	return s, nil
}

func (c *client) getFloat(p string) (float64, error) {
	s, err := c.getString(p)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

func (c *client) setPause(p bool) error { _, err := c.cmd("set_property", "pause", p); return err }
func (c *client) Close() error          { return c.rw.Close() }

// stallDetector 实现 2.4 的卡住判定（Q-P3 修正版）：进度键 = (循环计数, time-pos)，
// 未暂停时连续 2 次采样完全不变才判定卡住；暂停/释放期间不采样。
type stallDetector struct {
	last struct {
		loops int64
		pos   float64
		ok    bool
	}
	same int
}

// observe 返回 true 表示卡住。
func (d *stallDetector) observe(paused bool, loops int64, pos float64) bool {
	if paused {
		d.same, d.last.ok = 0, false
		return false
	}
	if d.last.ok && d.last.loops == loops && d.last.pos == pos {
		d.same++
	} else {
		d.same = 0
	}
	d.last.loops, d.last.pos, d.last.ok = loops, pos, true
	return d.same >= 2
}

// A command reply only acknowledges loadfile; per-file options are applied later.
// Subscribe before sending so an early file-loaded event cannot be lost.
func (c *client) loadFile(path string, options map[string]string, paused bool) error {
	loaded := make(chan struct{})
	c.mu.Lock()
	c.loaded = loaded
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.loaded == loaded {
			c.loaded = nil
		}
		c.mu.Unlock()
	}()
	if _, err := c.cmd("loadfile", path, "replace", -1, options); err != nil {
		return err
	}
	select {
	case <-loaded:
		return c.setPause(paused)
	case <-c.closed:
		return errClosed
	case <-time.After(5 * time.Second):
		return errors.New("mpv: file-loaded timeout")
	}
}
