// Package ui 是 MyGo 原生 UI（不用 WebView）：托盘（F-09）+ 设置窗口。
//
// 设置窗口对标 Wallpaper Engine / Lively 的信息架构（01 章竞品分析）：左侧导航，右侧页面。
//   - 壁纸库：缩略图网格 / 列表、搜索、类型筛选、详情面板（预览、元数据、应用到显示器、一键转码 F-07、删除）、拖入导入（F-06/F-08/F-11）
//   - 显示器：按真实坐标画的布局示意图、每屏分配（F-03）、运行状态徽章（级别 L0–L4、暂停原因、后端、硬解、限帧，F-17 雏形）
//   - 播放与性能：性能档位（流畅/均衡/省电 15 fps）、Windows 渲染后端（D-04）、硬件解码、转码编码器（F-07）
//   - 暂停规则（F-04）、电源与电池（F-05：电池策略含 15 fps 档、低电量阈值、macOS 低电量模式 / 过热）
//   - 外观（浅色 / 深色 / 跟随系统、强调色）、启动与更新（F-10、F-15 增量更新与回滚）、诊断（F-16）、关于
//
// v1 才做的功能（网页 / Shader 壁纸、创意工坊、属性面板、按应用暂停……）有入口，点击时提示“正在开发中”。
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
	Import(path, applyTo string) // applyTo：空 = 只加入库；显示器 ID = 同时应用到这块屏
	Remove(itemID string)
	UpdateConfig(func(*config.Config))
	ExitSafeMode()
	Next()
	Transcode(itemID string)
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
	pgPerf     = "perf"
	pgPause    = "pause"
	pgPower    = "power"
	pgLook     = "look"
	pgStartup  = "startup"
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

	page      string
	navOpen   [3]bool
	query     string
	filter    int
	gridSel   int
	grid      ui.GridState
	list      ui.ListState
	selID     string
	confirmRm bool
	dispSel   string
	applySel  string // 详情面板“应用到”下拉框的当前项
	renameID  string // 正在重命名的显示器 ID（空 = 不在编辑）
	renameBuf string
	diagMsg   string
	thumbs    map[string]thumb
	themeKey  string
	theme     *ui.Theme
	resetAsk  bool
	ffmpeg    string
	feed      string
	feedInit  bool
	ffInit    bool
	askLogin  bool
	askInit   bool
}

type thumb struct {
	bm  *ui.Bitmap
	mod time.Time
}

type updState struct {
	busy     bool
	msg      string
	info     *UpdateInfo
	progress float64
}

func NewSettings(ctl Controller, goos string) *Settings {
	s := &Settings{ctl: ctl, GOOS: goos, page: pgLibrary, navOpen: [3]bool{true, true, true}, gridSel: -1, thumbs: map[string]thumb{}}
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
func (s *Settings) SetPage(p string) { s.page = p }

// wip 是“正在开发中”的统一提示。
func (s *Settings) wip(c *ui.Context, feature string) {
	c.AddToast(ui.Toast{ID: "wip-" + feature, Title: feature + " 正在开发中", Description: "这个功能计划在 v1 版本提供，当前 MVP 只保留入口。"})
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
	s.applyTheme(c, v.Config.UI)
	t := c.Theme()
	ui.Row(c.Key("root")).Fill().Clip().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		s.sidebar(c, v)
		ui.Column(c.Key("main")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
			s.banners(c, v)
			switch s.page {
			case pgLibrary:
				s.library(c, v)
			case pgDisplays:
				s.displays(c, v)
			case pgDiscover:
				s.discover(c, v)
			case pgPerf:
				s.perf(c, v)
			case pgPause:
				s.pause(c, v)
			case pgPower:
				s.power(c, v)
			case pgLook:
				s.look(c, v)
			case pgStartup:
				s.startup(c, v)
			case pgDiag:
				s.diag(c, v)
			default:
				s.about(c, v)
			}
		})
	})
	s.firstRun(c, v)
}

// firstRun：F-10 默认不开机自启，首次运行时询问一次。
func (s *Settings) firstRun(c *ui.Context, v app.View) {
	if !s.askInit {
		s.askInit = true
		s.askLogin = !v.Config.Startup.Asked && s.SetLaunchAtLogin != nil
	}
	switch ui.AlertDialog(c.Key("asklogin"), &s.askLogin, "开机时自动启动动态壁纸？", "开启后登录系统时自动在后台运行并显示壁纸，可以随时在“启动与更新”里关闭。", "暂不", "开机启动") {
	case 0, ui.AlertDismissed:
		s.ctl.UpdateConfig(func(x *config.Config) { x.Startup.Asked = true })
	case 1:
		s.ctl.UpdateConfig(func(x *config.Config) { x.Startup.Asked = true; x.Startup.LaunchAtLogin = true })
	}
}

func (s *Settings) sidebar(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Column(c.Key("side")).Width(228).MinHeight(0).Background(sideBg(t)).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c.Key("brand")).Gap(10).Padding(18, 16, 10, 16).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(30, 30).Radius(8).Gradient(accentColor(v.Config.UI.Accent), ui.Hex("#22d3ee"), 135).Children(func() {})
			ui.Column(c).Children(func() {
				ui.Text(c, "动态壁纸").Bold().FontSize(15)
				ui.Text(c, "Wallpaper "+s.Info.Version).FontSize(11).TextColor(t.TextMuted)
			})
		})
		ui.Sidebar(c.Key("nav"), &s.page, func() {
			ui.SidebarSection(c.Key("sec0"), "壁纸", &s.navOpen[0], func() {
				ui.SidebarItem(c.Key("n-lib"), pgLibrary, icLibrary, "壁纸库").Children(func() {
					if n := len(v.Library); n > 0 {
						ui.Badge(c, itoa(n))
					}
				})
				ui.SidebarItem(c.Key("n-disp"), pgDisplays, icMonitor, "显示器").Children(func() {
					if n := len(v.Displays); n > 1 {
						ui.Badge(c, itoa(n))
					}
				})
				ui.SidebarItem(c.Key("n-disc"), pgDiscover, icDiscover, "发现与创意工坊")
			})
			ui.SidebarSection(c.Key("sec1"), "设置", &s.navOpen[1], func() {
				ui.SidebarItem(c.Key("n-perf"), pgPerf, icGauge, "播放与性能")
				ui.SidebarItem(c.Key("n-pause"), pgPause, icPause, "暂停规则")
				ui.SidebarItem(c.Key("n-power"), pgPower, icBattery, "电源与电池")
				ui.SidebarItem(c.Key("n-look"), pgLook, icPalette, "外观")
				ui.SidebarItem(c.Key("n-start"), pgStartup, icRocket, "启动与更新")
			})
			ui.SidebarSection(c.Key("sec2"), "支持", &s.navOpen[2], func() {
				ui.SidebarItem(c.Key("n-diag"), pgDiag, icPulse, "诊断")
				ui.SidebarItem(c.Key("n-about"), pgAbout, icInfo, "关于")
			})
		}).Grow(1).MinHeight(0)
		// 底部状态卡：一眼看到在不在播放，一键暂停（Lively / WE 托盘都有的“暂停全部”）
		ui.Column(c.Key("status")).Margin(12).Padding(12).Gap(8).Radius(10).Background(cardBg(t)).Border(1, t.Border).Children(func() {
			playing, paused := 0, 0
			for _, d := range v.Displays {
				switch {
				case d.ItemID == "":
				case d.Level == "L0" && d.State == "active":
					playing++
				default:
					paused++
				}
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				col := t.Success
				msg := "正在播放"
				switch {
				case v.PausedAll:
					col, msg = t.Warning, "已全部暂停"
				case playing == 0 && paused > 0:
					col, msg = t.Warning, "自动暂停中"
				case playing == 0:
					col, msg = t.TextMuted, "未设置壁纸"
				}
				ui.Box(c).Size(8, 8).Radius(4).Background(col).Children(func() {})
				ui.Text(c, msg).Bold().FontSize(13)
			})
			ui.Textf(c, "%d 块屏播放 · %d 块屏暂停", playing, paused).FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(6).Children(func() {
				lbl, ic := "暂停全部", icPause
				if v.PausedAll {
					lbl, ic = "继续播放", icPlay
				}
				if iconButton(c.Key("pauseall"), ic, lbl, false).Grow(1).Clicked() {
					s.ctl.SetPausedAll(!v.PausedAll)
				}
				if iconButton(c.Key("next"), icSkip, "", false).Label("切换到下一张壁纸").Tooltip("切换到下一张壁纸").Disabled(len(v.Library) < 2).Clicked() {
					s.ctl.Next()
				}
			})
		})
	})
}

func (s *Settings) banners(c *ui.Context, v app.View) {
	t := c.Theme()
	if !v.SafeMode && v.LastError == "" && len(v.ReadOnly) == 0 {
		return
	}
	ui.Column(c.Key("banners")).Padding(16, 24, 0, 24).Gap(8).Children(func() {
		if v.SafeMode {
			banner(c.Key("safe"), t.Warning, "安全模式", "连续多次异常退出，壁纸没有自动启动。确认问题已解决后再恢复。", func() {
				if ui.PrimaryButton(c.Key("exitsafe"), "恢复正常").Clicked() {
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
	ui.Row(c).Gap(12).Padding(12, 14).Radius(10).AlignItems(ui.Center).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.35)).Children(func() {
		ui.Box(c).Size(4, 28).Radius(2).Background(col).Children(func() {})
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, title).Bold().FontSize(13)
			ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Wrap()
		})
		if action != nil {
			action()
		}
	})
}

// ---- 共用小部件 ----

// page 画页面标题和可滚动的内容区。
func page(c *ui.Context, title, sub string, actions func(), body func()) {
	t := c.Theme()
	ui.Row(c.Key("hdr")).Padding(22, 28, 14, 28).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, title).Bold().FontSize(22)
			if sub != "" {
				ui.Text(c, sub).FontSize(13).TextColor(t.TextMuted)
			}
		})
		if actions != nil {
			actions()
		}
	})
	ui.Scroll(c.Key("body")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, 28, 28, 28).Gap(16).MaxWidth(880).Children(body)
	})
}

// card 是设置分组卡片。
func card(c *ui.Context, title, desc string, body func()) {
	t := c.Theme()
	ui.Column(c).Radius(12).Background(cardBg(t)).Border(1, t.Border).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.04)).Children(func() {
		if title != "" {
			ui.Column(c).Padding(14, 18, 4, 18).Children(func() {
				ui.Text(c, title).Bold().FontSize(14)
				if desc != "" {
					ui.Text(c, desc).FontSize(12).TextColor(t.TextMuted).Wrap()
				}
			})
		}
		ui.Column(c).Padding(4, 18, 10, 18).Children(body)
	})
}

// settingRow 是“标题 + 说明 | 控件”一行。
func settingRow(c *ui.Context, title, desc string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(16).Padding(10, 0).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.5)).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			ui.Text(c, title).FontSize(13)
			if desc != "" {
				ui.Text(c, desc).FontSize(12).TextColor(t.TextMuted).Wrap()
			}
		})
		control()
	})
}

// devTag 是“开发中”标签。
func devTag(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "开发中").FontSize(11).Padding(1, 7).Radius(999).Background(t.Warning.Alpha(0.16)).TextColor(t.Warning).SingleLine()
}

// chip 是小标签。
func chip(c *ui.Context, text string, col ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(11).Padding(1, 7).Radius(999).Background(col.Alpha(0.14)).TextColor(col).SingleLine()
}

func iconButton(c *ui.Context, ic *ui.SVG, label string, primary bool) ui.Element {
	mk := ui.Button
	if primary {
		mk = ui.PrimaryButton
	}
	return mk(c, "").Children(func() {
		ui.Icon(c, ic).FontSize(14)
		if label != "" {
			ui.Text(c, label).SingleLine()
		}
	}).Label(label)
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
		if q != "" && !strings.Contains(strings.ToLower(it.Title), q) && !strings.Contains(strings.ToLower(it.Origin), q) {
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
func dispLabel(d app.DisplayView) string { return fmt.Sprintf("%d  %s", d.Index, d.Name) }

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
