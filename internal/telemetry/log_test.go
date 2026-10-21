package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
