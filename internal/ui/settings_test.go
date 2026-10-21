package ui

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

type fakeCtl struct {
	mu       sync.Mutex
	v        app.View
	imported []string
	assigned map[string]string
	paused   bool
	removed  []string
	cfg      config.Config
	exitSafe bool
}

func (f *fakeCtl) View() app.View                       { f.mu.Lock(); defer f.mu.Unlock(); return f.v }
func (f *fakeCtl) SetPausedAll(b bool)                  { f.paused = b }
func (f *fakeCtl) Assign(d, i string)                   { f.assigned[d] = i }
func (f *fakeCtl) Import(p string)                      { f.imported = append(f.imported, p) }
func (f *fakeCtl) Remove(id string)                     { f.removed = append(f.removed, id) }
func (f *fakeCtl) ExitSafeMode()                        { f.exitSafe = true }
func (f *fakeCtl) UpdateConfig(fn func(*config.Config)) { fn(&f.cfg) }

func newFake() *fakeCtl {
	return &fakeCtl{assigned: map[string]string{}, cfg: config.Defaults(), v: app.View{
		Config:   config.Defaults(),
		SafeMode: true,
		Library:  []config.Item{{ID: "a1", Title: "海浪", Type: "video", Origin: "file", Codec: "h264"}},
		Displays: []app.DisplayView{{ID: "w-1", Name: "DISPLAY1", W: 1920, H: 1080, Primary: true, State: "idle", Level: "L0"}},
	}}
}

func TestSettingsView(t *testing.T) {
	f := newFake()
	s := NewSettings(f, "windows")
	s.PickFiles = func() []string { return []string{"/tmp/x.mp4"} }
	tt := ui.NewTester(s.View, 640, 520)
	for _, want := range []string{"海浪", "壁纸库", "安全模式：连续多次异常退出，壁纸未自动启动。"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("恢复正常"); err != nil || !f.exitSafe {
		t.Fatal("exit safe", err)
	}
	if err := tt.Click("选择文件导入…"); err != nil || len(f.imported) != 1 {
		t.Fatal("pick", err, f.imported)
	}
	if err := tt.Click("删除 海浪"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("确认删除"); err != nil || len(f.removed) != 1 {
		t.Fatal("remove", err)
	}
	// 显示器页
	if err := tt.Click("显示器"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("DISPLAY1（主屏）  1920×1080") {
		t.Fatalf("display tab: %q", tt.Texts())
	}
	// 设置页
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("暂停全部壁纸 开关"); err != nil || !f.paused {
		t.Fatal("pause all", err, f.paused)
	}
	if err := tt.Click("其他程序全屏时暂停 开关"); err != nil || f.cfg.Pause.OnFullscreen {
		t.Fatal("toggle fullscreen", err)
	}
	s.ExportDiag = func() (string, error) { return "/tmp/d.zip", nil }
	if err := tt.Click("导出诊断包"); err != nil || !tt.HasText("已导出：/tmp/d.zip") {
		t.Fatal("diag", err)
	}
	if !tt.HasText("Windows 渲染后端（切换后重启壁纸）") {
		t.Fatal("backend row")
	}
}

func TestDropImports(t *testing.T) {
	f := newFake()
	f.v.SafeMode = false
	s := NewSettings(f, "linux")
	tt := ui.NewTester(s.View, 640, 520)
	ok := false
	for y := float32(40); y < 200 && !ok; y += 10 {
		ok = tt.DropFiles(320, y, "/tmp/a.mov")
	}
	if !ok || len(f.imported) != 1 || f.imported[0] != "/tmp/a.mov" {
		t.Fatal("drop", ok, f.imported)
	}
}

func TestIDOf(t *testing.T) {
	if idOf("海浪 [a1]") != "a1" || idOf(none) != "" {
		t.Fatal()
	}
}
