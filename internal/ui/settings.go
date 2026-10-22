// Package ui 是 MyGo 原生 UI（不用 WebView）：托盘（F-09）+ 设置窗口。
//
// 设置窗口参照 Wallpaper Engine 以内容为中心（01 章竞品分析）：顶部细导航条（壁纸库 / 显示器 / 设置），
// 中间是壁纸组网格 + 选中后出现的检查器，底部是每块屏的“正在播放”条（兼作目标显示器选择）。
//   - 壁纸库：缩略图网格 / 列表、搜索、类型筛选、详情面板（预览、元数据、应用到显示器、一键转码 F-07、删除）、拖入导入（F-06/F-08/F-11）
//   - 显示器：按真实坐标画的布局示意图、每屏分配（F-03）、运行状态徽章（级别 L0–L4、暂停原因、后端、硬解、限帧，F-17 雏形）
//   - 播放与性能：性能档位（流畅/均衡/省电 15 fps）、Windows 渲染后端（D-04）、硬件解码、转码编码器（F-07）
//   - 暂停规则（F-04）、电源与电池（F-05：电池策略含 15 fps 档、低电量阈值、macOS 低电量模式 / 过热）
//   - 外观（浅色 / 深色 / 跟随系统、强调色）、启动与更新（F-10、F-15 增量更新与回滚）、诊断（F-16）、关于
//
// 本地能力连接真实控制器；网页 / Shader / 创意工坊在能力说明中明确标注边界。
// 视图函数与 MyGo 窗口 / 托盘分离，可以用 ui.NewTester 测试和截图（settings_test.go、cmd/uishots）。
package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

// Controller 是视图对编排器的依赖（*app.App 满足）。
type Controller interface {
	View() app.View
	SetPausedAll(bool)
	Assign(display, itemID string) // 单块屏；display 为空 = 只改默认壁纸
	AssignAll(itemID string)       // 所有显示器（清除单独设置）
	SetAlias(display, alias string)
	SetDisplayOptions(display string, options config.DisplayOptions) // 空 Fit/Speed/Audio = 跟随壁纸
	SetItemProps(itemID string, props config.ItemProps)              // 壁纸组自带的播放属性
	SetLayout(layout, itemID string)
	Import(path, applyTo string) // applyTo：空 = 只加入库；显示器 ID = 同时应用到这块屏
	CancelImports()
	Remove(itemID string)
	UpdateConfig(func(*config.Config))
	ExitSafeMode()
	Next()
	Transcode(itemID string)
	OptimizeDisplays(itemID string)
	CancelRenditions(itemID string)
	RefreshStorage()
	ClearCache()
}

// UpdateInfo 是“检查更新”的结果。
type UpdateInfo struct {
	Version string
	Notes   string
	Size    int64 // 需要下载的字节数（增量）
}

// Hooks 是由 UI 宿主（cmd/wallpaperd）注入的平台能力；nil 表示不支持，界面会给出说明。
type Hooks struct {
	PickFiles        func() []string
	PickFolder       func() string
	SetLanguage      func(string)
	SetLaunchAtLogin func(bool) error
	ExportDiag       func() (string, error) // F-16：返回 zip 路径
	OpenPath         func(string) error
	ShowInFolder     func(string)
	CheckUpdate      func() (*UpdateInfo, error)
	ApplyUpdate      func(progress func(done, total int64)) error
	Rollback         func() error
	CanRollback      func() (string, bool)
	Restart          func()
}

// Info 是关于页和诊断页的静态信息。
type Info struct {
	Version     string
	Platform    string // windows/amd64 …
	EncoderName string // 转码编码器显示名；空 = 没有
	MpvPath     string
	DataDir     string
	LogsDir     string
	SignedBuild bool
	UpdateKey   bool // 是否编译进了更新公钥
}

const (
	pgLibrary  = "library"
	pgDisplays = "displays"
	pgDiscover = "discover"
	pgPerf     = "playback"
	pgPause    = "rules"
	pgPower    = "power"
	pgLook     = "general"
	pgStartup  = "versions"
	pgDiag     = "diag"
	pgAbout    = "about"
)

// Settings 是设置窗口的状态（只在 MyGo 主线程上读写；snap 和 upd 例外，加锁）。
type Settings struct {
	ctl  Controller
	GOOS string
	Hooks
	Info Info
	// Invalidate 由 Shell 设置：后台任务完成后请求重绘。
	Invalidate func()

	mu   sync.Mutex
	snap app.View
	upd  updState

	page            string
	lastSettings    string // 上次打开的设置子页，顶部“设置”标签回到这里
	query           string
	filter          int
	gridSel         int
	grid            ui.GridState
	list            ui.ListState
	selID           string
	confirmRm       bool
	dispSel         string
	sortBy          string // 壁纸库排序：空 = 最近导入，name = 名称
	renameID        string // 正在重命名的显示器 ID（空 = 不在编辑）
	renameBuf       string
	diagMsg         string
	thumbs          map[string]thumb
	ambients        map[string]ambientBitmap
	themeKey        string
	theme           *ui.Theme
	resetAsk        bool
	ffmpeg          string
	mpvPath         string
	mpvPathInit     bool
	mpvPathError    string
	feed            string
	feedInit        bool
	ffInit          bool
	askLogin        bool
	askInit         bool
	language        string
	appExecutable   string
	appAction       string
	appError        string
	levelsOpen      bool
	diagBusy        bool
	diagPath        string
	rollbackAsk     bool
	rollbackVersion string
	confirmCache    bool
	pickerStatus    string
}

type thumb struct {
	bm  *ui.Bitmap
	mod time.Time
}

type updState struct {
	busy      bool
	installed bool
	msg       string
	info      *UpdateInfo
	progress  float64
}

func NewSettings(ctl Controller, goos string) *Settings {
	s := &Settings{ctl: ctl, GOOS: goos, page: pgLibrary, lastSettings: pgPerf, gridSel: -1, thumbs: map[string]thumb{}}
	s.grid.Selected = &s.gridSel
	s.Refresh()
	return s
}

// Refresh 从编排器取最新快照（任意 goroutine）。
func (s *Settings) Refresh() {
	v := s.ctl.View()
	s.mu.Lock()
	s.snap = v
	s.mu.Unlock()
}

func (s *Settings) view0() app.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *Settings) invalidate() {
	if s.Invalidate != nil {
		s.Invalidate()
	}
}

// SetPage 切换页面（托盘“打开壁纸库/设置”用）。
func (s *Settings) SetPage(p string) {
	switch p {
	case "perf":
		p = pgPerf
	case "pause", "power":
		p = pgPause
	case "look":
		p = pgLook
	case "startup":
		p = pgStartup
	case "diag", "discover":
		p = pgAbout
	}
	s.page = p
}

// thumbOf 返回条目的缩略图（首帧截图 PNG，按修改时间缓存）。
func (s *Settings) thumbOf(it config.Item) *ui.Bitmap {
	if it.Snapshot == "" {
		return nil
	}
	st, err := os.Stat(it.Snapshot)
	if err != nil {
		return nil
	}
	if t, ok := s.thumbs[it.ID]; ok && t.mod.Equal(st.ModTime()) {
		return t.bm
	}
	b, err := os.ReadFile(it.Snapshot)
	if err != nil {
		return nil
	}
	bm, err := ui.DecodeBitmap(b)
	if err != nil {
		return nil
	}
	s.thumbs[it.ID] = thumb{bm: bm, mod: st.ModTime()}
	return bm
}

// View 是 MyGo 视图函数。
func (s *Settings) View(c *ui.Context) {
	v := s.view0()
	// The app publishes its first valid snapshot after desktop initialization.
	// Do not expose default-valued controls or repeat onboarding before then.
	if v.Config.SchemaVersion == 0 {
		ui.Column(c.Key("starting")).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
			ui.Spinner(c).Size(24, 24)
			text(c, "正在启动…")
		})
		return
	}
	if s.language != v.Config.UI.Lang {
		s.language = v.Config.UI.Lang
		if s.SetLanguage != nil {
			s.SetLanguage(s.language)
		}
	}
	s.applyTheme(c, v.Config.UI)
	t := c.Theme()
	// 布局参照 Wallpaper Engine：顶部是目标显示器标题与标签条，中间是内容，壁纸库右侧是属性栏，底部是操作栏。
	ui.Column(c.Key("root")).Fill().Clip().Background(t.Background).Children(func() {
		s.frame(c, v)
	})
	s.firstRun(c, v)
}

// frame 是窗口内容：标题与标签、页面、底部操作栏。
func (s *Settings) frame(c *ui.Context, v app.View) {
	s.topBar(c, v)
	if isSettingsPage(s.page) {
		s.settingsTabs(c)
	}
	s.banners(c, v)
	ui.Column(c.Key("main")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
		switch s.page {
		case pgLibrary:
			s.library(c, v)
		case pgDisplays:
			s.displays(c, v)
		case pgPerf:
			s.perf(c, v)
		case pgPause:
			s.pause(c, v)
		case pgLook:
			s.look(c, v)
		case pgStartup:
			s.startup(c, v)
		default:
			s.about(c, v)
		}
	})
	s.bottomBar(c, v)
}

// firstRun：F-10 默认不开机自启，首次运行时询问一次。
func (s *Settings) firstRun(c *ui.Context, v app.View) {
	if !s.askInit {
		s.askInit = true
		s.askLogin = !v.Config.Startup.Asked && s.SetLaunchAtLogin != nil
	}
	switch alertDialog(c.Key("asklogin"), &s.askLogin, "开机时自动启动动态壁纸？", "开启后登录系统时自动在后台运行并显示壁纸，可以随时在“通用”里关闭。", "暂不", "开机启动") {
	case 0, ui.AlertDismissed:
		s.ctl.UpdateConfig(func(x *config.Config) { x.Startup.Asked = true })
	case 1:
		s.ctl.UpdateConfig(func(x *config.Config) { x.Startup.Asked = true; x.Startup.LaunchAtLogin = true })
	}
}

func (s *Settings) banners(c *ui.Context, v app.View) {
	t := c.Theme()
	if !v.SafeMode && v.LastError == "" && len(v.ReadOnly) == 0 {
		return
	}
	ui.Column(c.Key("banners")).Padding(12, 24, 0, 24).Gap(8).Children(func() {
		if v.SafeMode {
			banner(c.Key("safe"), t.Warning, "安全模式", "连续多次异常退出，壁纸没有自动启动。确认问题已解决后再恢复。", func() {
				if primaryButton(c.Key("exitsafe"), "恢复正常").Clicked() {
					s.ctl.ExitSafeMode()
				}
			})
		}
		for _, f := range v.ReadOnly {
			banner(c.Key("ro"+f), t.Warning, "配置只读", f+" 由更新版本写入，本版本只读取，不会覆盖。", nil)
		}
		if v.LastError != "" {
			banner(c.Key("err"), t.Danger, "出现问题", v.LastError, nil)
		}
	})
}

func banner(c *ui.Context, col ui.Color, title, msg string, action func()) {
	t := c.Theme()
	ui.Row(c).Gap(12).Padding(12, 16).Radius(12).AlignItems(ui.Center).Background(col.Alpha(0.10)).Border(1, col.Alpha(0.28)).Children(func() {
		ui.Box(c).Size(4, 28).Radius(2).Background(col).Children(func() {})
		ui.Column(c).Grow(1).Children(func() {
			text(c, title).Bold().FontSize(13)
			text(c, msg).FontSize(12).TextColor(t.TextMuted).Wrap()
		})
		if action != nil {
			action()
		}
	})
}

// ---- 共用小部件 ----

// pageWidth 是设置类页面正文的最大宽度。
const pageWidth = 760

// page 画页面标题和可滚动的内容区。
func page(c *ui.Context, title, sub string, actions func(), body func()) {
	t := c.Theme()
	ui.Row(c.Key("hdr")).Shrink(0).Padding(20, 32, 12, 32).Justify(ui.Center).Children(func() {
		ui.Row(c).Grow(1).MaxWidth(pageWidth).Gap(12).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
				heading(c, tr(c, title), 24)
				if sub != "" {
					text(c, sub).FontSize(13).TextColor(t.TextMuted).Wrap()
				}
			})
			if actions != nil {
				actions()
			}
		})
	})
	ui.Scroll(c.Key("body")).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Padding(0, 32, 28, 32).Justify(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MaxWidth(pageWidth).Gap(12).Children(body)
		})
	})
}

// card 是设置分组卡片。
func card(c *ui.Context, title, desc string, body func()) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		if title != "" {
			sectionHeader(c, "section", tr(c, title), nil)
		}
		group(c.Key("fields"), func() {
			if desc != "" {
				text(c, desc).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(12, 0, 4, 0)
			}
			body()
		})
	})
}

// settingRow 是“标题 + 说明 | 控件”一行。
func settingRow(c *ui.Context, title, desc string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(16).Padding(12, 0).MinHeight(52).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(lineColor(t)).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(160).Gap(2).Children(func() {
			text(c, title).FontSize(13).FontWeight(500)
			if desc != "" {
				text(c, desc).FontSize(12).TextColor(t.TextMuted).Wrap()
			}
		})
		control()
	})
}

// chip 是小标签。
func chip(c *ui.Context, label string, col ui.Color) ui.Element {
	return text(c, label).FontSize(11).FontWeight(600).Padding(2, 8).Radius(6).Background(col.Alpha(0.14)).TextColor(col).SingleLine()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// filtered 返回按搜索词和筛选过滤、按导入时间倒序的条目。
func (s *Settings) filtered(v app.View) []config.Item {
	q := strings.ToLower(strings.TrimSpace(s.query))
	var out []config.Item
	for _, it := range v.Library {
		if q != "" && !strings.Contains(strings.ToLower(it.Title), q) && !strings.Contains(strings.ToLower(it.Origin), q) && !strings.Contains(strings.ToLower(it.Author), q) && !strings.Contains(strings.ToLower(it.Description), q) && !strings.Contains(strings.ToLower(strings.Join(it.Tags, " ")), q) && !strings.Contains(strings.ToLower(strings.Join(it.Colors, " ")), q) {
			continue
		}
		switch s.filter {
		case 1:
			if it.Type != "video" {
				continue
			}
		case 2:
			if it.Type != "image" {
				continue
			}
		case 3:
			if it.Transcode != "recommended" {
				continue
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out
}

// SelectItem 选中壁纸库里的一项（测试、截图用）。
func (s *Settings) SelectItem(id string) { s.selID = id }

// dispLabel 是显示器在界面上的统一写法：“编号  名称”，编号按物理排列。
// Only generated fallback names are localized. System names and user aliases
// stay verbatim, even when they happen to match a UI catalog entry.
func displayName(c *ui.Context, d app.DisplayView) string {
	if d.Alias == "" && d.Friendly == "" && (d.Name == "" || d.Name == DefaultName(d)) {
		return fmt.Sprintf(tr(c, "显示器 %d"), d.Index)
	}
	return d.Name
}

func dispLabel(c *ui.Context, d app.DisplayView) string {
	return fmt.Sprintf("%d  %s", d.Index, displayName(c, d))
}

// curDisplay 是“当前显示器”：显示器页选中的那块；没选过时用主屏。
func (s *Settings) curDisplay(v app.View) *app.DisplayView {
	var primary *app.DisplayView
	for i := range v.Displays {
		if v.Displays[i].ID == s.dispSel {
			return &v.Displays[i]
		}
		if v.Displays[i].Primary && primary == nil {
			primary = &v.Displays[i]
		}
	}
	if primary == nil && len(v.Displays) > 0 {
		primary = &v.Displays[0]
	}
	return primary
}

// importPath 导入一个文件：默认只加入库；设置为“应用到当前显示器”时只应用到当前那一块。
func (s *Settings) importPath(p string) {
	s.pickerStatus = ""
	v := s.ctl.View()
	to := ""
	if v.Config.UI.ImportApply == "current" {
		if d := s.curDisplay(v); d != nil {
			to = d.ID
		}
	}
	s.ctl.Import(p, to)
}

// StartRename 进入某块屏的重命名编辑状态（截图用）。
func (s *Settings) StartRename(id, buf string) { s.dispSel, s.renameID, s.renameBuf = id, id, buf }

// SelectDisplay 选中一块显示器（测试、截图用）。
func (s *Settings) SelectDisplay(id string) { s.dispSel = id }
