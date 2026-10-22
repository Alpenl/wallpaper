package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

var typeNames = map[string]string{"video": "视频", "image": "图片", "web": "网页", "shader": "Shader"}
var originNames = map[string]string{"native": "本应用壁纸包", "lively": "Lively 导入", "we": "Wallpaper Engine 导入", "file": "本地文件"}

var (
	fitNames  = map[string]string{"fill": "填充", "fit": "适应", "stretch": "拉伸"}
	fitIDs    = map[string]string{"填充": "fill", "适应": "fit", "拉伸": "stretch"}
	fitLabels = []string{"填充", "适应", "拉伸"}
)

func (s *Settings) importFiles() {
	if s.PickFiles == nil {
		return
	}
	for _, p := range s.PickFiles() {
		s.importPath(p)
	}
}

// library 是主页：上方“舞台”展示选中的壁纸组（大预览 + 标题 + 应用 + 组属性），下方是收藏网格。
// 选中项默认是目标显示器正在播放的那张，所以舞台和环境底色总有内容。
func (s *Settings) library(c *ui.Context, v app.View) {
	t := c.Theme()
	// Only clear selection after a refreshed snapshot confirms removal, never
	// merely because an asynchronous delete request was submitted or rejected.
	if s.selID != "" && s.itemByID(v, s.selID) == nil {
		s.selID, s.confirmRm = "", false
	}
	if s.selID == "" && len(v.Library) > 0 {
		s.selID = v.Library[0].ID
		if d := s.curDisplay(v); d != nil && s.itemByID(v, d.ItemID) != nil {
			s.selID = d.ItemID
		}
	}
	items := s.filtered(v)
	s.gridSel = -1
	for i, it := range items {
		if it.ID == s.selID {
			s.gridSel = i
		}
	}
	inUse := map[string][]string{}
	for _, d := range v.Displays {
		if d.ItemID != "" {
			inUse[d.ItemID] = append(inUse[d.ItemID], dispLabel(c, d))
		}
	}
	drop := ui.Column(c.Key("libdrop")).Grow(1).MinHeight(0).Radius(20).Children(func() {
		if len(v.Library) == 0 {
			if v.Importing || v.LastImport != "" {
				s.importStatus(c, v)
			}
			s.emptyLibrary(c, v)
			return
		}
		if it := s.itemByID(v, s.selID); it != nil {
			s.hero(c, v, *it, inUse[it.ID])
		}
		s.collectionHeader(c, v)
		if v.Importing || v.LastImport != "" {
			s.importStatus(c, v)
		}
		if len(items) == 0 {
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, icSearch).FontSize(26).TextColor(t.TextMuted)
				text(c, "没有符合条件的壁纸").TextColor(t.TextMuted)
			})
			return
		}
		if v.Config.UI.LibraryView == "list" {
			s.libList(c, v, items, inUse)
		} else {
			s.libGrid(c, v, items)
		}
	})
	if drop.FileDragOver() {
		drop.Background(t.Accent.Alpha(0.08)).Border(2, t.Accent.Alpha(0.5))
	}
	for _, p := range drop.DroppedFiles() {
		s.importPath(p)
	}
}

func (s *Settings) importStatus(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Row(c.Key("importStatus")).Padding(0, 32, 8, 32).Gap(6).AlignItems(ui.Center).Children(func() {
		if v.Importing {
			ui.Spinner(c).Size(14, 14)
		}
		text(c, v.LastImport).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1)
		if v.ImportQueued > 0 {
			textf(c, "队列中还有 %d 个请求", v.ImportQueued).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// collectionHeader：“壁纸库”标题 + 类型筛选（胶囊） | 数量 + 网格/列表。
func (s *Settings) collectionHeader(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Row(c.Key("filters")).Shrink(0).Padding(6, 32, 12, 32).Gap(14).AlignItems(ui.Center).Children(func() {
		display(c, tr(c, "壁纸库"), 19).SingleLine()
		capsule(c.Key("ftabs"), func() {
			labels := []string{"全部", "视频", "图片", "建议转码"}
			for i, l := range labels {
				n := 0
				for _, it := range v.Library {
					switch {
					case i == 0, i == 1 && it.Type == "video", i == 2 && it.Type == "image", i == 3 && it.Transcode == "recommended":
						n++
					}
				}
				if pillTab(c.Key("f"+itoa(i)), nil, fmt.Sprintf("%s %d", tr(c, l), n), s.filter == i).Padding(4, 12).FontSize(12).Clicked() {
					s.filter = i
				}
			}
		}).Shrink(1).MinWidth(0)
		ui.Box(c).Grow(1).Children(func() {})
		textf(c, "共 %d 张壁纸 · 拖入文件即可导入", len(v.Library)).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).Ellipsis("…")
		view := 0
		if v.Config.UI.LibraryView == "list" {
			view = 1
		}
		if segmented(c.Key("view"), &view, "网格", "列表").Label(tr(c, "视图")).Shrink(0).Changed() {
			lv := "grid"
			if view == 1 {
				lv = "list"
			}
			s.ctl.UpdateConfig(func(x *config.Config) { x.UI.LibraryView = lv })
		}
	})
}

func (s *Settings) emptyLibrary(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Column(c.Key("empty")).Grow(1).Margin(8, 32, 8, 32).Radius(24).Background(glass(t)).Border(1, hairline(t)).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
		ui.Box(c).Size(72, 72).Radius(22).Gradient(t.Accent, ui.Hex("#22d3ee"), 135).Shadow(0, 14, 34, 0, t.Accent.Alpha(0.4)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, icImport).FontSize(32).TextColor(ui.Hex("#ffffff"))
		})
		display(c, tr(c, "把视频、GIF 或图片拖到这里"), 22).Padding(8, 0, 0, 0)
		text(c, "支持 mp4 / mov / m4v / webm / gif / png / jpg / webp，以及 .wpk、Lively 壁纸包和 Wallpaper Engine 视频壁纸").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).Padding(10, 0, 0, 0).Children(func() {
			if iconButton(c.Key("imp2"), icPlus, "选择文件…", true).Radius(999).Padding(8, 18).Disabled(s.PickFiles == nil).Clicked() {
				s.importFiles()
			}
			if iconButton(c.Key("folder2"), icFolder, "导入文件夹…", false).Radius(999).Padding(8, 18).Disabled(s.PickFolder == nil).Clicked() {
				s.importFolder()
			}
		})
	})
}

// libGrid 是收藏网格：封面铺满卡片、标题压在底部渐变上。选择由卡片自己处理（单击选中、双击应用），
// 不用 GridView 的选中底色，避免方形高亮衬在圆角卡片后面。
func (s *Settings) libGrid(c *ui.Context, v app.View, items []config.Item) {
	t := c.Theme()
	screens := map[string][]string{}
	for _, d := range v.Displays {
		if d.ItemID != "" {
			screens[d.ItemID] = append(screens[d.ItemID], itoa(d.Index))
		}
	}
	s.grid.Selected = nil
	s.grid.Key = func(i int) any { return items[i].ID }
	s.grid.Label = func(i int) string { return items[i].Title }
	ui.GridView(c.Key("grid"), &s.grid, len(items), 196, 132, func(i int) {
		it := items[i]
		sel := i == s.gridSel
		card := ui.Box(c.Key("card"+it.ID)).Grow(1).Margin(7).Radius(14).Clip().Cursor(ui.CursorPointer).Border(1, hairline(t)).Children(func() {
			s.preview(c, it, 118)
			ui.Row(c).Absolute().Top(8).Left(8).Gap(4).Children(func() {
				if it.Transcode == "recommended" {
					chip(c, "建议转码", ui.Hex("#fcd34d")).Background(ui.RGBA(0, 0, 0, 0.5))
				}
			})
			if n := screens[it.ID]; len(n) > 0 {
				ui.Row(c).Absolute().Top(8).Right(8).Gap(4).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(t.Accent).Shadow(0, 2, 8, 0, t.Accent.Alpha(0.5)).Children(func() {
					ui.Icon(c, icMonitor).FontSize(11).TextColor(ui.Hex("#ffffff"))
					ui.Text(c, strings.Join(n, " ")).FontSize(11).Bold().TextColor(ui.Hex("#ffffff"))
				}).Label(tr(c, "使用中"))
			}
			ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(22, 12, 9, 12).LinearGradient(ui.LinearGradient{From: ui.RGBA(0, 0, 0, 0), To: ui.RGBA(0, 0, 0, 0.78), Angle: 180}).Children(func() {
				ui.Text(c, it.Title).Bold().FontSize(13).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
				meta := tr(c, originNames[it.Origin])
				if it.Codec != "" {
					meta = strings.ToUpper(it.Codec) + " · " + meta
				}
				text(c, meta).FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.7)).SingleLine().Ellipsis("…")
			})
		}).Label(it.Title)
		switch {
		case sel:
			card.Border(2, t.Accent).Shadow(0, 10, 28, -4, t.Accent.Alpha(0.55))
		case card.Hovered():
			card.Border(1, ui.RGBA(255, 255, 255, 0.35)).Shadow(0, 12, 26, -6, softShadow(t))
		default:
			card.Shadow(0, 6, 16, -6, softShadow(t))
		}
		if card.Clicked() && !sel {
			s.selID, s.confirmRm, s.detailsOpen = it.ID, false, false
		}
		if card.DoubleClicked() {
			s.applyCurrent(c, v, it.ID)
		}
	}).Grow(1).MinHeight(0).Padding(0, 25, 12, 25)
}

// placeholderColors 是没有首帧截图时按 id 取的稳定渐变色。
func placeholderColors(id string) (ui.Color, ui.Color) {
	h := 0
	for _, r := range id {
		h = h*31 + int(r)
	}
	pal := [][2]string{{"#6366f1", "#22d3ee"}, {"#f43f5e", "#f59e0b"}, {"#10b981", "#3b82f6"}, {"#8b5cf6", "#ec4899"}, {"#0ea5e9", "#14b8a6"}}
	p := pal[(h%len(pal)+len(pal))%len(pal)]
	return ui.Hex(p[0]), ui.Hex(p[1])
}

// preview 画缩略图；没有首帧截图时画渐变占位。
func (s *Settings) preview(c *ui.Context, it config.Item, h float32) {
	if bm := s.thumbOf(it); bm != nil {
		ui.Image(c, bm).Fit(ui.Cover).Height(h).FillWidth()
		return
	}
	from, to := placeholderColors(it.ID)
	ic := icVideo
	if it.Type == "image" {
		ic = icImage
	}
	ui.Box(c).Height(h).FillWidth().Gradient(from, to, 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, ic).FontSize(26).TextColor(ui.RGBA(255, 255, 255, 0.85))
	})
}

func (s *Settings) libList(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	ui.Scroll(c.Key("listscroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(8).Padding(0, 32, 20, 32).Children(func() {
			for i, it := range items {
				it, i := it, i
				row := ui.Row(c.Key("row"+it.ID)).Gap(14).Padding(8, 14, 8, 8).Radius(14).AlignItems(ui.Center).Background(glass(t)).Border(1, hairline(t)).Cursor(ui.CursorPointer).Children(func() {
					ui.Box(c).Width(104).Radius(9).Clip().Children(func() { s.preview(c, it, 58) })
					ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
						ui.Text(c, it.Title).Bold().FontSize(14).SingleLine().Ellipsis("…")
						meta := tr(c, typeNames[it.Type]) + " · " + tr(c, originNames[it.Origin])
						if it.Codec != "" {
							meta += " · " + strings.ToUpper(it.Codec)
						}
						text(c, meta).FontSize(12).TextColor(t.TextMuted)
					})
					if it.Transcode == "recommended" {
						chip(c, "建议转码", t.Warning)
					}
					if n := inUse[it.ID]; len(n) > 0 {
						chip(c, "使用中 · "+strings.Join(n, "、"), t.Accent)
					}
				})
				if i == s.gridSel {
					row.Border(2, t.Accent).Background(t.Accent.Alpha(0.10))
				}
				if row.Clicked() {
					s.selID, s.gridSel, s.confirmRm = it.ID, i, false
				}
				if row.DoubleClicked() {
					s.applyCurrent(c, v, it.ID)
				}
			}
		})
	})
}

// hero 是舞台：左边大预览，右边标题、应用目标、组属性（声音 / 填充 / 速度）和转码建议。
func (s *Settings) hero(c *ui.Context, v app.View, it config.Item, using []string) {
	t := c.Theme()
	w, h := c.Size()
	imgW := min(float32(600), max(float32(320), (w-64)*0.46))
	if h < 800 {
		imgW = min(imgW, 400)
	}
	imgH := imgW * 9 / 16
	// 舞台最高占到“窗口 − 导航 − Dock − 一行网格”；小窗口时舞台内部滚动，网格始终可见。
	ui.Scroll(c.Key("heroscroll")).Shrink(0).MaxHeight(max(200, h-370)).Children(func() {
		ui.Row(c.Key("hero")).Gap(32).Padding(4, 32, 22, 32).AlignItems(ui.Start).Children(func() {
			// 舞台：带辉光的大预览
			ui.Box(c.Key("stage")).Size(imgW, imgH).Shrink(0).Radius(20).Clip().Border(1, hairline(t)).Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
				s.preview(c, it, imgH)
				// 次要操作浮在舞台右上角
				ui.Row(c.Key("stageacts")).Absolute().Top(12).Right(12).Gap(6).Children(func() {
					info := roundIcon(c.Key("info"), icInfo, "详细信息")
					if info.Clicked() {
						s.detailsOpen = !s.detailsOpen
					}
					s.details(c, info, it)
					if roundIcon(c.Key("props"), icMonitor, "屏幕属性").Clicked() {
						s.page = pgDisplays
					}
					if roundIcon(c.Key("reveal"), icFolder, "打开位置").Disabled(s.ShowInFolder == nil).Clicked() {
						s.ShowInFolder(filepath.Join(it.Dir, it.Entry))
					}
					if roundIcon(c.Key("rm"), icTrash, "从壁纸库删除").TextColor(ui.Hex("#fca5a5")).Disabled(v.Importing).Clicked() {
						s.confirmRm = true
					}
				})
				if len(using) > 0 {
					ui.Row(c).Absolute().Left(12).Bottom(12).Gap(6).Padding(4, 10).Radius(999).Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(t.Success).Shadow(0, 0, 6, 1, t.Success.Alpha(0.7)).Children(func() {})
						ui.Text(c, tr(c, "正在用于：")+strings.Join(using, "、")).FontSize(11).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
					})
				}
			})
			ui.Column(c.Key("info")).Grow(1).Shrink(1).MinWidth(0).Gap(14).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					spec := []string{tr(c, typeNames[it.Type])}
					if it.Codec != "" {
						spec = append(spec, strings.ToUpper(it.Codec))
					}
					if it.Width > 0 && it.Height > 0 {
						spec = append(spec, fmt.Sprintf("%d×%d", it.Width, it.Height))
					}
					if it.DurationSec > 0 {
						sec := int(it.DurationSec + 0.5)
						spec = append(spec, fmt.Sprintf("%d:%02d", sec/60, sec%60))
					}
					eyebrow(c, strings.Join(spec, "  ·  "))
					display(c, it.Title, 30).SingleLine().Ellipsis("…")
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						text(c, originNames[it.Origin]).FontSize(13).TextColor(t.TextMuted).SingleLine()
						if it.Author != "" {
							ui.Text(c, "· "+it.Author).FontSize(13).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1)
						}
						switch it.Transcode {
						case "recommended":
							chip(c, "建议转码", t.Warning)
						case "done":
							chip(c, "已优化", t.Success)
						}
					})
					if it.Description != "" {
						ui.Text(c, it.Description).FontSize(13).LineHeight(1.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)
					}
				})
				s.applyBox(c, v, it)
				s.groupProps(c, v, it)
				s.overrideNotice(c, v)
				s.transcodeCard(c, v, it)
			})
		})
	})
	if alertDialog(c.Key("rmdlg"), &s.confirmRm, fmt.Sprintf(tr(c, "删除「%s」？"), it.Title), "会同时删除导入时复制的文件和缩略图，使用它的显示器会恢复系统壁纸。此操作不能撤销。", "取消", "删除") == 1 {
		if v.Importing {
			c.Toast(tr(c, "请等待导入完成后再删除。"))
			return
		}
		s.ctl.Remove(it.ID)
		c.Toast(tr(c, "删除请求已提交"))
	}
}

// roundIcon 是浮在舞台画面上的圆形次要操作（深色磨砂，任何画面上都清楚）。
func roundIcon(c *ui.Context, ic *ui.SVG, label string) ui.Element {
	return button(c, "").Radius(999).Padding(8).Background(ui.RGBA(0, 0, 0, 0.45)).Border(1, ui.RGBA(255, 255, 255, 0.16)).TextColor(ui.Hex("#ffffff")).Shrink(0).Children(func() {
		ui.Icon(c, ic).FontSize(15)
	}).Label(tr(c, label)).Tooltip(tr(c, label))
}

// details 是“详细信息”弹出层。
func (s *Settings) details(c *ui.Context, anchor ui.Element, it config.Item) {
	t := c.Theme()
	ui.Popover(c.Key("detailsPop"), anchor, &s.detailsOpen, func() {
		ui.Column(c).Width(300).Gap(0).Padding(4).Children(func() {
			kv := func(k, val string) {
				ui.Row(c).Gap(10).Padding(6, 0).BorderWidth(0, 0, 1, 0).BorderColor(hairline(t)).Children(func() {
					text(c, k).FontSize(12).TextColor(t.TextMuted).Width(64)
					ui.Text(c, val).FontSize(12).Grow(1).Wrap()
				})
			}
			kv("来源", tr(c, originNames[it.Origin]))
			if it.Author != "" {
				kv("作者", it.Author)
			}
			if it.Width > 0 && it.Height > 0 {
				kv("分辨率", fmt.Sprintf("%d × %d", it.Width, it.Height))
			}
			if it.SizeBytes > 0 {
				kv("文件大小", humanBytes(it.SizeBytes))
			}
			if len(it.Colors) > 0 {
				kv("颜色", strings.Join(it.Colors, " · "))
			}
			if len(it.Tags) > 0 {
				kv("标签", strings.Join(it.Tags, " · "))
			}
			if it.Description != "" {
				kv("简介", it.Description)
			}
			kv("导入时间", it.AddedAt.Local().Format("2006-01-02 15:04"))
			kv("入口文件", it.Entry)
			if it.Note != "" && it.Transcode != "recommended" {
				kv("备注", mediaNote(c.Locale(), it.Note))
			}
		})
	})
}

// groupProps 是“壁纸组属性”磨砂卡：声音（开关 + 音量）、填充、速度，随壁纸保存。
func (s *Settings) groupProps(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	props := it.Props
	setProps := func(fn func(p *config.ItemProps)) {
		p := props
		fn(&p)
		s.ctl.SetItemProps(it.ID, p)
	}
	eff := config.ResolvePlayback(props, config.DisplayOptions{}, v.Config.Render.Muted)
	ui.Column(c.Key("group")).Gap(12).Padding(14, 16).Radius(16).Background(glass(t)).Border(1, hairline(t)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			eyebrow(c, tr(c, "壁纸组属性")).Shrink(0)
			text(c, "随壁纸保存，应用到任何显示器都生效").FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1).Grow(1)
			if props != (config.ItemProps{}) {
				if button(c.Key("resetprops"), "恢复默认").Radius(999).FontSize(11).Padding(2, 10).Shrink(0).Clicked() {
					s.ctl.SetItemProps(it.ID, config.ItemProps{})
				}
			}
		})
		// 每项一行：图标 + 名称（固定宽度对齐）| 控件
		row := func(key string, ic *ui.SVG, label, sub string, control func()) {
			ui.Row(c.Key(key)).Gap(12).AlignItems(ui.Center).MinHeight(32).Children(func() {
				ui.Box(c).Size(28, 28).Shrink(0).Radius(9).Background(t.Accent.Alpha(0.14)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, ic).FontSize(14).TextColor(t.Accent)
				})
				ui.Column(c).Width(132).Shrink(0).Children(func() {
					text(c, label).FontSize(13).Bold()
					if sub != "" {
						text(c, sub).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
					}
				})
				ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(control)
			})
		}
		sub := "这张壁纸自己的设置"
		switch {
		case it.Type == "image":
			sub = "图片壁纸没有声音"
		case props.Audio == "":
			sub = "未单独设置，跟随默认静音设置"
		}
		row("g-snd", icSound, "声音", sub, func() {
			if it.Type == "image" {
				return
			}
			on := !eff.Muted
			if ui.Switch(c.Key("sound"), &on).Label(tr(c, "播放声音 开关")).Changed() {
				b := on
				setProps(func(p *config.ItemProps) {
					p.Audio = "mute"
					if b {
						p.Audio = "sound"
					}
				})
			}
			if !eff.Muted {
				vol := float64(eff.Volume)
				if ui.Slider(c.Key("volume"), &vol, 5, 100).Label(tr(c, "音量")).Grow(1).Shrink(1).MinWidth(60).MaxWidth(200).Changed() {
					n := (int(vol) + 2) / 5 * 5
					setProps(func(p *config.ItemProps) { p.Volume = max(5, n) })
				}
				textf(c, "%d%%", eff.Volume).FontSize(12).TextColor(t.TextMuted).Width(36)
			}
		})
		row("g-fit", icImage, "填充", "", func() {
			idx := 0
			for i, l := range fitLabels {
				if fitIDs[l] == eff.Fit {
					idx = i
				}
			}
			if segmented(c.Key("ifit"), &idx, fitLabels...).Label(tr(c, "壁纸填充方式")).Changed() {
				id := fitIDs[fitLabels[idx]]
				setProps(func(p *config.ItemProps) { p.Fit = id })
			}
		})
		row("g-speed", icGauge, "速度", "", func() {
			if it.Type == "image" {
				text(c, "静态图片不支持速度").FontSize(12).TextColor(t.TextMuted)
				return
			}
			speed := eff.Speed
			if ui.NumberInput(c.Key("ispeed"), &speed, 0.25, 4, 0.25).Label(tr(c, "壁纸播放速度")).Width(150).Changed() {
				sp := speed
				setProps(func(p *config.ItemProps) { p.Speed = sp })
			}
			text(c, "倍速").FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
	})
}

// transcodeCard 是转码建议（F-07）：一条带暖色描边的磨砂条。
func (s *Settings) transcodeCard(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	st := v.Transcoding[it.ID]
	if it.Transcode != "recommended" && st == "" {
		return
	}
	ui.Row(c.Key("tc")).Gap(12).Padding(12, 14).Radius(16).Background(t.Warning.Alpha(0.10)).Border(1, t.Warning.Alpha(0.32)).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(32, 32).Shrink(0).Radius(10).Background(t.Warning.Alpha(0.18)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, icWand).FontSize(16).TextColor(t.Warning)
		})
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			text(c, "建议优化转码").Bold().FontSize(13)
			note := mediaNote(c.Locale(), it.Note)
			if note == "" {
				note = tr(c, "这个文件的编码或规格不利于省电播放。")
			}
			// Source/backend notes remain verbatim; the application-owned
			// promise depends on the encoder's verified capabilities.
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted).Wrap()
			hint := "标准转换将验证 30 fps、无音轨和目标尺寸；原文件保留。"
			if !v.Encoder.Strict {
				hint = "兼容转换能力取决于编码器，不保证固定帧率或去音轨；原文件保留。"
			}
			text(c, hint).FontSize(11).TextColor(t.TextMuted).Wrap()
			switch {
			case st == "等待转码…":
				text(c, "已加入转码队列，等待当前任务完成。").FontSize(12).TextColor(t.TextMuted).Wrap()
			case st == "正在转码…":
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					text(c, "正在转码，可以继续使用其他功能…").FontSize(12)
				})
			case !v.HasEncoder:
				text(c, s.noEncoderHint()).FontSize(12).TextColor(t.Warning).Wrap()
			case st != "":
				text(c, st).FontSize(12).TextColor(t.Danger).Wrap()
			}
		})
		switch {
		case st == "等待转码…" || st == "正在转码…":
		case !v.HasEncoder:
			if button(c.Key("tcset"), "设置编码器…").Radius(999).Shrink(0).Clicked() {
				s.page = pgPerf
			}
		default:
			if iconButton(c.Key("tcgo"), icWand, "一键优化", false).Radius(999).Padding(6, 14).Shrink(0).Clicked() {
				s.ctl.Transcode(it.ID)
			}
		}
	})
}

// overrideNotice：目标显示器覆盖了壁纸自带属性时提示，并可清除覆盖。
func (s *Settings) overrideNotice(c *ui.Context, v app.View) {
	t := c.Theme()
	d := s.curDisplay(v)
	if d == nil || !d.Options.Overrides() {
		return
	}
	var parts []string
	if d.Options.Fit != "" {
		parts = append(parts, tr(c, "填充")+" "+tr(c, fitNames[d.Options.Fit]))
	}
	if d.Options.Speed != 0 {
		parts = append(parts, fmt.Sprintf(tr(c, "速度 %g 倍"), d.Options.Speed))
	}
	switch d.Options.Audio {
	case "mute":
		parts = append(parts, tr(c, "静音"))
	case "sound":
		parts = append(parts, tr(c, "播放声音"))
	}
	ui.Column(c.Key("override")).Gap(6).Padding(10).Radius(8).Background(t.Accent.Alpha(0.08)).Border(1, t.Accent.Alpha(0.3)).Children(func() {
		ui.Text(c, fmt.Sprintf(tr(c, "%s 单独覆盖了：%s"), dispLabel(c, *d), strings.Join(parts, tr(c, "、")))).FontSize(12).Wrap()
		text(c, "在这块屏上以覆盖为准，其他屏使用壁纸自带的属性。").FontSize(11).TextColor(t.TextMuted).Wrap()
		if button(c.Key("clearov"), "清除覆盖").FontSize(12).Clicked() {
			o := d.Options
			o.Fit, o.Speed, o.Audio = "", 0, ""
			s.ctl.SetDisplayOptions(d.ID, o)
			c.Toast(tr(c, "已清除覆盖"))
		}
	})
}

func (s *Settings) noEncoderHint() string {
	if s.GOOS == "darwin" {
		return "未找到可用编码器。macOS 默认使用系统 AVFoundation；可选配置 ffmpeg，avconvert 仅作有限兼容兜底。"
	}
	return "没有找到 ffmpeg。本应用不附带编码器（许可证原因），请自行安装 ffmpeg 并加入 PATH，或在“播放与性能”里指定路径。"
}

// applyCurrent 把壁纸应用到当前显示器（双击 / 回车）。
func (s *Settings) applyCurrent(c *ui.Context, v app.View, itemID string) {
	d := s.curDisplay(v)
	if d == nil {
		return
	}
	s.ctl.Assign(d.ID, itemID)
	c.Toast(tr(c, "已应用到 ") + dispLabel(c, *d))
}

const applyAllLabel = "所有显示器"

// applyBox 是详情面板的“应用到”：下拉选择 当前显示器 / 某一块 / 所有显示器，再点“应用”。
func (s *Settings) applyBox(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	if len(v.Displays) == 0 {
		text(c, "没有检测到显示器").FontSize(12).TextColor(t.TextMuted)
		return
	}
	opts := make([]string, 0, len(v.Displays)+1)
	ids := map[string]string{}
	for _, d := range v.Displays {
		l := dispLabel(c, d)
		if d.Primary {
			l += tr(c, "（主屏）")
		}
		opts = append(opts, l)
		ids[l] = d.ID
	}
	if len(v.Displays) > 1 {
		opts = append(opts, applyAllLabel)
	}
	valid := s.applySel == applyAllLabel && len(v.Displays) > 1
	if _, ok := ids[s.applySel]; ok {
		valid = true
	}
	if !valid {
		cur := s.curDisplay(v)
		for l, id := range ids {
			if id == cur.ID {
				s.applySel = l
			}
		}
	}
	ui.Row(c.Key("applyrow")).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
		eyebrow(c, tr(c, "应用到")).Shrink(0).Padding(0, 4, 0, 0)
		selectControl(c.Key("applyto"), &s.applySel, opts).Label(tr(c, "应用到显示器")).Radius(999).Grow(1).Shrink(1).MinWidth(120).MaxWidth(260)
		if iconButton(c.Key("apply"), icCheck, "应用", true).Radius(999).Padding(7, 18).Shrink(0).Shadow(0, 6, 16, 0, t.Accent.Alpha(0.35)).Clicked() {
			if s.applySel == applyAllLabel {
				s.ctl.AssignAll(it.ID)
				c.Toast(tr(c, "已应用到所有显示器"))
			} else if id := ids[s.applySel]; id != "" {
				s.ctl.Assign(id, it.ID)
				s.dispSel = id
				c.Toast(tr(c, "已应用到 ") + strings.TrimSuffix(s.applySel, tr(c, "（主屏）")))
			}
		}
	})
}
