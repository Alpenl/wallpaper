package mpv

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func TestStallDetector(t *testing.T) {
	var d stallDetector
	steps := []struct {
		paused bool
		loops  int64
		pos    float64
		want   bool
	}{
		{false, 0, 1, false}, {false, 0, 2, false}, {false, 0, 2, false}, {false, 0, 2, true},
	}
	for i, s := range steps {
		if got := d.observe(s.paused, s.loops, s.pos); got != s.want {
			t.Fatal(i, got)
		}
	}
	// 循环：同一 time-pos 但 loops 变了 → 不是卡住（Q-P3）
	d = stallDetector{}
	for i, l := range []int64{0, 1, 2, 3} {
		if d.observe(false, l, 5) {
			t.Fatal("循环被误判为卡住", i)
		}
	}
	// 暂停期间清零
	d = stallDetector{}
	d.observe(false, 0, 1)
	d.observe(false, 0, 1)
	d.observe(true, 0, 0)
	if d.observe(false, 0, 1) || d.observe(false, 0, 1) {
		t.Fatal("暂停后应重新计数")
	}
}

func TestArgs(t *testing.T) {
	a := Args(Options{LogFile: "/tmp/m.log"}, 1234, "/tmp/s", "/v.mp4", true, 3.5)
	j := strings.Join(a, " ")
	for _, want := range []string{"--no-config", "--wid=1234", "--input-ipc-server=/tmp/s", "--loop-file=inf", "--no-audio", "--pause=yes", "--start=3.500", "--log-file=/tmp/m.log", "-- /v.mp4"} {
		if !strings.Contains(j, want) {
			t.Error(want, j)
		}
	}
}

type fakeSurface struct{}

func (fakeSurface) Native() uintptr         { return 0 }
func (fakeSurface) Bounds() image.Rectangle { return image.Rect(0, 0, 64, 36) }
func (fakeSurface) SetVisible(bool) error   { return nil }
func (fakeSurface) Close() error            { return nil }

// 用 box 上的真实 mpv（--vo=null）跑一遍生命周期；没有 mpv 时跳过。
func TestRealMpvLifecycle(t *testing.T) {
	path, err := exec.LookPath("mpv")
	if err != nil {
		t.Skip("mpv not installed")
	}
	video, _ := filepath.Abs(filepath.Join("..", "..", "media", "testdata", "h264_audio.mp4"))
	r := New(Options{Path: path, StatsEvery: 200 * time.Millisecond, ExtraArgs: []string{"--vo=null"}, Hwdec: "no"})
	if err := r.Load(render.Item{Path: video}, fakeSurface{}, false); err != nil {
		t.Fatal(err)
	}
	if !r.WaitFirstFrame(5 * time.Second) {
		t.Fatal("没有收到 playback-restart")
	}
	time.Sleep(1500 * time.Millisecond) // 1 s 的视频，至少循环一次
	if st := r.Stats(); st.Loops < 2 || st.Backend != "mpv" {
		t.Fatalf("应已循环: %+v", st)
	}
	for _, l := range []visibility.Level{visibility.L1, visibility.L3, visibility.L0, visibility.L1, visibility.L0} {
		if err := r.SetLevel(l); err != nil {
			t.Fatal(l, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	select {
	case err := <-r.Done():
		t.Fatal("不应报告意外退出", err)
	default:
	}
	// 意外退出 → Done
	r.mu.Lock()
	r.cmd.Process.Kill()
	r.mu.Unlock()
	select {
	case <-r.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("kill 后应收到 Done")
	}
	r.Close()
	// 有意关闭不触发 Done
	r2 := New(Options{Path: path, ExtraArgs: []string{"--vo=null"}, Hwdec: "no"})
	if err := r2.Load(render.Item{Path: video}, fakeSurface{}, true); err != nil {
		t.Fatal(err)
	}
	r2.Close()
	select {
	case err := <-r2.Done():
		t.Fatal(err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := r2.SetLevel(visibility.L0); err == nil {
		t.Fatal("关闭后应报错")
	}
	_ = os.Remove
}

func TestFirstFrame(t *testing.T) {
	p, err := exec.LookPath("mpv")
	if err != nil {
		t.Skip("mpv not installed")
	}
	dst := filepath.Join(t.TempDir(), "first.png")
	if err := FirstFrame(p, "../../media/testdata/h264_audio.mp4", dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatal("not png")
	}
}
