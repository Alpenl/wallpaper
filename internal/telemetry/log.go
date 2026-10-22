// Package telemetry 实现 16 章 16.4 的本地日志：slog JSON 行、异步写、有界队列丢弃不阻塞、滚动、隐私脱敏。
// 远程遥测按 D-15 不做，本包不含任何网络代码。
package telemetry

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 16.4 的参数。
const (
	QueueCap   = 1024
	FlushEvery = 2 * time.Second
	FlushBatch = 64
	MaxFile    = 2 << 20
	KeepFiles  = 5
)

// AsyncWriter 是一个异步、按大小滚动的日志文件写入器。Write 永不阻塞：队列满时丢弃并计数。
type AsyncWriter struct {
	path     string
	ch       chan []byte
	dropped  atomic.Uint64
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
	firstErr error
	exited   chan struct{}
	openFile func() (logFile, error)
	maxSize  int64
	flushCh  chan chan struct{}
}

// NewAsyncWriter 打开 dir/name.log。
func NewAsyncWriter(dir, name string) (*AsyncWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &AsyncWriter{path: filepath.Join(dir, name+".log"), ch: make(chan []byte, QueueCap), done: make(chan struct{}),
		maxSize: MaxFile, exited: make(chan struct{}), flushCh: make(chan chan struct{})}
	f, size, err := w.open()
	if err != nil {
		return nil, err
	}
	// AsyncWriter 拥有写入循环；Close 关闭 done，排空队列并刷盘后退出。
	go w.loop(f, size)
	return w, nil
}

// logFile permits deterministic I/O failure tests.
type logFile interface {
	io.Writer
	Stat() (os.FileInfo, error)
	Close() error
}

func (w *AsyncWriter) record(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.firstErr == nil {
		w.firstErr = err
	}
}

// Err returns the first I/O failure, including final flush and close failures.
func (w *AsyncWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.firstErr
}

func (w *AsyncWriter) open() (logFile, int64, error) {
	open := w.openFile
	if open == nil {
		open = func() (logFile, error) { return os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) }
	}
	f, err := open()
	if err != nil {
		w.record(err)
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		w.record(err)
		w.record(f.Close())
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Write 实现 io.Writer。
func (w *AsyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	b := append([]byte(nil), p...)
	select {
	case w.ch <- b:
	default:
		w.dropped.Add(1)
	}
	return len(p), nil
}

// Dropped 返回队列满或 I/O 失败后未写入的丢弃条数；不包括缓冲刷写失败中无法确认的条目。
func (w *AsyncWriter) Dropped() uint64 { return w.dropped.Load() }

// Sync 等待已入队的条目落盘（测试与退出时使用）。
func (w *AsyncWriter) Sync() {
	ack := make(chan struct{})
	select {
	case w.flushCh <- ack:
		<-ack
	case <-w.done:
		<-w.exited
	}
}

// Close 刷盘并关闭。
func (w *AsyncWriter) Close() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.done)
	}
	w.mu.Unlock()
	<-w.exited
	return w.Err()
}

func (w *AsyncWriter) loop(f logFile, size int64) {
	t := time.NewTicker(FlushEvery)
	defer t.Stop()
	w.loopTicks(f, size, t.C)
}

// loopTicks allows tests to advance the retry clock without wall-clock sleeps.
func (w *AsyncWriter) loopTicks(f logFile, size int64, ticks <-chan time.Time) {
	defer close(w.exited)
	bw := bufio.NewWriterSize(f, 32<<10)
	n := 0
	// bufio retains I/O errors until Reset; keep firstErr only for reporting.
	flush := func() {
		if f != nil {
			if err := bw.Flush(); err != nil {
				w.record(err)
				bw.Reset(f)
			}
		}
		n = 0
	}
	write := func(b []byte) {
		if f == nil {
			w.dropped.Add(1)
			return
		}
		if size+int64(len(b)) > w.maxSize && size > 0 {
			flush()
			w.record(f.Close())
			w.record(rotate(w.path, KeepFiles))
			var err error
			if f, size, err = w.open(); err != nil {
				w.dropped.Add(1)
				return
			}
			bw.Reset(f)
		}
		k, err := bw.Write(b)
		w.record(err)
		if err != nil {
			w.dropped.Add(1)
			bw.Reset(f)
		}
		size += int64(k)
		n++
		if n >= FlushBatch {
			flush()
		}
	}
	drain := func() {
		for {
			select {
			case b := <-w.ch:
				write(b)
			default:
				return
			}
		}
	}
	for {
		select {
		case b := <-w.ch:
			write(b)
		case <-ticks:
			if f == nil {
				// A failed reopen is retried only on the periodic flush tick.
				f, size, _ = w.open()
				if f != nil {
					bw.Reset(f)
				}
			}
			flush()
		case ack := <-w.flushCh:
			drain()
			flush()
			close(ack)
		case <-w.done:
			drain()
			flush()
			if f != nil {
				w.record(f.Close())
			}
			return
		}
	}
}

// rotate：x.log → x.1.log → … → x.(keep-1).log；总文件数 ≤ keep。
func rotate(path string, keep int) error {
	check := func(err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	base := strings.TrimSuffix(path, ".log")
	if err := check(os.Remove(fmt.Sprintf("%s.%d.log", base, keep-1))); err != nil {
		return err
	}
	for i := keep - 2; i >= 1; i-- {
		if err := check(os.Rename(fmt.Sprintf("%s.%d.log", base, i), fmt.Sprintf("%s.%d.log", base, i+1))); err != nil {
			return err
		}
	}
	return check(os.Rename(path, base+".1.log"))
}

// NewLogger 返回写到 AsyncWriter 的 JSON slog.Logger。debug=true 时级别为 Debug。
func NewLogger(w io.Writer, debug bool) *slog.Logger {
	lv := slog.LevelInfo
	if debug {
		lv = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv}))
}

// HashPath 按 16.4 隐私规则 2：素材路径只记录 sha256(path)[:8]。
func HashPath(p string) string {
	h := sha256.Sum256([]byte(p))
	return hex.EncodeToString(h[:])[:8]
}

// Scrub 按隐私规则 3：把主目录替换成 ~。
func Scrub(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	s = strings.ReplaceAll(s, home, "~")
	if strings.Contains(home, `\`) {
		s = strings.ReplaceAll(s, strings.ReplaceAll(home, `\`, "/"), "~")
	}
	return s
}
