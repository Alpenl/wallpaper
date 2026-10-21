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
	path    string
	ch      chan []byte
	dropped atomic.Uint64
	done    chan struct{}
	once    sync.Once
	maxSize int64
	flushCh chan chan struct{}
}

// NewAsyncWriter 打开 dir/name.log。
func NewAsyncWriter(dir, name string) (*AsyncWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &AsyncWriter{path: filepath.Join(dir, name+".log"), ch: make(chan []byte, QueueCap), done: make(chan struct{}),
		maxSize: MaxFile, flushCh: make(chan chan struct{})}
	f, size, err := w.open()
	if err != nil {
		return nil, err
	}
	go w.loop(f, size)
	return w, nil
}

func (w *AsyncWriter) open() (*os.File, int64, error) {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, err
	}
	st, _ := f.Stat()
	return f, st.Size(), nil
}

// Write 实现 io.Writer。
func (w *AsyncWriter) Write(p []byte) (int, error) {
	b := append([]byte(nil), p...)
	select {
	case <-w.done:
		return 0, io.ErrClosedPipe
	default:
	}
	select {
	case w.ch <- b:
	default:
		w.dropped.Add(1)
	}
	return len(p), nil
}

// Dropped 返回因队列满被丢弃的条数。
func (w *AsyncWriter) Dropped() uint64 { return w.dropped.Load() }

// Sync 等待已入队的条目落盘（测试与退出时使用）。
func (w *AsyncWriter) Sync() {
	ack := make(chan struct{})
	select {
	case w.flushCh <- ack:
		<-ack
	case <-w.done:
	}
}

// Close 刷盘并关闭。
func (w *AsyncWriter) Close() error {
	w.Sync()
	w.once.Do(func() { close(w.done) })
	return nil
}

func (w *AsyncWriter) loop(f *os.File, size int64) {
	bw := bufio.NewWriterSize(f, 32<<10)
	t := time.NewTicker(FlushEvery)
	defer t.Stop()
	n := 0
	write := func(b []byte) {
		if size+int64(len(b)) > w.maxSize && size > 0 {
			bw.Flush()
			f.Close()
			rotate(w.path, KeepFiles)
			var err error
			if f, size, err = w.open(); err != nil {
				return
			}
			bw.Reset(f)
		}
		k, _ := bw.Write(b)
		size += int64(k)
		n++
		if n >= FlushBatch {
			bw.Flush()
			n = 0
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
		case <-t.C:
			bw.Flush()
			n = 0
		case ack := <-w.flushCh:
			drain()
			bw.Flush()
			close(ack)
		case <-w.done:
			drain()
			bw.Flush()
			f.Close()
			return
		}
	}
}

// rotate：x.log → x.1.log → … → x.(keep-1).log；总文件数 ≤ keep。
func rotate(path string, keep int) {
	base := strings.TrimSuffix(path, ".log")
	os.Remove(fmt.Sprintf("%s.%d.log", base, keep-1))
	for i := keep - 2; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d.log", base, i), fmt.Sprintf("%s.%d.log", base, i+1))
	}
	os.Rename(path, base+".1.log")
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
