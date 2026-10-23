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

var filterLabels = []string{"全部", "视频", "图片", "建议转码", "网页", "Shader"}

func (s *Settings) importFiles() {
	if s.PickFiles == nil {
		return
	}
	s.pickerStatus = ""
	paths := s.PickFiles()
	if len(paths) == 0 && s.pickerStatus == "" {
		s.pickerStatus = "已取消文件选择"
	}
	for _, p := range paths {
		s.importPath(p)
	}
}

// library renders the left content area and handles file drops.
// The window frame owns the full-height property panel.
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
	drop := ui.Row(c.Key("libdrop")).Grow(1).Shrink(1).Basis(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c.Key("libmain")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
			if len(v.Library) == 0 {
				s.emptyLibrary(c, v)
				return
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
	ui.Row(c.Key("importStatus")).Grow(1).Shrink(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
		if v.Importing {
			ui.Spinner(c).Size(14, 14)
		}
		message := v.LastImport
		if s.pickerStatus != "" {
			message = s.pickerStatus
		}
		text(c, message).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1)
		if v.Importing || v.ImportQueued > 0 {
			if ghostButton(c.Key("cancel-import"), "取消导入").Clicked() {
				s.ctl.CancelImports()
			}
		}
		if v.ImportQueued > 0 {
			textf(c, "队列中还有 %d 个请求", v.ImportQueued).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// toolbar shares the navigation row: search, filter menu, sort and view.
// Budgets include CJK font headroom, fixed controls, gaps and a 140px search.
const libraryToolbarWide float32 = 960
const libraryToolbarNav float32 = 680

func (s *Settings) toolbar(c *ui.Context, v app.View, narrow bool) {
	ui.Row(c.Key("filters")).Grow(1).Shrink(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("q"), &s.query).Placeholder(tr(c, "搜索名称、标签、作者")).Label(tr(c, "搜索壁纸")).Radius(9).Padding(4, 8).FontSize(13).Height(26).Grow(1).Shrink(1).MinWidth(140)
		label := "筛选结果"
		if narrow {
			label = "筛选与显示"
		}
		softButton(c.Key("ftabs"), label).Menu(func(m *ui.Menu) {
			for i, l := range filterLabels {
				n := 0
				for _, it := range v.Library {
					switch {
					case i == 0, i == 1 && it.Type == "video", i == 2 && it.Type == "image", i == 3 && it.Transcode == "recommended", i == 4 && it.Type == "web", i == 5 && it.Type == "shader":
						n++
					}
				}
				if i >= 4 && n == 0 {
					continue
				}
				if m.Item(fmt.Sprintf("%s %d", tr(c, l), n)).Checked(s.filter == i).Chosen() {
					s.filter = i
				}
			}
			if narrow {
				m.Separator()
				for _, o := range []struct{ id, label string }{{"", "最近导入"}, {"name", "名称"}} {
					if m.Item(tr(c, o.label)).Checked(s.sortBy == o.id).Chosen() {
						s.sortBy = o.id
					}
				}
				m.Separator()
				for _, o := range []struct{ id, label string }{{"grid", "网格"}, {"list", "列表"}} {
					if m.Item(tr(c, o.label)).Checked(v.Config.UI.LibraryView == o.id || (o.id == "grid" && v.Config.UI.LibraryView != "list")).Chosen() {
						s.ctl.UpdateConfig(func(x *config.Config) { x.UI.LibraryView = o.id })
					}
				}
			}
		})
		if narrow {
			return
		}
		sortLabel := map[string]string{"": "最近导入", "name": "名称"}[s.sortBy]
		if selectControl(c.Key("sort"), &sortLabel, []string{"最近导入", "名称"}).Label(tr(c, "排序")).Radius(9).Width(118).Height(26).FontSize(12).Shrink(0).Changed() {
			s.sortBy = map[string]string{"最近导入": "", "名称": "name"}[sortLabel]
		}
		view := v.Config.UI.LibraryView
		segmentGroup(c.Key("view"), func() {
			for _, o := range []struct{ id, label string }{{"grid", "网格"}, {"list", "列表"}} {
				id := o.id
				if segment(c.Key("v-"+id), o.label, view == id || (id == "grid" && view != "list")).FontSize(12).Clicked() {
					s.ctl.UpdateConfig(func(x *config.Config) { x.UI.LibraryView = id })
				}
			}
		}).Shrink(0)
	})
}

func (s *Settings) emptyLibrary(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Column(c.Key("empty")).Grow(1).Margin(18, 24, 18, 24).Radius(18).Background(panelBg(t)).Border(1, lineColor(t)).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Clip().Children(func() {
		glow := float32(0.16)
		if t.Dark {
			glow = 0.28
		}
		// 顶部一抹强调色，让空页面不是一块死灰
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(260).LinearGradient(ui.LinearGradient{From: t.Accent.Alpha(glow), To: ui.Transparent, Angle: 180}).Children(func() {})
		overline(c, tr(c, "开始使用"))
		heading(c, tr(c, "把视频、GIF 或图片拖到这里"), 26)
		text(c, "支持视频、图片、.glsl / .frag Shader、.wpk、Lively 壁纸包和 Wallpaper Engine 本地壁纸").FontSize(13).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).Padding(14, 0, 0, 0).Children(func() {
			if accentButton(c.Key("imp2"), "选择文件…").Disabled(s.PickFiles == nil).Clicked() {
				s.importFiles()
			}
			if softButton(c.Key("folder2"), "导入文件夹…").Padding(8, 18).Disabled(s.PickFolder == nil).Clicked() {
				s.importFolder()
			}
		})
	})
}

// libGrid uses responsive 16:9 covers and a 6px gutter.
// 选择由格子自己处理（单击选中、双击应用到目标屏）。
func (s *Settings) libGrid(c *ui.Context, v app.View, items []config.Item) {
	t := c.Theme()
	screens := map[string][]string{}
	for _, d := range v.Displays {
		if d.ItemID != "" {
			screens[d.ItemID] = append(screens[d.ItemID], itoa(d.Index))
		}
	}
	w, _ := c.Size()
	available := w - 320 - 24
	cols := max(1, int((available+6)/226))
	tileHeight := (available - float32(cols-1)*6) / float32(cols) * 9 / 16
	s.grid.Selected = nil
	s.grid.Key = func(i int) any { return items[i].ID }
	s.grid.Label = func(i int) string { return items[i].Title }
	ui.GridView(c.Key("grid"), &s.grid, len(items), 220, tileHeight, func(i int) {
		it := items[i]
		sel := i == s.gridSel
		tile := ui.Box(c.Key("card"+it.ID)).Grow(1).Margin(-2).Radius(6).Clip().Cursor(ui.CursorPointer).Background(t.Surface).Border(1, lineColor(t)).Children(func() {
			s.preview(c, it, tileHeight)
			ui.Row(c).Absolute().Top(8).Left(8).Right(8).Gap(6).Children(func() {
				if n := screens[it.ID]; len(n) > 0 {
					ui.Row(c).Gap(5).Padding(2, 8).Radius(999).Background(ui.RGBA(10, 12, 16, 0.62)).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#34d399")).Children(func() {})
						ui.Text(c, tr(c, "屏 ")+strings.Join(n, "、")).FontSize(11).FontWeight(600).TextColor(ui.Hex("#ffffff"))
					}).Label(tr(c, "使用中"))
				}
				ui.Box(c).Grow(1).Children(func() {})
				if it.Transcode == "recommended" {
					ui.Text(c, tr(c, "建议转码")).FontSize(11).FontWeight(600).Padding(2, 8).Radius(999).Background(ui.RGBA(10, 12, 16, 0.62)).TextColor(ui.Hex("#fbbf24"))
				}
			})
			ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(26, 12, 10, 12).Gap(1).LinearGradient(ui.LinearGradient{From: ui.RGBA(0, 0, 0, 0), To: ui.RGBA(0, 0, 0, 0.78), Angle: 180}).Children(func() {
				ui.Text(c, it.Title).FontSize(13).FontWeight(600).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
				meta := tr(c, originNames[it.Origin])
				if it.Codec != "" {
					meta = strings.ToUpper(it.Codec) + " · " + meta
				}
				text(c, meta).FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.62)).SingleLine().Ellipsis("…")
			})
		}).Label(it.Title)
		switch {
		case sel:
			tile.Border(2, t.Accent).Shadow(0, 8, 24, -6, t.Accent.Alpha(0.6))
			if t.Dark {
				tile.Shadow(0, 0, 20, 1, t.Accent.Alpha(0.75))
			}
		case tile.Hovered():
			tile.Border(1, t.Accent.Alpha(0.55)).Shadow(0, 10, 22, -8, shadowColor(t))
		default:
			tile.Shadow(0, 4, 12, -6, shadowColor(t))
		}
		if tile.Clicked() && !sel {
			s.selID, s.confirmRm = it.ID, false
		}
		if tile.DoubleClicked() {
			s.applyCurrent(c, v, it.ID)
		}
	}).Grow(1).MinHeight(0).Padding(6, 12, 12, 12).Gap(0)
}

// placeholderColors 是没有首帧截图时按 id 取的稳定渐变色。
func placeholderColors(id string) (ui.Color, ui.Color) {
	h := 0
	for _, r := range id {
		h = h*31 + int(r)
	}
	pal := [][2]string{{"#334a7d", "#5f86c4"}, {"#6b3550", "#c0706a"}, {"#215a4f", "#4fa08a"}, {"#4b3a7d", "#8f73c9"}, {"#24506e", "#4f8fb8"}}
	p := pal[(h%len(pal)+len(pal))%len(pal)]
	return ui.Hex(p[0]), ui.Hex(p[1])
}

// preview 画缩略图；没有首帧截图时画渐变和类型文字。
func (s *Settings) preview(c *ui.Context, it config.Item, h float32) {
	if bm := s.thumbOf(it); bm != nil {
		ui.Image(c, bm).Fit(ui.Cover).Height(h).FillWidth()
		return
	}
	from, to := placeholderColors(it.ID)
	ui.Box(c).Height(h).FillWidth().Gradient(from, to, 150).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		overline(c, tr(c, typeNames[it.Type])).TextColor(ui.RGBA(255, 255, 255, 0.7))
	})
}

func (s *Settings) libList(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	ui.Scroll(c.Key("listscroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(6).Padding(6, 12, 12, 12).Children(func() {
			for i, it := range items {
				it, i := it, i
				row := ui.Row(c.Key("row"+it.ID)).Height(44).Gap(8).Padding(2, 8, 2, 2).Radius(6).AlignItems(ui.Center).Background(panelBg(t)).Border(1, lineColor(t)).Cursor(ui.CursorPointer).Children(func() {
					ui.Box(c).Width(68).Radius(8).Clip().Children(func() { s.preview(c, it, 38) })
					ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
						ui.Text(c, it.Title).FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
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
					row.Border(2, t.Accent).Background(t.Accent.Alpha(0.08))
				} else if row.Hovered() {
					row.Background(t.SurfaceHover)
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

// propertyPanel keeps the preview and footer pinned around independently scrolling properties.
func (s *Settings) propertyPanel(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Column(c.Key("detail")).Label(tr(c, "壁纸属性面板")).Width(320).Shrink(0).MinHeight(0).Background(panelBg(t)).BorderWidth(0, 0, 0, 1).BorderColor(lineColor(t)).Clip().Children(func() {
		bar := c.TitleBar()
		if s.GOOS == "windows" && bar.Right > 0 && bar.Height > 0 {
			ui.Box(c.Key("caption-strip")).Height(bar.Height).Shrink(0).DragWindow().Children(func() {})
		}
		if it := s.itemByID(v, s.selID); it != nil {
			var using []string
			for _, d := range v.Displays {
				if d.ItemID == it.ID {
					using = append(using, dispLabel(c, d))
				}
			}
			s.sidePanel(c, v, *it, using)
		} else {
			ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				text(c, "选择壁纸以查看属性").FontSize(13).TextColor(t.TextMuted)
			})
		}
		s.panelFooter(c, v)
	})
}

func (s *Settings) sidePanel(c *ui.Context, v app.View, it config.Item, using []string) {
	t := c.Theme()
	ui.Box(c.Key("stage")).Label(tr(c, "壁纸预览")).Height(180).Shrink(0).Clip().Children(func() {
		s.preview(c, it, 180)
		if len(using) > 0 {
			ui.Text(c, tr(c, "正在用于：")+strings.Join(using, "、")).Absolute().Left(10).Bottom(10).FontSize(11).TextColor(ui.Hex("#ffffff")).Background(ui.RGBA(10, 12, 16, 0.65)).Padding(3, 8).SingleLine()
		}
	})
	ui.Scroll(c.Key("dscroll")).Grow(1).Shrink(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(8, 12, 12, 12).Gap(6).Children(func() {
			ui.Column(c).Gap(4).Padding(4, 2, 0, 2).Children(func() {
				heading(c, it.Title, 20).Wrap()
				meta := tr(c, originNames[it.Origin])
				if it.Author != "" {
					meta += " · " + it.Author
				}
				ui.Text(c, meta).FontSize(13).TextColor(t.TextMuted).Wrap()
			})
			ui.Row(c.Key("tags")).Wrap().Gap(6).Padding(0, 2).Children(func() {
				tag := func(label string) {
					ui.Text(c, label).FontSize(11).FontWeight(600).Padding(3, 9).Radius(6).Background(t.Surface).Border(1, lineColor(t)).TextColor(t.TextMuted)
				}
				tag(tr(c, typeNames[it.Type]))
				if it.Codec != "" {
					tag(strings.ToUpper(it.Codec))
				}
				if it.Width > 0 && it.Height > 0 {
					tag(fmt.Sprintf("%d × %d", it.Width, it.Height))
				}
				switch it.Transcode {
				case "recommended":
					chip(c, "建议转码", t.Warning).Padding(3, 9)
				case "done":
					chip(c, "已优化", t.Success).Padding(3, 9)
				}
			})
			ui.Row(c.Key("acts")).Gap(8).Padding(4, 0, 0, 0).Children(func() {
				if softButton(c.Key("props"), "屏幕属性").Grow(1).Basis(0).Clicked() {
					s.page = pgDisplays
				}
				if softButton(c.Key("reveal"), "打开位置").Grow(1).Basis(0).Disabled(s.ShowInFolder == nil).Clicked() {
					s.ShowInFolder(filepath.Join(it.Dir, it.Entry))
				}
			})
			if ghostButton(c.Key("rm"), "从壁纸库删除").AlignSelf(ui.Start).TextColor(t.Danger).Disabled(v.Importing).Clicked() {
				s.confirmRm = true
			}
			s.groupProps(c, v, it)
			s.overrideNotice(c, v)
			s.transcodeCard(c, v, it)
			s.details(c, it)
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

// group 是属性栏里抬高一层的分组卡片，行与行之间用细线隔开。
func group(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).Radius(12).Background(raisedBg(t)).Border(1, lineColor(t)).Padding(2, 12).Children(fn)
}

// propRow 是分组卡片的一行：左边名称（可带一行说明），右边控件；last 行不画分隔线。
func propRow(c *ui.Context, key, label, sub string, last bool, control func()) {
	t := c.Theme()
	r := ui.Row(c.Key(key)).Gap(6).MinHeight(32).Padding(2, 0).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(92).Shrink(0).Gap(1).Children(func() {
			text(c, label).FontSize(13).FontWeight(500).SingleLine()
			if sub != "" {
				text(c, sub).FontSize(11).TextColor(faint(t)).SingleLine().Ellipsis("…")
			}
		})
		ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Justify(ui.End).Children(control)
	})
	if !last {
		r.BorderWidth(0, 0, 1, 0).BorderColor(lineColor(t))
	}
}

// groupProps 是“属性”：声音、音量、播放速度、填充都随壁纸保存，应用到任何显示器都生效。
func (s *Settings) groupProps(c *ui.Context, v app.View, it config.Item) {
	if it.Type == "web" || it.Type == "shader" {
		s.webProps(c, v, it)
		return
	}
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
			if ghostButton(c.Key("resetprops"), "恢复默认").FontSize(11).Padding(2, 8).Clicked() {
				s.ctl.SetItemProps(it.ID, config.ItemProps{})
			}
		}
	})
	group(c.Key("group"), func() {
		if it.Type == "image" {
			propRow(c, "g-snd", "声音", "", false, func() { text(c, "图片壁纸没有声音").FontSize(12).TextColor(t.TextMuted) })
		} else {
			sub := "这张壁纸自己的设置"
			if props.Audio == "" {
				sub = "未单独设置，跟随默认静音设置"
			}
			propRow(c, "g-snd", "声音", "", false, func() {
				text(c, sub).FontSize(11).TextColor(faint(t)).SingleLine().Ellipsis("…").Shrink(1)
				on := !eff.Muted
				if ui.Switch(c.Key("sound"), &on).Label(tr(c, "播放声音 开关")).Shrink(0).Changed() {
					b := on
					setProps(func(p *config.ItemProps) {
						p.Audio = "mute"
						if b {
							p.Audio = "sound"
						}
					})
				}
			})
			propRow(c, "g-vol", "音量", "", false, func() {
				vol := float64(eff.Volume)
				if ui.Slider(c.Key("volume"), &vol, 5, 100).Label(tr(c, "音量")).Disabled(eff.Muted).Grow(1).Shrink(1).MinWidth(60).Changed() {
					n := (int(vol) + 2) / 5 * 5
					setProps(func(p *config.ItemProps) { p.Volume = max(5, n) })
				}
				textf(c, "%d%%", eff.Volume).FontSize(12).FontWeight(600).Width(38).TextAlign(ui.End)
			})
		}
		propRow(c, "g-speed", "播放速度", "", false, func() {
			if it.Type == "image" {
				text(c, "静态图片不支持速度").FontSize(12).TextColor(t.TextMuted)
				return
			}
			speed := eff.Speed
			if ui.Slider(c.Key("ispeed"), &speed, 0.25, 4).Label(tr(c, "壁纸播放速度")).Grow(1).Shrink(1).MinWidth(60).Changed() {
				sp := float64(int(speed*4+0.5)) / 4
				setProps(func(p *config.ItemProps) { p.Speed = sp })
			}
			textf(c, "%.2gx", eff.Speed).FontSize(12).FontWeight(600).Width(38).TextAlign(ui.End)
		})
		propRow(c, "g-fit", "填充", "", true, func() {
			fit := fitNames[eff.Fit]
			if selectControl(c.Key("ifit"), &fit, fitLabels).Label(tr(c, "壁纸填充方式")).Radius(8).Width(136).Changed() {
				id := fitIDs[fit]
				setProps(func(p *config.ItemProps) { p.Fit = id })
			}
		})
	})
	text(c, "随壁纸保存，应用到任何显示器都生效").FontSize(11).TextColor(faint(t)).Wrap().Padding(0, 2)
}

// transcodeCard 是转码建议（F-07）：暖色描边卡片。
func (s *Settings) transcodeCard(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	st := v.Transcoding[it.ID]
	if it.Type != "video" && it.Transcode != "recommended" && st == "" {
		return
	}
	ui.Column(c.Key("tc")).Gap(6).Padding(12).Radius(12).Background(t.Warning.Alpha(0.08)).Border(1, t.Warning.Alpha(0.28)).Children(func() {
		text(c, "为每块屏优化").FontSize(13).FontWeight(600).TextColor(t.Warning)
		busy := false
		for _, rv := range v.Renditions {
			if rv.ItemID != it.ID {
				continue
			}
			name := rv.DisplayID
			for _, d := range v.Displays {
				if d.ID == rv.DisplayID {
					name = d.Name
				}
			}
			label := name
			if rv.Width > 0 {
				label += fmt.Sprintf(" · %d × %d", rv.Width, rv.Height)
			}
			propRow(c, "rendition-"+rv.DisplayID, label, "", false, func() { text(c, rv.Status).FontSize(12).TextColor(t.TextMuted) })
			if rv.Status == "等待转码…" || rv.Status == "正在转码…" {
				busy = true
			}
		}
		if busy {
			ui.Row(c).Gap(6).Children(func() { ui.Spinner(c).Size(14, 14); text(c, "正在优化屏幕版本…").FontSize(12) })
			if softButton(c.Key("covercancel"), "取消屏幕优化").Clicked() {
				s.ctl.CancelRenditions(it.ID)
			}
		} else if softButton(c.Key("covergo"), "优化屏幕版本").Clicked() {
			s.ctl.OptimizeDisplays(it.ID)
		}
		text(c, "建议优化转码").FontSize(13).FontWeight(600).TextColor(t.Warning)
		note := mediaNote(c.Locale(), it.Note)
		if note == "" {
			note = tr(c, "这个文件的编码或规格不利于省电播放。")
		}
		// Source/backend notes remain verbatim; the application-owned
		// promise depends on the encoder's verified capabilities.
		ui.Text(c, note).FontSize(12).Wrap()
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
			text(c, "需要 ffmpeg 才能转码").FontSize(12).TextColor(t.TextMuted).Wrap()
			if softButton(c.Key("tcset"), "设置编码器…").Clicked() {
				s.page = pgPerf
			}
		default:
			if st != "" {
				text(c, localizedError(c, st)).FontSize(12).TextColor(t.Danger).Wrap()
			}
			if softButton(c.Key("tcgo"), "一键优化").Margin(4, 0, 0, 0).FillWidth().Clicked() {
				s.ctl.Transcode(it.ID)
			}
		}
	})
}

// details 是“详细信息”分组。
func (s *Settings) details(c *ui.Context, it config.Item) {
	t := c.Theme()
	sectionHeader(c, "metahdr", tr(c, "详细信息"), nil)
	var rows [][2]string
	kv := func(k, val string) { rows = append(rows, [2]string{k, val}) }
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
	group(c.Key("meta"), func() {
		for i, r := range rows {
			row := ui.Row(c).Gap(12).Padding(9, 0).Children(func() {
				text(c, r[0]).FontSize(12).TextColor(t.TextMuted).Width(64).Shrink(0)
				ui.Text(c, r[1]).FontSize(12).Grow(1).Wrap()
			})
			if i < len(rows)-1 {
				row.BorderWidth(0, 0, 1, 0).BorderColor(lineColor(t))
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
	ui.Column(c.Key("override")).Gap(6).Padding(12).Radius(12).Background(t.Accent.Alpha(0.08)).Border(1, t.Accent.Alpha(0.3)).Children(func() {
		ui.Text(c, fmt.Sprintf(tr(c, "%s 单独覆盖了：%s"), dispLabel(c, *d), strings.Join(parts, tr(c, "、")))).FontSize(12).Wrap()
		text(c, "在这块屏上以覆盖为准，其他屏使用壁纸自带的属性。").FontSize(11).TextColor(t.TextMuted).Wrap()
		if softButton(c.Key("clearov"), "清除覆盖").FontSize(12).Clicked() {
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
