// Package ui 是 MyGo 原生 UI（不用 WebView）：托盘（F-09）+ 设置窗口（壁纸库与拖入导入 F-06/F-08/F-11、
// 每屏壁纸 F-03、暂停与电源策略 F-04/F-05、开机自启 F-10、诊断包 F-16、D-04 Windows 后端、安全模式横幅）。视图函数与 MyGo 窗口/托盘分离，前者可用 ui.NewTester 测试。
package ui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

// Controller 是视图对编排器的依赖（*app.App 满足）。
type Controller interface {
	View() app.View
	SetPausedAll(bool)
	Assign(display, itemID string)
	Import(path string)
	Remove(itemID string)
	UpdateConfig(func(*config.Config))
	ExitSafeMode()
}

const none = "（不设置）"

// Settings 是设置窗口的状态（只在 MyGo 主线程上读写，snap 例外：由 Refresh 加锁替换）。
type Settings struct {
	ctl  Controller
	GOOS string
	// 由 UI 层提供
	PickFiles        func() []string
	SetLaunchAtLogin func(bool) error
	ExportDiag       func() (string, error) // F-16：返回 zip 路径

	diagMsg string

	mu   sync.Mutex
	snap app.View

	tab     int
	sel     map[string]string // display → 选项文本
	confirm string            // 待确认删除的 item id
}

func NewSettings(ctl Controller, goos string) *Settings {
	s := &Settings{ctl: ctl, GOOS: goos, sel: map[string]string{}}
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

func itemLabel(it config.Item) string { return fmt.Sprintf("%s [%s]", it.Title, it.ID) }

func idOf(label string) string {
	if i := strings.LastIndex(label, "["); i >= 0 && strings.HasSuffix(label, "]") {
		return label[i+1 : len(label)-1]
	}
	return ""
}

// View 是 MyGo 视图函数。
func (s *Settings) View(c *ui.Context) {
	v := s.view0()
	t := c.Theme()
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		if v.SafeMode {
			ui.Row(c.Key("safe")).Gap(8).Padding(10).Radius(6).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Text(c, "安全模式：连续多次异常退出，壁纸未自动启动。").Grow(1).Wrap()
				if ui.PrimaryButton(c.Key("exitsafe"), "恢复正常").Clicked() {
					s.ctl.ExitSafeMode()
				}
			})
		}
		for _, f := range v.ReadOnly {
			ui.Text(c.Key("ro"+f), f+" 由更新版本写入，本版本只读，不会覆盖。").TextColor(t.TextMuted).Wrap()
		}
		if v.LastError != "" {
			ui.Text(c.Key("err"), "错误："+v.LastError).Wrap()
		}
		ui.Tabs(c.Key("tabs"), &s.tab, "壁纸库", "显示器", "设置")
		switch s.tab {
		case 0:
			s.library(c, v)
		case 1:
			s.displays(c, v)
		default:
			s.config(c, v)
		}
	})
}

func (s *Settings) library(c *ui.Context, v app.View) {
	t := c.Theme()
	drop := ui.Box(c.Key("drop")).Padding(18).Radius(8).Border(1, t.Border).Children(func() {
		msg := "把 mp4 / mov / gif / 图片 / .wpk / Lively 压缩包拖到这里导入"
		if v.Importing {
			msg = "正在导入…"
		}
		ui.Text(c, msg).TextColor(t.TextMuted).Wrap()
	})
	if drop.FileDragOver() {
		drop.Background(t.Surface)
	}
	for _, p := range drop.DroppedFiles() {
		s.ctl.Import(p)
	}
	ui.Row(c.Key("improw")).Gap(8).Children(func() {
		if ui.Button(c.Key("pick"), "选择文件导入…").Disabled(s.PickFiles == nil || v.Importing).Clicked() {
			for _, p := range s.PickFiles() {
				s.ctl.Import(p)
			}
		}
		if v.LastImport != "" {
			ui.Text(c, v.LastImport).TextColor(t.TextMuted).Wrap().Grow(1)
		}
	})
	if len(v.Library) == 0 {
		ui.Text(c.Key("empty"), "壁纸库为空。").TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c.Key("lib")).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for _, it := range v.Library {
				it := it
				ui.Row(c.Key("it"+it.ID)).Gap(8).Padding(8).Radius(6).Border(1, t.Border).Children(func() {
					info := fmt.Sprintf("%s · %s", it.Type, it.Origin)
					if it.Codec != "" {
						info += " · " + it.Codec
					}
					if it.Transcode == "recommended" {
						info += " · 建议转码"
					}
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, it.Title).Bold()
						ui.Text(c, info).FontSize(12).TextColor(t.TextMuted)
						if it.Note != "" {
							ui.Text(c, it.Note).FontSize(12).TextColor(t.TextMuted).Wrap()
						}
					})
					if s.confirm == it.ID {
						if ui.Button(c.Key("rmyes"+it.ID), "确认删除").Clicked() {
							s.confirm = ""
							s.ctl.Remove(it.ID)
						}
						if ui.Button(c.Key("rmno"+it.ID), "取消").Clicked() {
							s.confirm = ""
						}
					} else if ui.Button(c.Key("rm"+it.ID), "删除").Label("删除 " + it.Title).Clicked() {
						s.confirm = it.ID
					}
				})
			}
		})
	})
}

func (s *Settings) displays(c *ui.Context, v app.View) {
	t := c.Theme()
	opts := []string{none}
	byID := map[string]string{}
	lib := append([]config.Item(nil), v.Library...)
	sort.Slice(lib, func(i, j int) bool { return lib[i].AddedAt.Before(lib[j].AddedAt) })
	for _, it := range lib {
		opts = append(opts, itemLabel(it))
		byID[it.ID] = itemLabel(it)
	}
	if len(v.Displays) == 0 {
		ui.Text(c.Key("nodisp"), "没有检测到显示器。").TextColor(t.TextMuted)
	}
	for _, d := range v.Displays {
		d := d
		cur := none
		if l, ok := byID[d.ItemID]; ok {
			cur = l
		}
		if _, ok := s.sel[d.ID]; !ok || s.sel[d.ID] != cur && idOf(s.sel[d.ID]) == d.ItemID {
			s.sel[d.ID] = cur
		}
		ui.Column(c.Key("d"+d.ID)).Gap(4).Padding(10).Radius(6).Border(1, t.Border).Children(func() {
			name := d.Name
			if d.Primary {
				name += "（主屏）"
			}
			ui.Textf(c, "%s  %d×%d", name, d.W, d.H).Bold()
			sel := s.sel[d.ID]
			if ui.Select(c.Key("sel"+d.ID), &sel, opts).Label("壁纸 " + d.Name).Changed() {
				s.sel[d.ID] = sel
				s.ctl.Assign(d.ID, idOf(sel))
			}
			st := fmt.Sprintf("状态 %s · 级别 %s", d.State, d.Level)
			if d.Backend != "" {
				st += fmt.Sprintf(" · %s", d.Backend)
			}
			if d.HWDec != "" {
				st += " · hwdec " + d.HWDec
			}
			ui.Text(c, st).FontSize(12).TextColor(t.TextMuted)
		})
	}
}

func (s *Settings) config(c *ui.Context, v app.View) {
	cfg := v.Config
	row := func(key, label string, on bool, set func(*config.Config, bool)) {
		ui.Row(c.Key(key)).Gap(8).Center().Children(func() {
			ui.Text(c, label).Grow(1)
			b := on
			if ui.Switch(c.Key(key+"sw"), &b).Label(label + " 开关").Changed() {
				s.ctl.UpdateConfig(func(x *config.Config) { set(x, b) })
			}
		})
	}
	paused := v.PausedAll
	ui.Row(c.Key("pauseall")).Gap(8).Center().Children(func() {
		ui.Text(c, "暂停全部壁纸").Grow(1)
		if ui.Switch(c.Key("pauseallsw"), &paused).Label("暂停全部壁纸 开关").Changed() {
			s.ctl.SetPausedAll(paused)
		}
	})
	row("fs", "其他程序全屏时暂停", cfg.Pause.OnFullscreen, func(x *config.Config, b bool) { x.Pause.OnFullscreen = b })
	row("cov", "桌面被窗口完全遮挡时暂停", cfg.Pause.OnCovered, func(x *config.Config, b bool) { x.Pause.OnCovered = b })
	ui.Row(c.Key("batt")).Gap(8).Center().Children(func() {
		ui.Text(c, "使用电池时").Grow(1)
		labels := map[string]string{"pause": "暂停", "ignore": "继续播放"}
		cur := labels[cfg.Pause.BatteryPolicy]
		if cur == "" {
			cur = labels["pause"]
		}
		if ui.Select(c.Key("battsel"), &cur, []string{"暂停", "继续播放"}).Label("使用电池时").Changed() {
			p := "pause"
			if cur == "继续播放" {
				p = "ignore"
			}
			s.ctl.UpdateConfig(func(x *config.Config) { x.Pause.BatteryPolicy = p })
		}
	})
	ui.Row(c.Key("low")).Gap(8).Center().Children(func() {
		ui.Text(c, "低电量阈值（%，低于此值总是暂停）").Grow(1)
		f := float64(cfg.Pause.LowBatteryPct)
		if ui.NumberInput(c.Key("lowin"), &f, 0, 100, 5).Label("低电量阈值").Changed() {
			n := int(f)
			s.ctl.UpdateConfig(func(x *config.Config) { x.Pause.LowBatteryPct = n })
		}
	})
	ui.Row(c.Key("login")).Gap(8).Center().Children(func() {
		ui.Text(c, "开机自动启动").Grow(1)
		b := cfg.Startup.LaunchAtLogin
		if ui.Switch(c.Key("loginsw"), &b).Label("开机自动启动 开关").Disabled(s.SetLaunchAtLogin == nil).Changed() {
			s.ctl.UpdateConfig(func(x *config.Config) { x.Startup.LaunchAtLogin = b; x.Startup.Asked = true })
		}
	})
	if s.GOOS == "windows" {
		ui.Row(c.Key("backend")).Gap(8).Center().Children(func() {
			ui.Text(c, "Windows 渲染后端（切换后重启壁纸）").Grow(1)
			cur := cfg.Render.WindowsBackend
			if ui.Select(c.Key("backendsel"), &cur, []string{"mpv", "mf"}).Label("渲染后端").Changed() {
				b := cur
				s.ctl.UpdateConfig(func(x *config.Config) { x.Render.WindowsBackend = b })
			}
		})
	}
	ui.Row(c.Key("diag")).Gap(8).Center().Children(func() {
		if ui.Button(c.Key("diagbtn"), "导出诊断包").Disabled(s.ExportDiag == nil).Clicked() {
			if p, err := s.ExportDiag(); err != nil {
				s.diagMsg = "导出失败：" + err.Error()
			} else {
				s.diagMsg = "已导出：" + p
			}
		}
		if s.diagMsg != "" {
			ui.Text(c, s.diagMsg).FontSize(12).Wrap().Grow(1)
		}
	})
	ui.Text(c.Key("priv"), "不收集任何遥测数据（D-15）。日志只保存在本机 logs 目录。").FontSize(12).TextColor(c.Theme().TextMuted)
}
