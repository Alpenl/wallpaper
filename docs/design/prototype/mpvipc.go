package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mpvClient: mpv JSON IPC 的最小客户端（每行一个 JSON，带 request_id）。
type mpvClient struct {
	rw      io.ReadWriteCloser
	mu      sync.Mutex
	next    int
	pending map[int]chan mpvResp
	loops   atomic.Int64 // playback-restart 事件计数（每次循环回到开头都会触发，见 box 实测日志）
}
type mpvResp struct {
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	RequestID int             `json:"request_id"`
	Event     string          `json:"event"`
}

func newMpvClient(rw io.ReadWriteCloser) *mpvClient {
	c := &mpvClient{rw: rw, pending: map[int]chan mpvResp{}}
	go c.readLoop()
	return c
}

func (c *mpvClient) readLoop() {
	sc := bufio.NewScanner(c.rw)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r mpvResp
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.Event != "" {
			if r.Event == "playback-restart" {
				c.loops.Add(1)
			}
			if r.Event != "property-change" {
				logf("MPV_EVENT %s", r.Event)
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

func (c *mpvClient) cmd(args ...any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan mpvResp, 1)
	c.pending[id] = ch
	b, _ := json.Marshal(map[string]any{"command": args, "request_id": id})
	_, err := c.rw.Write(append(b, '\n'))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, errors.New("ipc closed")
		}
		if r.Error != "success" {
			return r.Data, errors.New(r.Error)
		}
		return r.Data, nil
	case <-time.After(2 * time.Second):
		return nil, errors.New("ipc timeout")
	}
}

func (c *mpvClient) getString(p string) string {
	d, err := c.cmd("get_property_string", p)
	if err != nil {
		return "ERR:" + err.Error()
	}
	var s string
	_ = json.Unmarshal(d, &s)
	return s
}

func (c *mpvClient) setPause(p bool) error { _, err := c.cmd("set_property", "pause", p); return err }
func (c *mpvClient) Close() error          { return c.rw.Close() }

// statsLoop 定期记录 hwdec-current / 掉帧 / time-pos。
// 卡住判定（Q-P3 修正）：进度键 = (循环计数 loops, time-pos)。仅当连续 2 次采样进度键完全不变、且未暂停时才判 STALL。
// 旧实现只比较 time-pos，当视频时长恰为采样间隔的整数倍时，每次采样都会落在同一 time-pos → 误报。
// 循环一次必然产生 playback-restart 事件（loops+1），因此“同一 time-pos 但跨了循环”不会再被当作卡住。
func statsLoop(c *mpvClient, every time.Duration, paused func() bool, onStall func(), stop <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	type key struct {
		loops int64
		pos   string
	}
	var last key
	same := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if paused() { // 第 19 轮：暂停/释放期间不查询（L3 时 time-pos 不可用会被误判为 IPC_DEAD；也减少暂停期唤醒）
			same = 0
			last = key{}
			continue
		}
		pos := c.getString("time-pos")
		k := key{c.loops.Load(), pos}
		logf("STATS hwdec-current=%s frame-drop-count=%s decoder-frame-drop-count=%s vo-delayed-frame-count=%s time-pos=%s loops=%d paused=%v",
			c.getString("hwdec-current"), c.getString("frame-drop-count"), c.getString("decoder-frame-drop-count"),
			c.getString("vo-delayed-frame-count"), pos, k.loops, paused())
		if strings.HasPrefix(pos, "ERR:") {
			logf("IPC_DEAD")
			onStall()
			return
		}
		if !paused() && k == last {
			same++
			if same >= 2 {
				logf("STALL 进度 (loops=%d,time-pos=%s) 连续 %d 次未变 → 重启 mpv", k.loops, k.pos, same)
				onStall()
				return
			}
		} else {
			same = 0
		}
		last = k
	}
}

func mpvArgs(wid uintptr, ipc, hwdec string, extra ...string) []string {
	a := []string{
		"--no-config", "--idle=yes", "--force-window=yes",
		"--wid=" + itoa(uint64(wid)),
		"--input-ipc-server=" + ipc,
		"--hwdec=" + hwdec, "--loop-file=inf", "--keep-open=yes", "--no-audio",
		"--no-input-default-bindings", "--input-vo-keyboard=no", "--osc=no", "--osd-level=0",
		"--msg-level=all=warn", "--log-file=" + *flagLog + ".mpv.log",
	}
	a = append(a, extra...)
	return append(a, *flagVideo)
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
