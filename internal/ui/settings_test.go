package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/config"
	"wpapp/internal/ui/uidemo"
)

func newUI(t *testing.T, goos string) (*uidemo.Ctl, *Settings, *ui.Tester) {
	t.Helper()
	ctl := uidemo.New(nil)
	s := NewSettings(ctl, goos)
	s.Info = Info{Version: "0.2.0-test", Platform: goos + "/amd64", EncoderName: "ffmpeg", UpdateKey: true}
	s.PickFiles = func() []string { return []string{"/tmp/x.mp4"} }
	s.ExportDiag = func() (string, error) { return "/tmp/diag.zip", nil }
	s.SetLaunchAtLogin = func(bool) error { return nil }
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 740)
	tt.SetLocale("zh-CN")
	tt.Frame()
	return ctl, s, tt
}

func must(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, w := range texts {
		if !tt.HasText(w) {
			t.Fatalf("缺少 %q；界面文本：%q", w, tt.Texts())
		}
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("点击 %q: %v；界面文本：%q", label, err, tt.Texts())
	}
	tt.Frame()
}

// 每一页都能渲染（三个平台），导航可点击。
func TestAllPagesRender(t *testing.T) {
	pages := []struct{ nav, want string }{
		{"显示器", "布局"}, {"发现与创意工坊", "更多壁纸，即将到来"}, {"播放与性能", "性能档位"}, {"暂停规则", "自动暂停"},
		{"电源与电池", "使用电池时"}, {"外观", "颜色模式"}, {"启动与更新", "开机自动启动"}, {"诊断", "诊断包"}, {"关于", "开源组件与许可"}, {"壁纸库", "共 6 张壁纸 · 拖入文件即可导入"},
	}
	for _, goos := range []string{"windows", "darwin", "linux"} {
		_, _, tt := newUI(t, goos)
		for _, p := range pages {
			click(t, tt, p.nav)
			must(t, tt, p.want)
		}
		click(t, tt, "播放与性能")
		if goos == "windows" {
			must(t, tt, "渲染后端", "硬件解码")
		}
		if goos == "darwin" && tt.HasText("硬件解码") {
			t.Fatal("macOS 不应显示硬件解码选项")
		}
	}
}

func TestLibraryDetailActions(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1100)
	s.SelectItem("ocean")
	tt.Frame()
	must(t, tt, "海浪 4K", "建议优化转码", "Wallpaper Engine 导入")
	must(t, tt, "应用到", "1  DELL U2723QE（主屏）")
	click(t, tt, "应用")
	if v := ctl.View(); v.Displays[0].ItemID != "ocean" || v.Displays[1].ItemID != "city" || v.Default != "aurora" {
		t.Fatal("默认只应用到当前显示器（主屏），不动另一块和默认壁纸", v.Displays, v.Default)
	}
	s.applySel = "2  显示器 2"
	tt.Frame()
	click(t, tt, "应用")
	if v := ctl.View(); v.Displays[1].ItemID != "ocean" || v.Default != "aurora" {
		t.Fatal("应只应用到 2 号屏", v.Displays)
	}
	s.applySel = "所有显示器"
	tt.Frame()
	s.SelectItem("forest")
	tt.Frame()
	s.applySel = "所有显示器"
	tt.Frame()
	click(t, tt, "应用")
	if v := ctl.View(); v.Default != "forest" || v.Displays[0].ItemID != "forest" || v.Displays[1].ItemID != "forest" || v.Displays[0].Custom {
		t.Fatal("应用到所有显示器", v.Displays, v.Default)
	}
	s.SelectItem("ocean")
	tt.Frame()
	click(t, tt, "一键优化")
	if len(ctl.Transc) != 1 || ctl.Transc[0] != "ocean" {
		t.Fatal("应发起转码", ctl.Transc)
	}
	must(t, tt, "正在转码，可以继续使用其他功能…")
	click(t, tt, "属性")
	must(t, tt, "壁纸属性面板（速度、填充方式） 正在开发中")
	tt.Advance(5 * time.Second)
	click(t, tt, "从壁纸库删除")
	must(t, tt, "删除「海浪 4K」？")
	click(t, tt, "删除")
	if len(ctl.Removed) != 1 || ctl.Removed[0] != "ocean" {
		t.Fatal("应删除", ctl.Removed)
	}
}

func TestLibrarySearchFilterImport(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	click(t, tt, "建议转码 2")
	must(t, tt, "海浪 4K", "雨林晨雾")
	if tt.HasText("HEVC · 本地文件") {
		t.Fatal("筛选后不应显示极光之夜")
	}
	click(t, tt, "全部 6")
	s.query = "城市"
	tt.Frame()
	must(t, tt, "城市霓虹")
	if tt.HasText("雪山") {
		t.Fatal("搜索后不应显示雪山")
	}
	s.query = "不存在"
	tt.Frame()
	must(t, tt, "没有符合条件的壁纸")
	s.query = ""
	click(t, tt, "导入壁纸")
	if len(ctl.Imported) != 1 || ctl.Imported[0] != "/tmp/x.mp4" || ctl.ImportTo[0] != "" {
		t.Fatal("应导入选择的文件，且默认只加入库", ctl.Imported, ctl.ImportTo)
	}
	ctl.UpdateConfig(func(c *config.Config) { c.UI.ImportApply = "current" })
	s.SelectDisplay("d2")
	tt.Frame()
	click(t, tt, "导入壁纸")
	if len(ctl.ImportTo) != 2 || ctl.ImportTo[1] != "d2" {
		t.Fatal("设置为应用到当前显示器时只传当前那块", ctl.ImportTo)
	}
	click(t, tt, "列表")
	if ctl.View().Config.UI.LibraryView != "list" {
		t.Fatal("应切到列表视图")
	}
	must(t, tt, "使用中 · 1  DELL U2723QE")
}

func TestEmptyLibrary(t *testing.T) {
	ctl := uidemo.New(nil)
	ctl.V.Library = nil
	s := NewSettings(ctl, "linux")
	s.PickFiles = func() []string { return []string{"/a.gif"} }
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 740)
	tt.Frame()
	must(t, tt, "把视频、GIF 或图片拖到这里")
	click(t, tt, "选择文件…")
	if len(ctl.Imported) != 1 {
		t.Fatal(ctl.Imported)
	}
	click(t, tt, "浏览创意工坊")
	must(t, tt, "创意工坊 正在开发中")
}

func TestSettingsChangeConfig(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	tt.SetSize(1120, 1200)
	click(t, tt, "播放与性能")
	click(t, tt, "省电")
	if ctl.View().Config.Perf.Profile != "saver" {
		t.Fatal("省电档")
	}
	click(t, tt, "电源与电池")
	click(t, tt, "降到 15 fps")
	if ctl.View().Config.Pause.BatteryPolicy != "fps15" {
		t.Fatal("电池 15 fps", ctl.View().Config.Pause.BatteryPolicy)
	}
	click(t, tt, "暂停规则")
	click(t, tt, "其他程序全屏时暂停 开关")
	if ctl.View().Config.Pause.OnFullscreen {
		t.Fatal("应关闭全屏暂停")
	}
	click(t, tt, "添加应用…")
	must(t, tt, "按应用暂停 正在开发中")
	click(t, tt, "外观")
	click(t, tt, "深色")
	if ctl.View().Config.UI.Theme != "dark" {
		t.Fatal("深色")
	}
	click(t, tt, "紫")
	if ctl.View().Config.UI.Accent != "purple" {
		t.Fatal("强调色")
	}
	click(t, tt, "启动与更新")
	click(t, tt, "开机自动启动 开关")
	if c := ctl.View().Config.Startup; !c.LaunchAtLogin || !c.Asked {
		t.Fatal("开机自启", c)
	}
	click(t, tt, "诊断")
	click(t, tt, "恢复默认")
	tt.Key(0, ui.KeyEnter) // 对话框的默认按钮“恢复默认”
	tt.Frame()
	if c := ctl.View().Config; c.UI.Theme != "system" || c.Perf.Profile != "balanced" || !c.Startup.LaunchAtLogin {
		t.Fatal("恢复默认应保留开机自启、重置其他", c.UI.Theme, c.Perf.Profile)
	}
}

func TestSidebarPauseAndNext(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	must(t, tt, "正在播放", "1 块屏播放 · 1 块屏暂停")
	click(t, tt, "暂停全部")
	if !ctl.View().PausedAll {
		t.Fatal("暂停全部")
	}
	must(t, tt, "已全部暂停", "继续播放")
	click(t, tt, "切换到下一张壁纸")
	if ctl.Nexts != 1 {
		t.Fatal("下一张")
	}
}

func TestDisplaysPage(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	tt.SetSize(1120, 1200)
	click(t, tt, "显示器")
	must(t, tt, "DELL U2723QE（主屏）", "播放中", "硬解 d3d11va")
	click(t, tt, "2  显示器 2")
	must(t, tt, "2  显示器 2", "暂停原因：其他程序全屏", "系统没有提供名称，按排列顺序编号")
	for _, x := range tt.Texts() {
		if strings.Contains(x, `\\.\`) || strings.Contains(x, "DISPLAY") {
			t.Fatal("不应显示系统内部名", x)
		}
	}
	click(t, tt, "复制")
	if v := ctl.View(); v.Default != "city" || v.Displays[0].ItemID != "city" || v.Displays[0].Custom {
		t.Fatal("复制到所有显示器", v.Default, v.Displays)
	}
}

// 重命名：别名只写到正在编辑的那块屏（按 ID），显示在名称位置，可恢复默认。
func TestDisplayRename(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1200)
	click(t, tt, "显示器")
	s.SelectDisplay("d2")
	tt.Frame()
	click(t, tt, "重命名")
	if s.renameID != "d2" || s.renameBuf != "显示器 2" {
		t.Fatal("编辑框应预填当前名称", s.renameID, s.renameBuf)
	}
	s.renameBuf = "  书桌左屏 "
	tt.Frame()
	click(t, tt, "保存")
	v := ctl.View()
	if v.Displays[1].Name != "书桌左屏" || v.Displays[1].Alias != "书桌左屏" || v.Displays[0].Name != "DELL U2723QE" || v.Displays[0].Alias != "" {
		t.Fatal("别名应只作用于 2 号屏", v.Displays)
	}
	must(t, tt, "2  书桌左屏", "自定义名称", "恢复默认名称")
	// 编辑一半切到另一块屏：放弃，不会把名字写到 1 号屏
	click(t, tt, "重命名")
	s.renameBuf = "不该保存"
	click(t, tt, "1  DELL U2723QE")
	if s.renameID != "" {
		t.Fatal("切屏应退出编辑")
	}
	if v := ctl.View(); v.Displays[0].Alias != "" {
		t.Fatal(v.Displays[0])
	}
	s.SelectDisplay("d2")
	tt.Frame()
	click(t, tt, "恢复默认名称")
	if v := ctl.View(); v.Displays[1].Name != "显示器 2" || v.Displays[1].Alias != "" {
		t.Fatal(v.Displays[1])
	}
	// 保存与系统名称相同的名字 = 不存别名
	s.SelectDisplay("d1")
	tt.Frame()
	click(t, tt, "重命名")
	click(t, tt, "保存")
	if v := ctl.View(); v.Displays[0].Alias != "" {
		t.Fatal("与系统名称相同不应存别名", v.Displays[0])
	}
}

func TestSafeModeAndError(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	ctl.V.SafeMode, ctl.V.LastError = true, "渲染器启动失败: boom"
	tt.Frame()
	must(t, tt, "安全模式", "渲染器启动失败: boom")
	click(t, tt, "恢复正常")
	if !ctl.ExitSafe {
		t.Fatal("退出安全模式")
	}
}

func TestUpdateFlow(t *testing.T) {
	_, s, tt := newUI(t, "windows")
	applied := make(chan struct{}, 1)
	s.CheckUpdate = func() (*UpdateInfo, error) { return &UpdateInfo{Version: "0.3.0", Size: 3 << 20}, nil }
	s.ApplyUpdate = func(p func(int64, int64)) error { p(1, 2); applied <- struct{}{}; return nil }
	s.CanRollback = func() (string, bool) { return "0.1.0", true }
	click(t, tt, "启动与更新")
	click(t, tt, "检查更新")
	wait(t, tt, "下载并安装 0.3.0")
	must(t, tt, "回滚到 0.1.0", "发现新版本 0.3.0，增量下载约 3.0 MB")
	click(t, tt, "下载并安装 0.3.0")
	<-applied
	wait(t, tt, "更新已安装，重启应用后生效")
	s.CheckUpdate = func() (*UpdateInfo, error) { return nil, errors.New("还没有配置更新源") }
	click(t, tt, "检查更新")
	wait(t, tt, "还没有配置更新源")
}

func wait(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		tt.Frame()
		if tt.HasText(text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %q 超时：%q", text, strings.Join(tt.Texts(), " | "))
}

func TestReasonsText(t *testing.T) {
	if got := ReasonsText("Fullscreen|Battery"); got != "其他程序全屏、使用电池" {
		t.Fatal(got)
	}
	if ReasonsText("") != "" || ReasonsText("X") != "X" {
		t.Fatal()
	}
}

func TestFirstRunAsksLaunchAtLogin(t *testing.T) {
	ctl := uidemo.New(nil)
	ctl.V.Config.Startup.Asked = false
	s := NewSettings(ctl, "windows")
	s.SetLaunchAtLogin = func(bool) error { return nil }
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 740)
	tt.Frame()
	must(t, tt, "开机时自动启动动态壁纸？")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if c := ctl.View().Config.Startup; !c.Asked || !c.LaunchAtLogin {
		t.Fatal("应记录询问结果并开启", c)
	}
	if tt.HasText("开机时自动启动动态壁纸？") {
		t.Fatal("只问一次")
	}
}
