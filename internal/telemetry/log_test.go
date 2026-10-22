package telemetry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAsyncWriteAndRotate(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAsyncWriter(dir, "app")
	if err != nil {
		t.Fatal(err)
	}
	w.maxSize = 1000
	lg := NewLogger(w, false)
	for i := 0; i < 200; i++ {
		lg.Info("transition", "display", i, "to", "L1")
	}
	lg.Debug("hidden")
	w.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "app*.log"))
	if len(files) != KeepFiles {
		t.Fatalf("应保留 %d 个文件，实际 %v", KeepFiles, files)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app.log"))
	if !strings.Contains(string(b), `"msg":"transition"`) || strings.Contains(string(b), "hidden") {
		t.Fatal(string(b))
	}
	for _, f := range files {
		st, _ := os.Stat(f)
		if st.Size() > 1200 {
			t.Fatal("单文件超过上限", f, st.Size())
		}
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("关闭后写应报错")
	}
}

func TestDropWhenFull(t *testing.T) {
	w := &AsyncWriter{ch: make(chan []byte, 2), done: make(chan struct{})}
	for i := 0; i < 5; i++ {
		if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
			t.Fatal(n, err)
		}
	}
	if w.Dropped() != 3 {
		t.Fatal("队列满应丢弃且不阻塞", w.Dropped())
	}
}

func TestPrivacy(t *testing.T) {
	if len(HashPath("/a/b.mp4")) != 8 || HashPath("x") == HashPath("y") {
		t.Fatal()
	}
	home, _ := os.UserHomeDir()
	if got := Scrub(home + "/Videos/x.mp4"); !strings.HasPrefix(got, "~") {
		t.Fatal(got)
	}
}

// Failure injection is per writer, so tests do not replace process globals.
type failingLogFile struct {
	*os.File
	writeErr, statErr, closeErr error
	failOnce                    bool
}

func (f *failingLogFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		err := f.writeErr
		if f.failOnce {
			f.writeErr = nil
		}
		return 0, err
	}
	return f.File.Write(p)
}
func (f *failingLogFile) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}
func (f *failingLogFile) Close() error {
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}
func TestLoggerIOFailures(t *testing.T) {
	for _, stage := range []string{"stat", "write", "flush", "reopen", "close"} {
		t.Run(stage, func(t *testing.T) {
			want := errors.New(stage + " failure")
			w := &AsyncWriter{path: filepath.Join(t.TempDir(), "app.log"), ch: make(chan []byte, QueueCap), done: make(chan struct{}), exited: make(chan struct{}), flushCh: make(chan chan struct{}), maxSize: MaxFile}
			opens := 0
			w.openFile = func() (logFile, error) {
				opens++
				if stage == "reopen" && opens > 1 {
					return nil, want
				}
				f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					return nil, err
				}
				ff := &failingLogFile{File: f}
				switch stage {
				case "stat":
					ff.statErr = want
				case "write", "flush":
					ff.writeErr = want
					ff.closeErr = errors.New("secondary close failure")
				case "close":
					ff.closeErr = want
				}
				return ff, nil
			}
			f, size, err := w.open()
			if stage == "stat" {
				if !errors.Is(err, want) || !errors.Is(w.Err(), want) {
					t.Fatal(err, w.Err())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage == "reopen" {
				w.maxSize = 1
			}
			go w.loop(f, size)
			payload := "x"
			if stage == "write" {
				payload = strings.Repeat("x", 64<<10)
			}
			w.Write([]byte(payload))
			w.Write([]byte("y"))
			w.Sync()
			if err := w.Close(); !errors.Is(err, want) {
				t.Fatalf("Close = %v", err)
			}
			if !errors.Is(w.Err(), want) {
				t.Fatal(w.Err())
			}
			if stage == "reopen" && w.Dropped() != 1 {
				t.Fatal(w.Dropped())
			}
			if err := w.Close(); !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
}
func TestLoggerConcurrentCloseWriteSync(t *testing.T) {
	w, err := NewAsyncWriter(t.TempDir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				switch i % 3 {
				case 0:
					w.Write([]byte("record\n"))
				case 1:
					w.Sync()
				case 2:
					if err := w.Close(); err != nil {
						t.Error(err)
					}
				}
			}
		}(i)
	}
	close(start)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent close did not finish")
	}
	select {
	case <-w.exited:
	default:
		t.Fatal("writer not joined")
	}
	if _, err := w.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestCloseDrainsAcceptedRecords(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAsyncWriter(dir, "app")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err = w.Write([]byte("record\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "record\n") != 100 || w.Dropped() != 0 {
		t.Fatal("Close did not drain all accepted records")
	}
}

// Both direct writes and buffered flushes must recover from a transient error.
func TestLoggerRecoversAfterIOError(t *testing.T) {
	for _, payload := range []string{"buffered", strings.Repeat("x", 64<<10)} {
		t.Run(fmt.Sprint(len(payload)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			want := errors.New("transient write failure")
			w := &AsyncWriter{path: path, ch: make(chan []byte, QueueCap), done: make(chan struct{}), exited: make(chan struct{}), flushCh: make(chan chan struct{}), maxSize: MaxFile}
			go w.loop(&failingLogFile{File: f, writeErr: want, failOnce: true}, 0)
			defer w.Close()
			w.Write([]byte(payload))
			w.Sync()
			dropped := w.Dropped()
			w.Write([]byte("recovered\n"))
			w.Sync()
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "recovered\n" {
				t.Fatalf("got=%q err=%v", got, err)
			}
			if !errors.Is(w.Err(), want) || w.Dropped() != dropped {
				t.Fatalf("err=%v drops=%d", w.Err(), w.Dropped())
			}
			if len(payload) > 32<<10 && dropped != 1 {
				t.Fatalf("failed record drops=%d", dropped)
			}
		})
	}
}

func TestLoggerReopenRetriesOnlyOnTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w := &AsyncWriter{path: path, ch: make(chan []byte, QueueCap), done: make(chan struct{}), exited: make(chan struct{}), flushCh: make(chan chan struct{}), maxSize: 1}
	want := errors.New("reopen failure")
	opens := 0
	w.openFile = func() (logFile, error) {
		opens++
		if opens == 2 || opens == 3 {
			return nil, want
		}
		return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	}
	f, size, err := w.open()
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	go w.loopTicks(f, size, ticks)
	defer w.Close()
	w.Write([]byte("x"))
	w.Write([]byte("y"))
	w.Sync()
	if opens != 2 || w.Dropped() != 1 {
		t.Fatalf("opens=%d drops=%d", opens, w.Dropped())
	}
	for i := 0; i < 10; i++ {
		w.Write([]byte("z"))
		w.Sync()
	}
	if opens != 2 || w.Dropped() != 11 {
		t.Fatalf("opens=%d drops=%d", opens, w.Dropped())
	}
	ticks <- time.Now()
	w.Sync()
	if opens != 3 {
		t.Fatalf("opens=%d", opens)
	}
	w.Write([]byte("z"))
	w.Sync()
	if opens != 3 || w.Dropped() != 12 {
		t.Fatalf("opens=%d drops=%d", opens, w.Dropped())
	}
	ticks <- time.Now()
	w.Sync()
	w.Write([]byte("recovered"))
	w.Sync()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "recovered" || opens != 4 || w.Dropped() != 12 || !errors.Is(w.Err(), want) {
		t.Fatalf("got=%q read=%v opens=%d drops=%d err=%v", got, err, opens, w.Dropped(), w.Err())
	}
}
