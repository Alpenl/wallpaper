package ui

import (
	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

// 窗口框架：环境底色、顶部导航、设置子标签、底部浮动 Dock。
// 参照 Wallpaper Engine 以内容为中心：没有常驻侧边栏；当前壁纸经高斯模糊铺满窗口做底色，
// 所有面板是半透明“磨砂”面，界面色调随壁纸变化。

var settingsPages = []struct {
	id    string
	ic    *ui.SVG
	label string
}{
	{pgPerf, icGauge, "播放与性能"}, {pgPause, icPause, "暂停与电源"}, {pgLook, icPalette, "通用"},
	{pgStartup, icRefresh, "版本管理"}, {pgAbout, icInfo, "关于与诊断"},
}

func isSettingsPage(p string) bool { return p != pgLibrary && p != pgDisplays }

// ambientItem 是环境底色取自哪张壁纸：壁纸库里是选中的那张，其他页是目标显示器正在播放的那张。
func (s *Settings) ambientItem(v app.View) *config.Item {
	if s.page == pgLibrary {
		if it := s.itemByID(v, s.selID); it != nil {
			return it
		}
	}
	if d := s.curDisplay(v); d != nil {
		if it := s.itemByID(v, d.ItemID); it != nil {
			return it
		}
	}
	if len(v.Library) > 0 {
		return &v.Library[0]
	}
	return nil
}

// ambient 画在最底层：预先模糊的壁纸小图放大铺满（见 ambient.go；空库时是强调色渐变）+ 一层纱 + 底部轻微压暗。
func (s *Settings) ambient(c *ui.Context, v app.View) {
	t := c.Theme()
	it := s.ambientItem(v)
	ui.Box(c.Key("ambient")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Clip().Children(func() {
		var bm *ui.Bitmap
		if it != nil {
			bm = s.ambientOf(*it)
		}
		switch {
		case bm != nil:
			ui.Image(c, bm).Fit(ui.Cover).Absolute().Left(-40).Top(-40).Right(-40).Bottom(-40)
		case it != nil:
			from, to := placeholderColors(it.ID)
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Gradient(from, to, 135).Children(func() {})
		default:
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Gradient(t.Accent.Alpha(0.40), ui.Hex("#22d3ee").Alpha(0.22), 135).Children(func() {})
		}
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(ambientVeil(t)).Children(func() {})
		ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(220).LinearGradient(ui.LinearGradient{From: ui.Transparent, To: t.Background.Alpha(0.55), Angle: 180}).Children(func() {})
	})
}

// pillTab 是胶囊分段里的一项：选中时是更实的亮面，未选中只有文字。
func pillTab(c *ui.Context, ic *ui.SVG, label string, sel bool) ui.Element {
	t := c.Theme()
	b := button(c, "").Background(ui.Transparent).BorderWidth(0).Padding(6, 14).Radius(999).Children(func() {
		if ic != nil {
			ui.Icon(c, ic).FontSize(14)
		}
		text(c, label).SingleLine().FontSize(13)
	}).Label(tr(c, label))
	if sel {
		bg := ui.Hex("#ffffff")
		if t.Dark {
			bg = ui.RGBA(255, 255, 255, 0.14)
		}
		b.Background(bg).TextColor(t.Text).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.12))
	} else {
		b.TextColor(t.TextMuted)
	}
	return b
}

// capsule 是一组胶囊标签的磨砂容器。
func capsule(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(2).Padding(4).Radius(999).Background(glass(t)).Border(1, hairline(t)).AlignItems(ui.Center).Children(fn)
}

func (s *Settings) topBar(c *ui.Context, v app.View) {
	t := c.Theme()
	if isSettingsPage(s.page) {
		s.lastSettings = s.page
	}
	accent := accentColor(v.Config.UI.Accent)
	w, _ := c.Size()
	narrow := w < 1100
	ui.Row(c.Key("top")).Shrink(0).Height(64).Padding(0, 24).Gap(12).AlignItems(ui.Center).Children(func() {
		// 左：品牌（与右侧等宽，保证导航在窗口正中）
		ui.Row(c.Key("brand")).Grow(1).Basis(0).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(26, 26).Shrink(0).Radius(8).Gradient(accent, ui.Hex("#22d3ee"), 135).Shadow(0, 4, 12, 0, accent.Alpha(0.45)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Box(c).Size(10, 10).Radius(3).Border(2, ui.RGBA(255, 255, 255, 0.9)).Children(func() {})
			})
			text(c, "动态壁纸").Bold().FontSize(15).LetterSpacing(0.3).SingleLine()
		})
		// 中：主导航
		capsule(c.Key("nav"), func() {
			if pillTab(c.Key("t-lib"), icLibrary, "壁纸库", s.page == pgLibrary).Clicked() {
				s.page = pgLibrary
			}
			if pillTab(c.Key("t-disp"), icMonitor, "显示器", s.page == pgDisplays).Clicked() {
				s.page = pgDisplays
			}
			if pillTab(c.Key("t-set"), icSliders, "设置", isSettingsPage(s.page)).Clicked() && !isSettingsPage(s.page) {
				s.page = s.lastSettings
			}
		}).Shrink(0)
		// 右：搜索与导入
		ui.Row(c.Key("actions")).Grow(1).Basis(0).MinWidth(0).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if s.page != pgLibrary {
				return
			}
			if len(v.Library) > 0 {
				ui.Row(c.Key("search")).Gap(4).Padding(0, 0, 0, 12).Radius(999).Background(glass(t)).Border(1, hairline(t)).AlignItems(ui.Center).Shrink(1).MinWidth(110).Width(map[bool]float32{true: 170, false: 230}[narrow]).Children(func() {
					ui.Icon(c, icSearch).FontSize(14).TextColor(t.TextMuted)
					ui.TextInput(c.Key("q"), &s.query).Placeholder(tr(c, "搜索名称、标签、作者")).Label(tr(c, "搜索壁纸")).Background(ui.Transparent).BorderWidth(0).Grow(1).Shrink(1).MinWidth(0)
				})
			}
			button(c.Key("more"), "").Radius(999).Padding(8).Background(glass(t)).Border(1, hairline(t)).Shrink(0).Children(func() { ui.Icon(c, icFolder).FontSize(15) }).Label(tr(c, "更多导入")).Tooltip(tr(c, "更多导入")).Menu(func(m *ui.Menu) {
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
			label := "导入壁纸"
			if narrow {
				label = ""
			}
			if iconButton(c.Key("imp"), icPlus, label, true).Label(tr(c, "导入壁纸")).Tooltip(tr(c, "导入壁纸")).Radius(999).Padding(7, map[bool]float32{true: 9, false: 16}[narrow]).Shrink(0).Shadow(0, 6, 16, 0, t.Accent.Alpha(0.35)).Disabled(s.PickFiles == nil || v.Importing).Clicked() {
				s.importFiles()
			}
		})
	})
}

// settingsTabs 是设置页的二级导航：居中的胶囊分段。
func (s *Settings) settingsTabs(c *ui.Context) {
	ui.Row(c.Key("stabs")).Shrink(0).Padding(2, 24, 0, 24).Justify(ui.Center).Children(func() {
		capsule(c, func() {
			for _, p := range settingsPages {
				if pillTab(c.Key("s-"+p.id), p.ic, p.label, s.page == p.id).Clicked() {
					s.page = p.id
				}
			}
		}).Wrap().Shrink(1)
	})
}

// nowPlaying 是底部浮动 Dock：每块屏一张小卡（当前壁纸、状态），点选即设为“目标显示器”，
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
	ui.Row(c.Key("dockwrap")).Shrink(0).Padding(4, 24, 18, 24).Justify(ui.Center).Children(func() {
		ui.Row(c.Key("status")).Shrink(1).MinWidth(0).Gap(14).Padding(8, 10, 8, 18).Radius(22).Background(glassStrong(t)).Border(1, hairline(t)).Shadow(0, 16, 40, -6, softShadow(t)).AlignItems(ui.Center).Children(func() {
			ui.Column(c.Key("summary")).Shrink(0).Gap(2).Children(func() {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
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
					ui.Box(c).Size(8, 8).Radius(4).Background(col).Shadow(0, 0, 8, 1, col.Alpha(0.6)).Children(func() {})
					text(c, msg).Bold().FontSize(13).SingleLine()
				})
				textf(c, "%d 块屏播放 · %d 块屏暂停", playing, paused).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			ui.Box(c).Size(1, 34).Shrink(0).Background(hairline(t)).Children(func() {})
			ui.Row(c.Key("screens")).Shrink(1).MinWidth(0).Clip().Gap(6).Children(func() {
				for _, d := range v.Displays {
					d := d
					sel := cur != nil && cur.ID == d.ID
					tile := ui.Row(c.Key("np"+d.ID)).Shrink(0).Gap(10).Padding(4, 12, 4, 4).Radius(14).AlignItems(ui.Center).Cursor(ui.CursorPointer).Children(func() {
						ui.Box(c).Size(56, 34).Shrink(0).Radius(9).Clip().Children(func() {
							if it := s.itemByID(v, d.ItemID); it != nil {
								s.preview(c, *it, 34)
							} else {
								ui.Box(c).Height(34).FillWidth().Background(t.TextMuted.Alpha(0.14)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
									ui.Icon(c, icMonitor).FontSize(13).TextColor(t.TextMuted)
								})
							}
						})
						ui.Column(c).Width(128).Gap(1).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, itoa(d.Index)).FontSize(10).Bold().Padding(0, 5).Radius(5).Background(t.TextMuted.Alpha(0.2))
								ui.Text(c, displayName(c, d)).FontSize(12).Bold().SingleLine().Ellipsis("…").Shrink(1)
							})
							state, col := s.screenState(c, v, d)
							ui.Text(c, state).FontSize(11).TextColor(col).SingleLine().Ellipsis("…")
						})
					}).Label(tr(c, "目标显示器") + " " + dispLabel(c, d))
					switch {
					case sel:
						tile.Background(t.Accent.Alpha(0.16)).Border(1, t.Accent.Alpha(0.55))
					case tile.Hovered():
						tile.Background(t.SurfaceHover)
					}
					if tile.Clicked() && !sel {
						s.dispSel, s.renameID = d.ID, ""
					}
				}
			})
			ui.Box(c).Size(1, 34).Shrink(0).Background(hairline(t)).Children(func() {})
			lbl, ic := "暂停全部", icPause
			if v.PausedAll {
				lbl, ic = "继续播放", icPlay
			}
			if iconButton(c.Key("pauseall"), ic, lbl, false).Radius(999).Padding(7, 14).Shrink(0).Clicked() {
				s.ctl.SetPausedAll(!v.PausedAll)
			}
			if iconButton(c.Key("next"), icSkip, "", false).Radius(999).Padding(8).Shrink(0).Label(tr(c, "切换到下一张壁纸")).Tooltip(tr(c, "切换到下一张壁纸")).Disabled(len(v.Library) < 2).Clicked() {
				s.ctl.Next()
			}
		})
	})
}

// screenState 是 Dock 小卡第二行：“壁纸名 · 状态”。
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
