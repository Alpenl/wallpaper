package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
)

// 窗口框架，布局参照 Wallpaper Engine：
//   - 标题区：小标签“目标显示器”+ 大号“显示器 N”、屏幕名、切换显示器；壁纸库里的应用和属性都作用于这块屏；
//   - 标签行：左边分段控件（壁纸库 / 显示器 / 设置），右边导入操作，下方一条从强调色淡出的细线；
//   - 底部操作栏：播放状态、暂停全部、下一张；壁纸库页右侧是应用。
// 不用图标：层次靠底色明度、细描边、字重和强调色。

var settingsPages = []struct{ id, label string }{
	{pgPerf, "播放与性能"}, {pgPause, "暂停与电源"}, {pgLook, "通用"}, {pgStartup, "版本管理"}, {pgAbout, "关于与诊断"},
}

func isSettingsPage(p string) bool { return p != pgLibrary && p != pgDisplays }

func (s *Settings) topBar(c *ui.Context, v app.View) {
	t := c.Theme()
	if isSettingsPage(s.page) {
		s.lastSettings = s.page
	}
	d := s.curDisplay(v)
	ui.Column(c.Key("header")).Shrink(0).Background(barBg(t)).Children(func() {
		// 标题区
		ui.Row(c.Key("top")).Padding(18, 24, 14, 24).Gap(14).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(4).Shrink(1).MinWidth(0).Children(func() {
				overline(c, tr(c, "目标显示器"))
				ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
					title := tr(c, "动态壁纸")
					if d != nil {
						title = fmt.Sprintf(tr(c, "显示器 %d"), d.Index)
					}
					heading(c, title, 28).SingleLine()
					if d != nil {
						ui.Text(c, displayName(c, *d)).FontSize(14).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1).Padding(0, 0, 4, 0)
					}
					if len(v.Displays) > 1 {
						softButton(c.Key("pickdisp"), "选择显示器").Padding(4, 12).FontSize(12).Margin(0, 0, 2, 4).Menu(func(m *ui.Menu) {
							for _, o := range v.Displays {
								label := dispLabel(c, o)
								if o.Primary {
									label += tr(c, "（主屏）")
								}
								if m.Item(label).Checked(d != nil && o.ID == d.ID).Chosen() {
									s.dispSel, s.renameID = o.ID, ""
								}
							}
						})
					}
				})
			})
			ui.Box(c).Grow(1).Children(func() {})
			// 目标屏状态：一个小圆点 + 当前壁纸
			if d != nil {
				state, col := s.screenState(c, v, *d)
				ui.Row(c.Key("tstate")).Gap(8).AlignItems(ui.Center).Padding(6, 12).Radius(999).Background(col.Alpha(0.10)).Border(1, col.Alpha(0.25)).Shrink(1).MinWidth(0).Margin(0, 0, 2, 0).Children(func() {
					ui.Box(c).Size(7, 7).Radius(4).Background(col).Shadow(0, 0, 6, 0, col.Alpha(0.8)).Children(func() {})
					ui.Text(c, state).FontSize(12).TextColor(t.Text).SingleLine().Ellipsis("…").Shrink(1)
				})
			}
		})
		// 标签行
		ui.Row(c.Key("tabs")).Padding(0, 24, 12, 24).Gap(8).AlignItems(ui.Center).Children(func() {
			segmentGroup(c.Key("nav"), func() {
				if segment(c.Key("t-lib"), "壁纸库", s.page == pgLibrary).Clicked() {
					s.page = pgLibrary
				}
				if segment(c.Key("t-disp"), "显示器", s.page == pgDisplays).Clicked() {
					s.page = pgDisplays
				}
				if segment(c.Key("t-set"), "设置", isSettingsPage(s.page)).Clicked() && !isSettingsPage(s.page) {
					s.page = s.lastSettings
				}
			})
			ui.Box(c).Grow(1).Children(func() {})
			if s.page != pgLibrary {
				return
			}
			if ghostButton(c.Key("folder"), "导入文件夹").Disabled(s.PickFolder == nil || v.Importing).Clicked() {
				s.importFolder()
			}
			ghostButton(c.Key("more"), "更多导入").Menu(func(m *ui.Menu) {
				if m.Item(tr(c, "导入 Lively 壁纸包（.zip）…")).Disabled(s.PickFiles == nil).Chosen() {
					s.importFiles()
				}
				if m.Item(tr(c, "导入文件夹…")).Disabled(s.PickFolder == nil || v.Importing).Chosen() {
					s.importFolder()
				}
				m.Separator()
				if m.Item(tr(c, "支持的格式与能力说明")).Chosen() {
					s.page = pgAbout
				}
			})
			if accentButton(c.Key("imp"), "导入壁纸").Disabled(s.PickFiles == nil || v.Importing).Clicked() {
				s.importFiles()
			}
		})
		// 强调色淡出的细线
		ui.Box(c).Height(1).LinearGradient(ui.LinearGradient{From: t.Accent.Alpha(0.75), To: lineColor(t), Angle: 90, End: 0.45}).Children(func() {})
	})
}

// settingsTabs 是设置页的二级标签：同款分段控件，居中。
func (s *Settings) settingsTabs(c *ui.Context) {
	ui.Row(c.Key("stabs")).Shrink(0).Padding(16, 24, 0, 24).Justify(ui.Center).Children(func() {
		segmentGroup(c, func() {
			for _, p := range settingsPages {
				if segment(c.Key("s-"+p.id), p.label, s.page == p.id).Clicked() {
					s.page = p.id
				}
			}
		}).Wrap().Shrink(1)
	})
}

// bottomBar 是底部操作栏：播放状态、暂停全部、下一张；壁纸库页右侧是“应用到所有显示器 / 应用”。
func (s *Settings) bottomBar(c *ui.Context, v app.View) {
	t := c.Theme()
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
	ui.Row(c.Key("status")).Shrink(0).Padding(12, 24).Gap(10).AlignItems(ui.Center).Background(barBg(t)).BorderWidth(1, 0, 0, 0).BorderColor(lineColor(t)).Children(func() {
		col := t.Success
		msg := "正在播放"
		switch {
		case v.PausedAll:
			col, msg = t.Warning, "已全部暂停"
		case playing == 0 && paused > 0:
			col, msg = t.Warning, "自动暂停中"
		case playing == 0:
			col, msg = faint(t), "未设置壁纸"
		}
		ui.Box(c).Size(8, 8).Radius(4).Background(col).Shadow(0, 0, 8, 0, col.Alpha(0.8)).Children(func() {})
		ui.Column(c).Gap(1).Margin(0, 8, 0, 2).Children(func() {
			text(c, msg).FontSize(13).FontWeight(600).SingleLine()
			textf(c, "%d 块屏播放 · %d 块屏暂停", playing, paused).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		lbl := "暂停全部"
		if v.PausedAll {
			lbl = "继续播放"
		}
		if softButton(c.Key("pauseall"), lbl).Clicked() {
			s.ctl.SetPausedAll(!v.PausedAll)
		}
		if ghostButton(c.Key("next"), "下一张").Label(tr(c, "切换到下一张壁纸")).Tooltip(tr(c, "切换到下一张壁纸")).Disabled(len(v.Library) < 2).Clicked() {
			s.ctl.Next()
		}
		ui.Box(c).Grow(1).Children(func() {})
		if s.page != pgLibrary {
			return
		}
		it := s.itemByID(v, s.selID)
		d := s.curDisplay(v)
		if len(v.Displays) > 1 && softButton(c.Key("applyall"), "应用到所有显示器").Disabled(it == nil).Clicked() {
			s.ctl.AssignAll(it.ID)
			c.Toast(tr(c, "已应用到所有显示器"))
		}
		if accentButton(c.Key("apply"), "应用").Padding(8, 34).Disabled(it == nil || d == nil).Clicked() {
			s.applyCurrent(c, v, it.ID)
		}
	})
}

// screenState 是“壁纸名 · 状态”。
func (s *Settings) screenState(c *ui.Context, v app.View, d app.DisplayView) (string, ui.Color) {
	t := c.Theme()
	it := s.itemByID(v, d.ItemID)
	if it == nil {
		return tr(c, "未设置壁纸"), faint(t)
	}
	st, col := tr(c, levelNames[d.Level]), t.Success
	switch {
	case d.State == "failed":
		st, col = tr(c, "已停用"), t.Danger
	case d.State == "recovering":
		st, col = tr(c, "正在恢复"), t.Warning
	case d.Level != "L0":
		col = t.Warning
	}
	return it.Title + " · " + st, col
}
