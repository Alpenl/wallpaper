package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
)

// 窗口框架，布局参照 Wallpaper Engine：
//   - 左上是大号“显示器 N”标题和“选择显示器”：壁纸库里的应用、属性都作用于这块目标屏；
//   - 下面一行是标签（壁纸库 / 显示器 / 设置），整行底部一条强调色线；
//   - 底部操作栏：左边是播放状态与暂停 / 下一张，右边是应用（壁纸库页）。
// 全程不用图标，层次只靠底色深浅、细线和强调色。

var settingsPages = []struct{ id, label string }{
	{pgPerf, "播放与性能"}, {pgPause, "暂停与电源"}, {pgLook, "通用"}, {pgStartup, "版本管理"}, {pgAbout, "关于与诊断"},
}

func isSettingsPage(p string) bool { return p != pgLibrary && p != pgDisplays }

// tabButton 是标签条上的一项：选中时实心强调色，其余是普通平面按钮。
func tabButton(c *ui.Context, label string, sel bool) ui.Element {
	t := c.Theme()
	b := button(c, label).Radius(2).Padding(6, 16)
	if sel {
		b.Background(t.Accent).TextColor(t.AccentText).Border(1, t.Accent)
	}
	return b
}

func (s *Settings) topBar(c *ui.Context, v app.View) {
	t := c.Theme()
	if isSettingsPage(s.page) {
		s.lastSettings = s.page
	}
	d := s.curDisplay(v)
	// 标题行：显示器 N（目标屏）+ 名称 + 选择显示器
	ui.Row(c.Key("top")).Shrink(0).Padding(16, 20, 10, 20).Gap(12).AlignItems(ui.Center).Children(func() {
		title := tr(c, "动态壁纸")
		if d != nil {
			title = fmt.Sprintf(tr(c, "显示器 %d"), d.Index)
		}
		heading(c, title, 30).SingleLine()
		if d != nil {
			ui.Text(c, displayName(c, *d)).FontSize(14).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1)
		}
		if len(v.Displays) > 1 {
			button(c.Key("pickdisp"), "选择显示器").Radius(2).Padding(4, 10).FontSize(13).Menu(func(m *ui.Menu) {
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
		ui.Box(c).Grow(1).Children(func() {})
		if s.page == pgLibrary {
			if button(c.Key("folder"), "导入文件夹").Radius(2).Disabled(s.PickFolder == nil || v.Importing).Clicked() {
				s.importFolder()
			}
			button(c.Key("more"), "更多导入").Radius(2).Menu(func(m *ui.Menu) {
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
		}
	})
	// 标签条：底部一条贯穿的强调色线
	ui.Row(c.Key("tabs")).Shrink(0).Margin(0, 20, 0, 20).Padding(0, 0, 4, 0).Gap(4).AlignItems(ui.End).BorderWidth(0, 0, 2, 0).BorderColor(t.Accent).Children(func() {
		if tabButton(c.Key("t-lib"), "壁纸库", s.page == pgLibrary).Clicked() {
			s.page = pgLibrary
		}
		if tabButton(c.Key("t-disp"), "显示器", s.page == pgDisplays).Clicked() {
			s.page = pgDisplays
		}
		if tabButton(c.Key("t-set"), "设置", isSettingsPage(s.page)).Clicked() && !isSettingsPage(s.page) {
			s.page = s.lastSettings
		}
		ui.Box(c).Grow(1).Children(func() {})
		if s.page == pgLibrary {
			if primaryButton(c.Key("imp"), "导入壁纸").Radius(2).Padding(6, 16).Disabled(s.PickFiles == nil || v.Importing).Clicked() {
				s.importFiles()
			}
		}
	})
}

// settingsTabs 是设置页的二级标签：文字标签，选中项下面一条强调色线。
func (s *Settings) settingsTabs(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("stabs")).Shrink(0).Padding(6, 20, 0, 20).Gap(4).Wrap().BorderWidth(0, 0, 1, 0).BorderColor(lineColor(t)).Children(func() {
		for _, p := range settingsPages {
			sel := s.page == p.id
			b := button(c.Key("s-"+p.id), p.label).Background(ui.Transparent).BorderWidth(0).Radius(0).Padding(8, 14, 8, 14)
			if sel {
				b.TextColor(t.Text).BorderWidth(0, 0, 2, 0).BorderColor(t.Accent)
			} else {
				b.TextColor(t.TextMuted)
			}
			if b.Clicked() {
				s.page = p.id
			}
		}
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
	ui.Row(c.Key("status")).Shrink(0).Padding(10, 20).Gap(10).AlignItems(ui.Center).Background(barBg(t)).BorderWidth(1, 0, 0, 0).BorderColor(lineColor(t)).Children(func() {
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
		ui.Box(c).Size(8, 8).Radius(1).Background(col).Children(func() {})
		text(c, msg).FontSize(13).FontWeight(500).SingleLine()
		textf(c, "%d 块屏播放 · %d 块屏暂停", playing, paused).FontSize(12).TextColor(t.TextMuted).SingleLine()
		lbl := "暂停全部"
		if v.PausedAll {
			lbl = "继续播放"
		}
		if button(c.Key("pauseall"), lbl).Radius(2).Margin(0, 0, 0, 8).Clicked() {
			s.ctl.SetPausedAll(!v.PausedAll)
		}
		if button(c.Key("next"), "下一张").Radius(2).Label(tr(c, "切换到下一张壁纸")).Tooltip(tr(c, "切换到下一张壁纸")).Disabled(len(v.Library) < 2).Clicked() {
			s.ctl.Next()
		}
		ui.Box(c).Grow(1).Children(func() {})
		if s.page != pgLibrary {
			return
		}
		it := s.itemByID(v, s.selID)
		d := s.curDisplay(v)
		if len(v.Displays) > 1 && button(c.Key("applyall"), "应用到所有显示器").Radius(2).Padding(7, 18).Disabled(it == nil).Clicked() {
			s.ctl.AssignAll(it.ID)
			c.Toast(tr(c, "已应用到所有显示器"))
		}
		if primaryButton(c.Key("apply"), "应用").Radius(2).Padding(7, 30).Disabled(it == nil || d == nil).Clicked() {
			s.applyCurrent(c, v, it.ID)
		}
	})
}
