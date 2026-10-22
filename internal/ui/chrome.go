package ui

import (
	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
)

// 窗口框架：顶部导航条、设置子标签、底部“正在播放”屏幕条。
// 参照 Wallpaper Engine：不要常驻侧边栏，把宽度留给壁纸内容；屏幕条兼作“目标显示器”选择。

var settingsPages = []struct {
	id    string
	ic    *ui.SVG
	label string
}{
	{pgPerf, icGauge, "播放与性能"}, {pgPause, icPause, "暂停与电源"}, {pgLook, icPalette, "通用"},
	{pgStartup, icRefresh, "版本管理"}, {pgAbout, icInfo, "关于与诊断"},
}

func isSettingsPage(p string) bool { return p != pgLibrary && p != pgDisplays }

// navTab 是顶部的一个标签：选中时用强调色浅底。
func navTab(c *ui.Context, ic *ui.SVG, label string, sel bool) ui.Element {
	t := c.Theme()
	b := button(c, "").Background(ui.Transparent).BorderWidth(0).Padding(6, 12).Radius(8).Children(func() {
		ui.Icon(c, ic).FontSize(15)
		text(c, label).SingleLine()
	}).Label(tr(c, label))
	if sel {
		b.Background(t.Accent.Alpha(0.13)).TextColor(t.Accent)
	} else {
		b.TextColor(t.TextMuted)
	}
	return b
}

func (s *Settings) topBar(c *ui.Context, v app.View) {
	t := c.Theme()
	if isSettingsPage(s.page) {
		s.lastSettings = s.page
	}
	ui.Row(c.Key("top")).Shrink(0).Height(54).Padding(0, 16).Gap(4).AlignItems(ui.Center).Background(sideBg(t)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c.Key("brand")).Gap(8).Margin(0, 14, 0, 0).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(22, 22).Radius(6).Gradient(accentColor(v.Config.UI.Accent), ui.Hex("#22d3ee"), 135).Children(func() {})
			text(c, "动态壁纸").Bold().FontSize(14).SingleLine()
		})
		if navTab(c.Key("t-lib"), icLibrary, "壁纸库", s.page == pgLibrary).Clicked() {
			s.page = pgLibrary
		}
		if navTab(c.Key("t-disp"), icMonitor, "显示器", s.page == pgDisplays).Clicked() {
			s.page = pgDisplays
		}
		if navTab(c.Key("t-set"), icSliders, "设置", isSettingsPage(s.page)).Clicked() && !isSettingsPage(s.page) {
			s.page = s.lastSettings
		}
		ui.Box(c).Grow(1).Children(func() {})
		if s.page != pgLibrary {
			return
		}
		if len(v.Library) > 0 {
			ui.TextInput(c.Key("q"), &s.query).Placeholder(tr(c, "搜索名称、标签、作者")).Label(tr(c, "搜索壁纸")).Width(220).Shrink(1).MinWidth(120)
		}
		button(c.Key("more"), "").Children(func() { ui.Icon(c, icFolder).FontSize(14) }).Label(tr(c, "更多导入")).Tooltip(tr(c, "更多导入")).Menu(func(m *ui.Menu) {
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
		if iconButton(c.Key("imp"), icPlus, "导入壁纸", true).Disabled(s.PickFiles == nil || v.Importing).Clicked() {
			s.importFiles()
		}
	})
}

// settingsTabs 是设置页的二级标签（替代原侧边栏的“设置/系统”两组）。
func (s *Settings) settingsTabs(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("stabs")).Shrink(0).Padding(10, 32, 0, 32).Justify(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Grow(1).MaxWidth(pageWidth).Gap(2).Wrap().Children(func() {
			for _, p := range settingsPages {
				p := p
				sel := s.page == p.id
				b := button(c.Key("s-"+p.id), "").Background(ui.Transparent).BorderWidth(0).Radius(0).Padding(8, 12, 10, 12).Children(func() {
					ui.Icon(c, p.ic).FontSize(14)
					text(c, p.label).SingleLine()
				}).Label(tr(c, p.label))
				if sel {
					b.TextColor(t.Accent).BorderWidth(0, 0, 2, 0).BorderColor(t.Accent)
				} else {
					b.TextColor(t.TextMuted)
				}
				if b.Clicked() {
					s.page = p.id
				}
			}
		})
	})
}

// nowPlaying 是底部屏幕条：每块屏一张小卡（当前壁纸、状态），点选即设为“目标显示器”，
// 壁纸库里双击 / 应用 都作用于它；右侧是全局暂停与切换。
func (s *Settings) nowPlaying(c *ui.Context, v app.View) {
	t := c.Theme()
	cur := s.curDisplay(v)
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
	ui.Row(c.Key("status")).Shrink(0).Height(72).Padding(0, 16).Gap(12).AlignItems(ui.Center).Background(sideBg(t)).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c.Key("summary")).Width(148).Shrink(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
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
				text(c, msg).Bold().FontSize(13).SingleLine()
			})
			textf(c, "%d 块屏播放 · %d 块屏暂停", playing, paused).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		ui.Row(c.Key("screens")).Grow(1).Shrink(1).MinWidth(0).Clip().Children(func() {
			ui.Row(c).Gap(8).Padding(4, 0).Children(func() {
				for _, d := range v.Displays {
					d := d
					sel := cur != nil && cur.ID == d.ID
					tile := ui.Row(c.Key("np"+d.ID)).Gap(8).Padding(4, 10, 4, 4).Radius(10).AlignItems(ui.Center).Background(cardBg(t)).Border(1, t.Border).Cursor(ui.CursorPointer).Children(func() {
						ui.Box(c).Size(64, 36).Radius(6).Clip().Children(func() {
							if it := s.itemByID(v, d.ItemID); it != nil {
								s.preview(c, *it, 36)
							} else {
								ui.Box(c).Height(36).FillWidth().Background(t.TextMuted.Alpha(0.14)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
									ui.Icon(c, icMonitor).FontSize(14).TextColor(t.TextMuted)
								})
							}
						})
						ui.Column(c).Width(132).Gap(1).Children(func() {
							ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
								ui.Text(c, itoa(d.Index)).FontSize(10).Bold().Padding(0, 5).Radius(4).Background(t.TextMuted.Alpha(0.18))
								ui.Text(c, displayName(c, d)).FontSize(12).Bold().SingleLine().Ellipsis("…").Shrink(1)
							})
							state, col := s.screenState(c, v, d)
							ui.Text(c, state).FontSize(11).TextColor(col).SingleLine().Ellipsis("…")
						})
					}).Label(tr(c, "目标显示器") + " " + dispLabel(c, d))
					if sel {
						tile.Border(2, t.Accent)
					} else if tile.Hovered() {
						tile.Border(1, t.Accent.Alpha(0.5))
					}
					if tile.Clicked() && !sel {
						s.dispSel, s.renameID = d.ID, ""
					}
				}
			})
		})
		lbl, ic := "暂停全部", icPause
		if v.PausedAll {
			lbl, ic = "继续播放", icPlay
		}
		if iconButton(c.Key("pauseall"), ic, lbl, false).Clicked() {
			s.ctl.SetPausedAll(!v.PausedAll)
		}
		if iconButton(c.Key("next"), icSkip, "", false).Label(tr(c, "切换到下一张壁纸")).Tooltip(tr(c, "切换到下一张壁纸")).Disabled(len(v.Library) < 2).Clicked() {
			s.ctl.Next()
		}
	})
}

// screenState 是屏幕条第二行：“壁纸名 · 状态”。
func (s *Settings) screenState(c *ui.Context, v app.View, d app.DisplayView) (string, ui.Color) {
	t := c.Theme()
	it := s.itemByID(v, d.ItemID)
	if it == nil {
		return tr(c, "未设置壁纸"), t.TextMuted
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
