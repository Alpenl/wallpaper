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
	ctl.GOOS = goos
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
	// 设置子页在顶部“设置”标签下；第一次进入先点“设置”。
	pages := []struct{ nav, want string }{
		{"显示器", "布局"}, {"设置", "性能档位"}, {"播放与性能", "性能档位"}, {"暂停与电源", "自动暂停"},
		{"通用", "颜色模式"}, {"版本管理", "当前版本"}, {"关于与诊断", "诊断包"}, {"壁纸库", "全部 6"},
	}
	for _, goos := range []string{"windows", "darwin", "linux"} {
		_, _, tt := newUI(t, goos)
		for _, p := range pages {
			click(t, tt, p.nav)
			must(t, tt, p.want)
		}
		click(t, tt, "设置")
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
	tt.SetSize(1120, 1500)
	s.SelectItem("ocean")
	tt.Frame()
	must(t, tt, "海浪 4K", "建议优化转码", "Wallpaper Engine 导入")
	// 应用作用于标题里的目标显示器（默认主屏），在底部操作栏。
	must(t, tt, "显示器 1", "DELL U2723QE")
	click(t, tt, "应用")
	if v := ctl.View(); v.Displays[0].ItemID != "ocean" || v.Displays[1].ItemID != "city" || v.Default != "aurora" {
		t.Fatal("默认只应用到当前显示器（主屏），不动另一块和默认壁纸", v.Displays, v.Default)
	}
	s.SelectDisplay("d2")
	tt.Frame()
	click(t, tt, "应用")
	if v := ctl.View(); v.Displays[1].ItemID != "ocean" || v.Default != "aurora" {
		t.Fatal("应只应用到 2 号屏", v.Displays)
	}
	s.SelectItem("forest")
	tt.Frame()
	click(t, tt, "应用到所有显示器")
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
	click(t, tt, "屏幕属性")
	must(t, tt, "播放速度", "暂停此屏幕")
	click(t, tt, "壁纸库")
	tt.Advance(5 * time.Second)
	click(t, tt, "从壁纸库删除")
	must(t, tt, "删除「海浪 4K」？")
	click(t, tt, "删除")
	if len(ctl.Removed) != 1 || ctl.Removed[0] != "ocean" {
		t.Fatal("应删除", ctl.Removed)
	}
}

// 声音和播放属性随壁纸组保存；目标显示器的覆盖项可在检查器里清除。
func TestWallpaperGroupProps(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1500)
	s.SelectItem("ocean")
	tt.Frame()
	must(t, tt, "这张壁纸自己的设置", "60%")
	click(t, tt, "播放声音 开关")
	if p := ctl.View().Library[1].Props; p.Audio != "mute" || p.Volume != 60 || p.Speed != 0.75 {
		t.Fatal("sound toggle should only change the group's audio", p)
	}
	if !tt.HasText("60%") {
		t.Fatal("muted group keeps its volume")
	}
	click(t, tt, "恢复默认")
	if p := ctl.View().Library[1].Props; p != (config.ItemProps{}) {
		t.Fatal("reset props", p)
	}
	must(t, tt, "未单独设置，跟随默认静音设置")
	s.SelectDisplay("d2")
	tt.Frame()
	must(t, tt, "2  显示器 2 单独覆盖了：静音")
	click(t, tt, "清除覆盖")
	if o := ctl.View().Displays[1].Options; o.Overrides() {
		t.Fatal("overrides not cleared", o)
	}
	must(t, tt, "已清除覆盖")
	tt.Advance(5 * time.Second)
	if tt.HasText("清除覆盖") {
		t.Fatal("notice should disappear")
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
	s.PickFolder = func() string { return "/wallpapers" }
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 740)
	tt.Frame()
	must(t, tt, "把视频、GIF 或图片拖到这里")
	click(t, tt, "选择文件…")
	if len(ctl.Imported) != 1 {
		t.Fatal(ctl.Imported)
	}
	click(t, tt, "导入文件夹…")
	if len(ctl.Imported) != 2 || ctl.Imported[1] != "/wallpapers" {
		t.Fatal(ctl.Imported)
	}
}

func TestSettingsChangeConfig(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 2200)
	click(t, tt, "设置")
	click(t, tt, "播放与性能")
	click(t, tt, "省电")
	if ctl.View().Config.Perf.Profile != "saver" {
		t.Fatal("省电档")
	}
	click(t, tt, "暂停与电源")
	click(t, tt, "降到 15 fps")
	if ctl.View().Config.Pause.BatteryPolicy != "fps15" {
		t.Fatal("电池 15 fps", ctl.View().Config.Pause.BatteryPolicy)
	}
	click(t, tt, "暂停与电源")
	click(t, tt, "其他程序全屏时暂停 开关")
	if ctl.View().Config.Pause.OnFullscreen {
		t.Fatal("应关闭全屏暂停")
	}
	s.appExecutable = "game.exe"
	tt.Frame()
	click(t, tt, "添加规则")
	if len(ctl.View().Config.Pause.AppRules) != 1 {
		t.Fatal("app rule not saved")
	}
	click(t, tt, "通用")
	click(t, tt, "深色")
	if ctl.View().Config.UI.Theme != "dark" {
		t.Fatal("深色")
	}
	click(t, tt, "紫")
	if ctl.View().Config.UI.Accent != "purple" {
		t.Fatal("强调色")
	}
	click(t, tt, "通用")
	click(t, tt, "开机自动启动 开关")
	if c := ctl.View().Config.Startup; !c.LaunchAtLogin || !c.Asked {
		t.Fatal("开机自启", c)
	}
	click(t, tt, "关于与诊断")
	click(t, tt, "恢复默认")
	tt.Key(0, ui.KeyEnter) // 对话框的默认按钮“恢复默认”
	tt.Frame()
	if c := ctl.View().Config; c.UI.Theme != "system" || c.Perf.Profile != "balanced" || !c.Startup.LaunchAtLogin {
		t.Fatal("恢复默认应保留开机自启、重置其他", c.UI.Theme, c.Perf.Profile)
	}
}

func TestStatusBarPauseAndNext(t *testing.T) {
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
	tt.SetSize(1120, 1500)
	click(t, tt, "显示器")
	must(t, tt, "DELL U2723QE（主屏）", "播放中", "硬解 d3d11va")
	click(t, tt, "2  显示器 2")
	must(t, tt, "2  显示器 2", "暂停原因：其他程序全屏", "系统没有提供名称，按排列顺序编号")
	for _, x := range tt.Texts() {
		if strings.Contains(x, `\\.\`) || strings.Contains(x, "DISPLAY") {
			t.Fatal("不应显示系统内部名", x)
		}
	}
	click(t, tt, "将此屏壁纸复制到所有显示器")
	if v := ctl.View(); v.Default != "city" || v.Displays[0].ItemID != "city" || v.Displays[0].Custom {
		t.Fatal("复制到所有显示器", v.Default, v.Displays)
	}
}

// 重命名：别名只写到正在编辑的那块屏（按 ID），显示在名称位置，可恢复默认。
func TestDisplayRename(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1500)
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
	click(t, tt, "1  DELL U2723QE · 主屏")
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
	ctl, s, tt := newUI(t, "windows")
	ctl.UpdateConfig(func(c *config.Config) { c.Update.FeedURL = "https://example.com/updates" })
	applied := make(chan struct{}, 1)
	s.CheckUpdate = func() (*UpdateInfo, error) { return &UpdateInfo{Version: "0.3.0", Size: 3 << 20}, nil }
	s.ApplyUpdate = func(p func(int64, int64)) error { p(1, 2); applied <- struct{}{}; return nil }
	s.CanRollback = func() (string, bool) { return "0.1.0", true }
	click(t, tt, "设置")
	click(t, tt, "版本管理")
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

func TestDisplayPropertiesApplyOnlyToSelectedDisplay(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1600)
	s.SetPage("displays")
	s.SelectDisplay("d2")
	tt.Frame()
	click(t, tt, "暂停此屏幕 开关")
	if !ctl.View().Displays[1].Options.Paused || ctl.View().Displays[0].Options.Paused {
		t.Fatal("pause changed the wrong display")
	}
	// 默认跟随壁纸；选具体值才写入这块屏的覆盖项。
	click(t, tt, "跟随壁纸")
	click(t, tt, "适应")
	if ctl.View().Displays[1].Options.Fit != "fit" || ctl.View().Displays[0].Options.Fit != "" {
		t.Fatal("fit did not persist to selected display")
	}
	must(t, tt, "填充会裁切边缘，适应保留全图，拉伸铺满屏幕。当前：适应")
	click(t, tt, "跟随全局")
	click(t, tt, "省电")
	if ctl.View().Displays[1].Options.Profile != "saver" {
		t.Fatal("profile not saved")
	}
}

func TestAppRulesValidateAndUpdate(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1800)
	s.SetPage("rules")
	s.appExecutable, s.appAction = "/tmp/game.exe", "暂停"
	if s.addAppRule() {
		t.Fatal("accepted full path")
	}
	s.appExecutable = "game.exe"
	if !s.addAppRule() {
		t.Fatal(s.appError)
	}
	s.appExecutable, s.appAction = "GAME.exe", "省电档"
	if !s.addAppRule() {
		t.Fatal(s.appError)
	}
	rules := ctl.View().Config.Pause.AppRules
	if len(rules) != 1 || rules[0].Action != "saver" {
		t.Fatal(rules)
	}
	tt.Frame()
	click(t, tt, "删除规则 game.exe")
	if len(ctl.View().Config.Pause.AppRules) != 0 {
		t.Fatal("remove rule failed")
	}
}

func TestPlaybackAndStartupArePersistent(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1800)
	s.SetPage("playback")
	tt.Frame()
	click(t, tt, "静音播放 开关")
	click(t, tt, "从上次位置继续播放 开关")
	click(t, tt, "导入时自动转码 开关")
	cfg := ctl.View().Config
	if cfg.Render.Muted || cfg.Render.ResumePosition || !cfg.Render.AutoTranscode {
		t.Fatal(cfg.Render)
	}
	s.SetPage("general")
	tt.Frame()
	click(t, tt, "启动时打开设置窗口 开关")
	if ctl.View().Config.Startup.OpenSettings {
		t.Fatal("startup window setting was not saved")
	}
}

func TestEnglishLanguageSelection(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	language := ""
	s.SetLanguage = func(lang string) { language = lang }
	s.SetPage("general")
	tt.Frame()
	click(t, tt, "简体中文")
	click(t, tt, "English")
	if ctl.View().Config.UI.Lang != "en" || language != "en" {
		t.Fatal(ctl.View().Config.UI.Lang, language)
	}
	tt.SetLocale(language)
	tt.Frame()
	must(t, tt, "General", "Interface language", "English", "Color mode")
	click(t, tt, "Dark")
	if ctl.View().Config.UI.Theme != "dark" {
		t.Fatal("localized theme control failed")
	}
	click(t, tt, "Displays")
	must(t, tt, "Layout", "Playback speed")
}

func TestDiagnosticsExportDoesNotBlockUI(t *testing.T) {
	_, s, tt := newUI(t, "windows")
	begin, finish := make(chan struct{}), make(chan struct{})
	s.ExportDiag = func() (string, error) { close(begin); <-finish; return "/tmp/diagnostic.zip", nil }
	s.SetPage("about")
	tt.Frame()
	click(t, tt, "导出诊断包")
	<-begin
	click(t, tt, "壁纸库")
	must(t, tt, "全部 6")
	close(finish)
	s.SetPage("about")
	wait(t, tt, "诊断包已导出。请检查内容后再分享。")
}

func TestUnavailableUpdatesNeverClaimLatest(t *testing.T) {
	_, s, tt := newUI(t, "windows")
	s.Info.UpdateKey = false
	s.SetPage("versions")
	tt.Frame()
	must(t, tt, "尚未配置更新源。未检查更新，版本状态未知。")
	if tt.HasText("已是最新版本") {
		t.Fatal("unverified latest-version claim")
	}
}

func TestUpdateFeedValidation(t *testing.T) {
	for _, value := range []string{"", "https://updates.example.com/release", "http://localhost:8000/releases", "http://127.0.0.1/test"} {
		if err := validateUpdateFeed(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"http://example.com", "https://name:secret@example.com", "file:///tmp/update", "https://example.com?token=a", "https://example.com/#fragment", "://"} {
		if validateUpdateFeed(value) == nil {
			t.Errorf("accepted invalid source %q", value)
		}
	}
}

func TestSearchIncludesTagsAndAuthors(t *testing.T) {
	ctl, s, _ := newUI(t, "windows")
	ctl.V.Library[0].Author = "Artist Example"
	ctl.V.Library[0].Tags = []string{"northern", "night sky"}
	for _, q := range []string{"artist example", "NIGHT SKY"} {
		s.query = q
		items := s.filtered(ctl.View())
		if len(items) != 1 || items[0].ID != "aurora" {
			t.Fatalf("search %q: %v", q, items)
		}
	}
}

func TestPageAliasesResolveToSevenPages(t *testing.T) {
	_, s, _ := newUI(t, "windows")
	for old, want := range map[string]string{"perf": "playback", "pause": "rules", "power": "rules", "look": "general", "startup": "versions", "diag": "about", "discover": "about"} {
		s.SetPage(old)
		if s.page != want {
			t.Errorf("%s mapped to %s", old, s.page)
		}
	}
}

type queuedController struct {
	*uidemo.Ctl
	pending []func(*config.Config)
}

func (c *queuedController) UpdateConfig(fn func(*config.Config)) { c.pending = append(c.pending, fn) }
func (c *queuedController) flush() {
	for _, fn := range c.pending {
		c.Ctl.UpdateConfig(fn)
	}
	c.pending = nil
}

func TestQueuedRuleCommandsUseStableIdentity(t *testing.T) {
	ctl := &queuedController{Ctl: uidemo.New(nil)}
	ctl.V.Config.Pause.AppRules = []config.AppRule{{Executable: "first", Action: "pause"}, {Executable: "second", Action: "pause"}, {Executable: "third", Action: "pause"}}
	s := NewSettings(ctl, "linux")
	s.removeAppRule("first")
	s.updateAppRule("second", "saver")
	s.removeAppRule("third")
	ctl.flush()
	rules := ctl.View().Config.Pause.AppRules
	if len(rules) != 1 || rules[0].Executable != "second" || rules[0].Action != "saver" {
		t.Fatal(rules)
	}
}

func TestRuleCaseSensitivityMatchesPlatform(t *testing.T) {
	for _, platform := range []string{"windows", "linux", "darwin"} {
		ctl := uidemo.New(nil)
		ctl.GOOS = platform
		s := NewSettings(ctl, platform)
		s.appExecutable, s.appAction = "Firefox", "暂停"
		s.addAppRule()
		s.appExecutable, s.appAction = "firefox", "省电档"
		s.addAppRule()
		want := 2
		if platform == "windows" {
			want = 1
		}
		if got := len(ctl.View().Config.Pause.AppRules); got != want {
			t.Errorf("%s: %d rules, want %d", platform, got, want)
		}
	}
}

func TestMultiFilePickerAndDropForwardEveryPath(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	s.PickFiles = func() []string { return []string{"/tmp/first.mp4", "/tmp/second.png"} }
	click(t, tt, "导入壁纸")
	if len(ctl.Imported) != 2 {
		t.Fatal(ctl.Imported)
	}
	if !tt.DropFiles(400, 400, "/tmp/third.mp4", "/tmp/fourth.png") {
		t.Fatal("drop target did not accept files")
	}
	tt.Frame()
	if len(ctl.Imported) != 4 {
		t.Fatal(ctl.Imported)
	}
}

func TestStartupWaitsForValidConfigBeforeOnboarding(t *testing.T) {
	ctl := uidemo.New(nil)
	cfg := ctl.V.Config
	ctl.V.Config = config.Config{}
	s := NewSettings(ctl, "windows")
	s.SetLaunchAtLogin = func(bool) error { return nil }
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 740)
	tt.Frame()
	must(t, tt, "正在启动…")
	if s.askInit || tt.HasText("开机时自动启动动态壁纸？") {
		t.Fatal("onboarding initialized from empty app snapshot")
	}
	ctl.V.Config = cfg
	tt.Frame()
	if tt.HasText("开机时自动启动动态壁纸？") {
		t.Fatal("asked already-configured user again")
	}
}

func TestQueuedTranscodeDoesNotShowRetry(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	s.SelectItem("ocean")
	ctl.V.Transcoding["ocean"] = "等待转码…"
	tt.Frame()
	must(t, tt, "已加入转码队列，等待当前任务完成。")
	if tt.HasText("一键优化") {
		t.Fatal("queued job can be resubmitted")
	}
}

func TestAppRuleLimitMatchesConfigValidation(t *testing.T) {
	ctl, s, _ := newUI(t, "linux")
	s.appExecutable = strings.Repeat("a", 257)
	if s.addAppRule() {
		t.Fatal("accepted overlong executable")
	}
	for i := 0; i < 64; i++ {
		ctl.V.Config.Pause.AppRules = append(ctl.V.Config.Pause.AppRules, config.AppRule{Executable: itoa(i), Action: "pause"})
	}
	s.appExecutable = "new"
	if s.addAppRule() {
		t.Fatal("accepted rule beyond capacity")
	}
	s.appExecutable, s.appAction = "12", "省电档"
	if !s.addAppRule() {
		t.Fatal("existing rule should update at capacity", s.appError)
	}
	if ctl.View().Config.Pause.AppRules[12].Action != "saver" {
		t.Fatal("existing rule not updated")
	}
}

func TestEnglishDoesNotTranslateImportedTitles(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	ctl.V.Library[0].Title = "通用"
	ctl.V.Library[0].Author = "设置"
	ctl.V.Library[0].Description = "暂停"
	s.SelectItem("aurora")
	tt.SetSize(1120, 1200)
	tt.SetLocale("en")
	tt.Frame()
	must(t, tt, "通用", "设置", "暂停")
}

func TestRollbackRequiresExplicitConfirmation(t *testing.T) {
	_, s, tt := newUI(t, "windows")
	rolled := make(chan struct{}, 1)
	s.CanRollback = func() (string, bool) { return "0.1.0", true }
	s.Rollback = func() error { rolled <- struct{}{}; return nil }
	s.SetPage("versions")
	tt.Frame()
	click(t, tt, "回滚到 0.1.0")
	must(t, tt, "回滚到 0.1.0？")
	select {
	case <-rolled:
		t.Fatal("rollback ran before confirmation")
	default:
	}
	click(t, tt, "取消")
	select {
	case <-rolled:
		t.Fatal("rollback ran after cancel")
	default:
	}
	click(t, tt, "回滚到 0.1.0")
	click(t, tt, "回滚并重启")
	select {
	case <-rolled:
	case <-time.After(time.Second):
		t.Fatal("rollback did not run")
	}
	wait(t, tt, "已回滚，重启应用后生效")
}

type rejectingRemoveController struct {
	*uidemo.Ctl
	removals int
}

func (c *rejectingRemoveController) Remove(string) {
	c.removals++
	c.V.LastError = "删除失败：文件仍被占用"
}

func TestRejectedAsyncRemovalKeepsSelectionAndDoesNotClaimSuccess(t *testing.T) {
	ctl := &rejectingRemoveController{Ctl: uidemo.New(nil)}
	s := NewSettings(ctl, "windows")
	s.SelectItem("ocean")
	tt := ui.NewTester(func(c *ui.Context) { s.Refresh(); s.View(c) }, 1120, 1400)
	tt.SetLocale("zh-CN")
	tt.Frame()
	click(t, tt, "从壁纸库删除")
	click(t, tt, "删除")
	if ctl.removals != 1 || s.selID != "ocean" {
		t.Fatal(ctl.removals, s.selID)
	}
	must(t, tt, "删除请求已提交", "删除失败：文件仍被占用")
	if tt.HasText("已删除「") {
		t.Fatal("claimed async removal succeeded")
	}
	ctl.Ctl.Remove("ocean")
	tt.Frame()
	if s.selID == "ocean" {
		t.Fatal("selection not cleared after snapshot confirms removal")
	}
}

func TestImportingDisablesRemoval(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1400)
	s.SelectItem("ocean")
	ctl.V.Importing = true
	tt.Frame()
	click(t, tt, "从壁纸库删除")
	if s.confirmRm || len(ctl.Removed) != 0 {
		t.Fatal("remove was enabled during import")
	}
	if got := translate("en", "删除请求已提交"); got != "Removal request submitted" {
		t.Fatal(got)
	}
}

func TestPageHeaderKeepsSpacingAtMinimumWindowSize(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			page(c, "Header layout check", "A long page at minimum window size", nil, func() {
				for i := 0; i < 40; i++ {
					ui.Text(c, "Scrollable content").Padding(10)
				}
			})
		})
	}, 900, 600)
	tt.Frame()
	header, ok := tt.Find("Header layout check")
	if !ok || header.Y < 16 {
		t.Fatalf("header collapsed into top padding: %+v", header)
	}
	tt.Scroll(500, 400, 0, -600)
	tt.Frame()
	after, ok := tt.Find("Header layout check")
	if !ok || after.Y != header.Y {
		t.Fatalf("scroll moved page header: before=%+v after=%+v", header, after)
	}
}
