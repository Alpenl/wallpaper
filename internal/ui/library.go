package ui

import (
	"fmt"
	"path/filepath"
	"sort"
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

var filterLabels = []string{"全部", "视频", "图片", "建议转码"}

func (s *Settings) importFiles() {
	if s.PickFiles == nil {
		return
	}
	for _, p := range s.PickFiles() {
		s.importPath(p)
	}
}

// library 是主页，布局参照 Wallpaper Engine：左边是工具条 + 封面网格，右边是选中壁纸的属性栏。
// 选中项默认是目标显示器正在播放的那张，所以属性栏总有内容；应用按钮在底部操作栏。
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
	if s.sortBy == "name" {
		sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title) })
	}
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
	drop := ui.Row(c.Key("libdrop")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c.Key("libmain")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
			if len(v.Library) == 0 {
				if v.Importing || v.LastImport != "" {
					s.importStatus(c, v)
				}
				s.emptyLibrary(c, v)
				return
			}
			s.toolbar(c, v)
			if v.Importing || v.LastImport != "" {
				s.importStatus(c, v)
			}
			if len(items) == 0 {
				ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
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
		if it := s.itemByID(v, s.selID); it != nil {
			s.sidePanel(c, v, *it, inUse[it.ID])
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
	ui.Row(c.Key("importStatus")).Padding(0, 20, 8, 20).Gap(6).AlignItems(ui.Center).Children(func() {
		if v.Importing {
			ui.Spinner(c).Size(14, 14)
		}
		text(c, v.LastImport).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1)
		if v.ImportQueued > 0 {
			textf(c, "队列中还有 %d 个请求", v.ImportQueued).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// toolbar：搜索 | 类型筛选 | 数量 … 排序 | 网格/列表。
func (s *Settings) toolbar(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Row(c.Key("filters")).Shrink(0).Wrap().Padding(12, 20, 10, 20).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("q"), &s.query).Placeholder(tr(c, "搜索名称、标签、作者")).Label(tr(c, "搜索壁纸")).Radius(2).Width(240).Shrink(1).MinWidth(120)
		for i, l := range filterLabels {
			n := 0
			for _, it := range v.Library {
				switch {
				case i == 0, i == 1 && it.Type == "video", i == 2 && it.Type == "image", i == 3 && it.Transcode == "recommended":
					n++
				}
			}
			b := button(c.Key("f"+itoa(i)), fmt.Sprintf("%s %d", tr(c, l), n)).Radius(2).Padding(4, 10).FontSize(12)
			if s.filter == i {
				b.Background(t.Accent.Alpha(0.18)).TextColor(t.Text).Border(1, t.Accent)
			}
			if b.Clicked() {
				s.filter = i
			}
		}
		ui.Box(c).Grow(1).Children(func() {})
		textf(c, "共 %d 张壁纸 · 拖入文件即可导入", len(v.Library)).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).Ellipsis("…")
		sortLabel := map[string]string{"": "最近导入", "name": "名称"}[s.sortBy]
		if selectControl(c.Key("sort"), &sortLabel, []string{"最近导入", "名称"}).Label(tr(c, "排序")).Radius(2).Width(120).Shrink(0).Changed() {
			s.sortBy = map[string]string{"最近导入": "", "名称": "name"}[sortLabel]
		}
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
	ui.Column(c.Key("empty")).Grow(1).Margin(14, 20, 14, 20).Radius(3).Background(panelBg(t)).Border(1, lineColor(t)).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Children(func() {
		heading(c, tr(c, "把视频、GIF 或图片拖到这里"), 22)
		text(c, "支持 mp4 / mov / m4v / webm / gif / png / jpg / webp，以及 .wpk、Lively 壁纸包和 Wallpaper Engine 视频壁纸").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Padding(12, 0, 0, 0).Children(func() {
			if primaryButton(c.Key("imp2"), "选择文件…").Radius(2).Padding(7, 20).Disabled(s.PickFiles == nil).Clicked() {
				s.importFiles()
			}
			if button(c.Key("folder2"), "导入文件夹…").Radius(2).Padding(7, 20).Disabled(s.PickFolder == nil).Clicked() {
				s.importFolder()
			}
		})
	})
}

// libGrid 是封面网格：方形封面铺满格子、标题居中压在底部暗带上，选中是强调色描边。
// 选择由格子自己处理（单击选中、双击应用到目标屏）。
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
	ui.GridView(c.Key("grid"), &s.grid, len(items), 168, 168, func(i int) {
		it := items[i]
		sel := i == s.gridSel
		tile := ui.Box(c.Key("card" + it.ID)).Grow(1).Margin(3).Radius(2).Clip().Cursor(ui.CursorPointer).Background(t.Surface).Children(func() {
			s.preview(c, it, 162)
			if n := screens[it.ID]; len(n) > 0 {
				ui.Text(c, tr(c, "屏 ")+strings.Join(n, "、")).Absolute().Top(6).Left(6).FontSize(11).Padding(1, 6).Radius(2).Background(t.Accent).TextColor(ui.Hex("#ffffff")).Label(tr(c, "使用中"))
			}
			if it.Transcode == "recommended" {
				ui.Text(c, tr(c, "建议转码")).Absolute().Top(6).Right(6).FontSize(11).Padding(1, 6).Radius(2).Background(ui.RGBA(0, 0, 0, 0.6)).TextColor(ui.Hex("#fcd34d"))
			}
			ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(6, 8).Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, it.Title).FontSize(13).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
				meta := tr(c, originNames[it.Origin])
				if it.Codec != "" {
					meta = strings.ToUpper(it.Codec) + " · " + meta
				}
				text(c, meta).FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.65)).SingleLine().Ellipsis("…")
			})
		}).Label(it.Title)
		switch {
		case sel:
			tile.Border(2, t.Accent)
		case tile.Hovered():
			tile.Border(1, t.Accent.Alpha(0.7))
		}
		if tile.Clicked() && !sel {
			s.selID, s.confirmRm = it.ID, false
		}
		if tile.DoubleClicked() {
			s.applyCurrent(c, v, it.ID)
		}
	}).Grow(1).MinHeight(0).Padding(0, 17, 10, 17)
}

// placeholderColors 是没有首帧截图时按 id 取的稳定渐变色。
func placeholderColors(id string) (ui.Color, ui.Color) {
	h := 0
	for _, r := range id {
		h = h*31 + int(r)
	}
	pal := [][2]string{{"#3b4a6b", "#5b7fa6"}, {"#5a3d4a", "#9b6a5c"}, {"#2f5a4c", "#4f8a7a"}, {"#4a3d6b", "#7b6aa6"}, {"#2f4f6b", "#4f7f9a"}}
	p := pal[(h%len(pal)+len(pal))%len(pal)]
	return ui.Hex(p[0]), ui.Hex(p[1])
}

// preview 画缩略图；没有首帧截图时画纯色渐变和类型文字。
func (s *Settings) preview(c *ui.Context, it config.Item, h float32) {
	if bm := s.thumbOf(it); bm != nil {
		ui.Image(c, bm).Fit(ui.Cover).Height(h).FillWidth()
		return
	}
	from, to := placeholderColors(it.ID)
	ui.Box(c).Height(h).FillWidth().Gradient(from, to, 160).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		text(c, typeNames[it.Type]).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.7))
	})
}

func (s *Settings) libList(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	ui.Scroll(c.Key("listscroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(2).Padding(0, 20, 12, 20).Children(func() {
			for i, it := range items {
				it, i := it, i
				row := ui.Row(c.Key("row"+it.ID)).Gap(12).Padding(4, 12, 4, 4).Radius(2).AlignItems(ui.Center).Background(panelBg(t)).Cursor(ui.CursorPointer).Children(func() {
					ui.Box(c).Width(96).Radius(2).Clip().Children(func() { s.preview(c, it, 54) })
					ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
						ui.Text(c, it.Title).FontSize(13).FontWeight(500).SingleLine().Ellipsis("…")
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
					row.Border(2, t.Accent)
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

// sidePanel 是右侧属性栏：预览、标题、标签，操作按钮，“属性”（随壁纸保存的声音 / 音量 / 速度 / 填充），转码建议和详细信息。
func (s *Settings) sidePanel(c *ui.Context, v app.View, it config.Item, using []string) {
	t := c.Theme()
	ui.Column(c.Key("detail")).Width(340).Shrink(0).MinHeight(0).Background(panelBg(t)).BorderWidth(0, 0, 0, 1).BorderColor(lineColor(t)).Children(func() {
		ui.Box(c).Height(191).Shrink(0).Margin(14, 14, 0, 14).Radius(2).Clip().Children(func() { s.preview(c, it, 191) })
		ui.Scroll(c.Key("dscroll")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(12, 16, 16, 16).Gap(10).Children(func() {
				ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
					ui.Text(c, it.Title).FontSize(19).FontWeight(500).Wrap().TextAlign(ui.Center)
					meta := tr(c, originNames[it.Origin])
					if it.Author != "" {
						meta += " · " + it.Author
					}
					ui.Text(c, meta).FontSize(13).TextColor(t.TextMuted).Wrap().TextAlign(ui.Center)
				})
				ui.Row(c.Key("tags")).Wrap().Gap(6).Justify(ui.Center).Children(func() {
					tag := func(label string) { ui.Text(c, label).FontSize(12).Padding(3, 9).Radius(2).Background(t.Surface) }
					tag(tr(c, typeNames[it.Type]))
					if it.Codec != "" {
						tag(strings.ToUpper(it.Codec))
					}
					if it.Width > 0 && it.Height > 0 {
						tag(fmt.Sprintf("%d x %d", it.Width, it.Height))
					}
					switch it.Transcode {
					case "recommended":
						chip(c, "建议转码", t.Warning).FontSize(12).Padding(3, 9)
					case "done":
						chip(c, "已优化", t.Success).FontSize(12).Padding(3, 9)
					}
				})
				if len(using) > 0 {
					ui.Text(c, tr(c, "正在用于：")+strings.Join(using, "、")).FontSize(12).TextColor(t.Accent).Wrap().TextAlign(ui.Center).AlignSelf(ui.Center)
				}
				if button(c.Key("rm"), "从壁纸库删除").Radius(2).FillWidth().Background(t.Danger).TextColor(ui.Hex("#ffffff")).Border(1, t.Danger).Disabled(v.Importing).Clicked() {
					s.confirmRm = true
				}
				ui.Row(c.Key("acts")).Gap(6).Children(func() {
					if button(c.Key("props"), "屏幕属性").Radius(2).Grow(1).Basis(0).Clicked() {
						s.page = pgDisplays
					}
					if button(c.Key("reveal"), "打开位置").Radius(2).Grow(1).Basis(0).Disabled(s.ShowInFolder == nil).Clicked() {
						s.ShowInFolder(filepath.Join(it.Dir, it.Entry))
					}
				})
				s.groupProps(c, v, it)
				s.overrideNotice(c, v)
				s.transcodeCard(c, v, it)
				s.details(c, it)
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

// propRow 是属性栏的一行：左边名称，右边控件。
func propRow(c *ui.Context, key, label string, control func()) {
	ui.Row(c.Key(key)).Gap(10).MinHeight(30).AlignItems(ui.Center).Children(func() {
		text(c, label).FontSize(13).Width(76).Shrink(0).SingleLine()
		ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(control)
	})
}

// groupProps 是“属性”：声音、音量、播放速度、填充都随壁纸保存，应用到任何显示器都生效。
func (s *Settings) groupProps(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	props := it.Props
	setProps := func(fn func(p *config.ItemProps)) {
		p := props
		fn(&p)
		s.ctl.SetItemProps(it.ID, p)
	}
	eff := config.ResolvePlayback(props, config.DisplayOptions{}, v.Config.Render.Muted)
	sectionHeader(c, "propshdr", tr(c, "属性"), func() {
		if props != (config.ItemProps{}) {
			if button(c.Key("resetprops"), "恢复默认").Radius(2).FontSize(11).Padding(2, 8).Clicked() {
				s.ctl.SetItemProps(it.ID, config.ItemProps{})
			}
		}
	})
	text(c, "随壁纸保存，应用到任何显示器都生效").FontSize(11).TextColor(t.TextMuted).Wrap()
	ui.Column(c.Key("group")).Gap(4).Children(func() {
		if it.Type == "image" {
			propRow(c, "g-snd", "声音", func() { text(c, "图片壁纸没有声音").FontSize(12).TextColor(t.TextMuted) })
		} else {
			propRow(c, "g-snd", "声音", func() {
				sub := "这张壁纸自己的设置"
				if props.Audio == "" {
					sub = "未单独设置，跟随默认静音设置"
				}
				text(c, sub).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1)
				on := !eff.Muted
				if ui.Checkbox(c.Key("sound"), &on, "").Label(tr(c, "播放声音 开关")).Shrink(0).Changed() {
					b := on
					setProps(func(p *config.ItemProps) {
						p.Audio = "mute"
						if b {
							p.Audio = "sound"
						}
					})
				}
			})
			propRow(c, "g-vol", "音量", func() {
				vol := float64(eff.Volume)
				if ui.Slider(c.Key("volume"), &vol, 5, 100).Label(tr(c, "音量")).Disabled(eff.Muted).Grow(1).Shrink(1).MinWidth(60).Changed() {
					n := (int(vol) + 2) / 5 * 5
					setProps(func(p *config.ItemProps) { p.Volume = max(5, n) })
				}
				textf(c, "%d%%", eff.Volume).FontSize(12).Width(36).TextAlign(ui.End)
			})
		}
		propRow(c, "g-speed", "播放速度", func() {
			if it.Type == "image" {
				text(c, "静态图片不支持速度").FontSize(12).TextColor(t.TextMuted)
				return
			}
			speed := eff.Speed
			if ui.StepSlider(c.Key("ispeed"), &speed, 0.25, 4, 0.25).Label(tr(c, "壁纸播放速度")).Grow(1).Shrink(1).MinWidth(60).Changed() {
				sp := speed
				setProps(func(p *config.ItemProps) { p.Speed = sp })
			}
			textf(c, "%.2gx", eff.Speed).FontSize(12).Width(36).TextAlign(ui.End)
		})
		propRow(c, "g-fit", "填充", func() {
			fit := fitNames[eff.Fit]
			if selectControl(c.Key("ifit"), &fit, fitLabels).Label(tr(c, "壁纸填充方式")).Radius(2).Grow(1).Changed() {
				id := fitIDs[fit]
				setProps(func(p *config.ItemProps) { p.Fit = id })
			}
		})
	})
}

// transcodeCard 是转码建议（F-07）。
func (s *Settings) transcodeCard(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	st := v.Transcoding[it.ID]
	if it.Transcode != "recommended" && st == "" {
		return
	}
	sectionHeader(c, "tchdr", tr(c, "建议优化转码"), nil)
	ui.Column(c.Key("tc")).Gap(6).Children(func() {
		note := mediaNote(c.Locale(), it.Note)
		if note == "" {
			note = tr(c, "这个文件的编码或规格不利于省电播放。")
		}
		// Source/backend notes remain verbatim; the application-owned
		// promise depends on the encoder's verified capabilities.
		ui.Text(c, note).FontSize(12).TextColor(t.Warning).Wrap()
		hint := "标准转换将验证 30 fps、无音轨和目标尺寸；原文件保留。"
		if !v.Encoder.Strict {
			hint = "兼容转换能力取决于编码器，不保证固定帧率或去音轨；原文件保留。"
		}
		text(c, hint).FontSize(12).TextColor(t.TextMuted).Wrap()
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
			if button(c.Key("tcset"), "设置编码器…").Radius(2).Clicked() {
				s.page = pgPerf
			}
		default:
			if st != "" {
				text(c, st).FontSize(12).TextColor(t.Danger).Wrap()
			}
			if button(c.Key("tcgo"), "一键优化").Radius(2).FillWidth().Clicked() {
				s.ctl.Transcode(it.ID)
			}
		}
	})
}

// details 是“详细信息”小节。
func (s *Settings) details(c *ui.Context, it config.Item) {
	t := c.Theme()
	sectionHeader(c, "metahdr", tr(c, "详细信息"), nil)
	ui.Column(c.Key("meta")).Children(func() {
		kv := func(k, val string) {
			ui.Row(c).Gap(10).Padding(5, 0).Children(func() {
				text(c, k).FontSize(12).TextColor(t.TextMuted).Width(64).Shrink(0)
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
		if it.DurationSec > 0 {
			sec := int(it.DurationSec + 0.5)
			kv("时长", fmt.Sprintf("%d:%02d", sec/60, sec%60))
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
	ui.Column(c.Key("override")).Gap(6).Padding(10).Radius(2).Background(t.Accent.Alpha(0.08)).Border(1, t.Accent.Alpha(0.3)).Children(func() {
		ui.Text(c, fmt.Sprintf(tr(c, "%s 单独覆盖了：%s"), dispLabel(c, *d), strings.Join(parts, tr(c, "、")))).FontSize(12).Wrap()
		text(c, "在这块屏上以覆盖为准，其他屏使用壁纸自带的属性。").FontSize(11).TextColor(t.TextMuted).Wrap()
		if button(c.Key("clearov"), "清除覆盖").Radius(2).FontSize(12).Clicked() {
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
