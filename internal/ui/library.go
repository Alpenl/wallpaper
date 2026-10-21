package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

var typeNames = map[string]string{"video": "视频", "image": "图片", "web": "网页", "shader": "Shader"}
var originNames = map[string]string{"native": "本应用壁纸包", "lively": "Lively 导入", "we": "Wallpaper Engine 导入", "file": "本地文件"}

func (s *Settings) importFiles() {
	if s.PickFiles == nil {
		return
	}
	for _, p := range s.PickFiles() {
		s.importPath(p)
	}
}

func (s *Settings) library(c *ui.Context, v app.View) {
	t := c.Theme()
	items := s.filtered(v)
	// 选中项跟随 id（筛选、排序变化后仍选中同一张）
	s.gridSel = -1
	for i, it := range items {
		if it.ID == s.selID {
			s.gridSel = i
		}
	}
	inUse := map[string][]string{}
	for _, d := range v.Displays {
		if d.ItemID != "" {
			inUse[d.ItemID] = append(inUse[d.ItemID], dispLabel(d))
		}
	}
	drop := ui.Column(c.Key("libdrop")).Grow(1).MinHeight(0).Children(func() {
		// 标题栏：搜索、筛选、视图、导入
		ui.Row(c.Key("hdr")).Padding(22, 28, 10, 28).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, "壁纸库").Bold().FontSize(22)
				ui.Textf(c, "共 %d 张壁纸 · 拖入文件即可导入", len(v.Library)).FontSize(13).TextColor(t.TextMuted)
			})
			ui.TextInput(c.Key("q"), &s.query).Placeholder("搜索名称或来源").Label("搜索壁纸").Width(200)
			view := 0
			if v.Config.UI.LibraryView == "list" {
				view = 1
			}
			if ui.Segmented(c.Key("view"), &view, "网格", "列表").Label("视图").Changed() {
				lv := "grid"
				if view == 1 {
					lv = "list"
				}
				s.ctl.UpdateConfig(func(x *config.Config) { x.UI.LibraryView = lv })
			}
			ui.Button(c.Key("more"), "更多导入").Menu(func(m *ui.Menu) {
				if m.Item("导入 Lively 壁纸包（.zip）…").Disabled(s.PickFiles == nil).Chosen() {
					s.importFiles()
				}
				if m.Item("导入 Wallpaper Engine 壁纸文件夹…").Chosen() {
					if s.PickFolder != nil {
						if d := s.PickFolder(); d != "" {
							s.importPath(d)
						}
					} else {
						s.wip(c, "选择文件夹导入")
					}
				}
				m.Separator()
				if m.Item("新建网页壁纸（v1）").Chosen() {
					s.wip(c, "网页壁纸")
				}
				if m.Item("新建 Shader 壁纸（v1）").Chosen() {
					s.wip(c, "Shader 壁纸")
				}
				if m.Item("从创意工坊下载（v1）").Chosen() {
					s.wip(c, "创意工坊")
				}
			})
			if iconButton(c.Key("imp"), icPlus, "导入壁纸", true).Disabled(s.PickFiles == nil || v.Importing).Clicked() {
				s.importFiles()
			}
		})
		ui.Row(c.Key("filters")).Padding(0, 28, 12, 28).Gap(8).AlignItems(ui.Center).Children(func() {
			labels := []string{"全部", "视频", "图片", "建议转码"}
			for i, l := range labels {
				n := 0
				for _, it := range v.Library {
					switch {
					case i == 0, i == 1 && it.Type == "video", i == 2 && it.Type == "image", i == 3 && it.Transcode == "recommended":
						n++
					}
				}
				b := ui.Button(c.Key("f"+itoa(i)), fmt.Sprintf("%s %d", l, n)).Radius(999).Padding(3, 12).FontSize(12)
				if s.filter == i {
					b.Background(t.Accent.Alpha(0.14)).TextColor(t.Accent).Border(1, t.Accent.Alpha(0.4))
				}
				if b.Clicked() {
					s.filter = i
				}
			}
			ui.Spacer(c)
			if v.Importing || v.LastImport != "" {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if v.Importing {
						ui.Spinner(c).Size(14, 14)
					}
					ui.Text(c, v.LastImport).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MaxWidth(420)
				})
			}
		})
		if len(v.Library) == 0 {
			s.emptyLibrary(c, v)
			return
		}
		ui.Row(c.Key("content")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(0, 0, 0, 28).Gap(0).Children(func() {
			ui.Column(c.Key("gridcol")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
				if len(items) == 0 {
					ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
						ui.Icon(c, icSearch).FontSize(28).TextColor(t.TextMuted)
						ui.Text(c, "没有符合条件的壁纸").TextColor(t.TextMuted)
					})
					return
				}
				if v.Config.UI.LibraryView == "list" {
					s.libList(c, v, items, inUse)
				} else {
					s.libGrid(c, v, items, inUse)
				}
			})
			if s.gridSel >= 0 {
				s.detail(c, v, items[s.gridSel], inUse[items[s.gridSel].ID])
			} else {
				ui.Column(c.Key("nodetail")).Width(300).Margin(0, 28, 28, 16).Padding(20).Radius(12).Background(cardBg(t)).Border(1, t.Border).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, icImage).FontSize(28).TextColor(t.TextMuted)
					ui.Text(c, "选择一张壁纸查看详情").TextColor(t.TextMuted)
					ui.Text(c, "双击可应用到当前显示器").FontSize(12).TextColor(t.TextMuted)
				})
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

func (s *Settings) emptyLibrary(c *ui.Context, v app.View) {
	t := c.Theme()
	ui.Column(c.Key("empty")).Grow(1).Margin(0, 28, 28, 28).Radius(16).Border(2, t.Border).BorderStyle(ui.BorderDashed).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
		ui.Box(c).Size(64, 64).Radius(16).Gradient(t.Accent, ui.Hex("#22d3ee"), 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, icImport).FontSize(30).TextColor(ui.Hex("#ffffff"))
		})
		ui.Text(c, "把视频、GIF 或图片拖到这里").Bold().FontSize(17)
		ui.Text(c, "支持 mp4 / mov / m4v / webm / gif / png / jpg / webp，以及 .wpk、Lively 壁纸包和 Wallpaper Engine 视频壁纸").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Padding(8, 0, 0, 0).Children(func() {
			if iconButton(c.Key("imp2"), icPlus, "选择文件…", true).Disabled(s.PickFiles == nil).Clicked() {
				s.importFiles()
			}
			if iconButton(c.Key("disc2"), icDiscover, "浏览创意工坊", false).Clicked() {
				s.wip(c, "创意工坊")
			}
		})
	})
}

func (s *Settings) libGrid(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	s.grid.Key = func(i int) any { return items[i].ID }
	s.grid.Label = func(i int) string { return items[i].Title }
	g := ui.GridView(c.Key("grid"), &s.grid, len(items), 200, 188, func(i int) {
		it := items[i]
		sel := i == s.gridSel
		card := ui.Column(c.Key("card"+it.ID)).Grow(1).Margin(6).Radius(12).Background(cardBg(t)).Border(1, t.Border).Clip().Children(func() {
			ui.Box(c).Height(110).Children(func() {
				s.preview(c, it, 110)
				ui.Row(c).Absolute().Top(8).Left(8).Gap(4).Children(func() {
					chip(c, typeNames[it.Type], ui.Hex("#ffffff")).Background(ui.RGBA(0, 0, 0, 0.55))
					if it.Transcode == "recommended" {
						chip(c, "建议转码", ui.Hex("#fbbf24")).Background(ui.RGBA(0, 0, 0, 0.55))
					}
				})
				if len(inUse[it.ID]) > 0 {
					ui.Row(c).Absolute().Top(8).Right(8).Children(func() {
						chip(c, "使用中", ui.Hex("#ffffff")).Background(t.Accent)
					})
				}
			})
			ui.Column(c).Padding(8, 10).Gap(2).Children(func() {
				ui.Text(c, it.Title).Bold().FontSize(13).SingleLine().Ellipsis("…")
				meta := originNames[it.Origin]
				if it.Codec != "" {
					meta = strings.ToUpper(it.Codec) + " · " + meta
				}
				ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
		})
		if sel {
			card.Border(2, t.Accent).Shadow(0, 4, 14, 0, t.Accent.Alpha(0.25))
		} else if card.Hovered() {
			card.Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.10))
		}
	}).Grow(1).MinHeight(0).Padding(0, 0, 20, 0)
	if g.Changed() && s.gridSel >= 0 && s.gridSel < len(items) {
		s.selID = items[s.gridSel].ID
		s.confirmRm = false
	}
	if g.Submitted() && s.gridSel >= 0 && s.gridSel < len(items) {
		s.applyCurrent(c, v, items[s.gridSel].ID)
	}
}

// preview 画缩略图；没有首帧截图时画渐变占位。
func (s *Settings) preview(c *ui.Context, it config.Item, h float32) {
	if bm := s.thumbOf(it); bm != nil {
		ui.Image(c, bm).Fit(ui.Cover).Height(h).FillWidth()
		return
	}
	h1 := 0
	for _, r := range it.ID {
		h1 = h1*31 + int(r)
	}
	pal := [][2]string{{"#6366f1", "#22d3ee"}, {"#f43f5e", "#f59e0b"}, {"#10b981", "#3b82f6"}, {"#8b5cf6", "#ec4899"}, {"#0ea5e9", "#14b8a6"}}
	p := pal[(h1%len(pal)+len(pal))%len(pal)]
	ic := icVideo
	if it.Type == "image" {
		ic = icImage
	}
	ui.Box(c).Height(h).FillWidth().Gradient(ui.Hex(p[0]), ui.Hex(p[1]), 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, ic).FontSize(26).TextColor(ui.RGBA(255, 255, 255, 0.85))
	})
}

func (s *Settings) libList(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	ui.Scroll(c.Key("listscroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(6).Padding(0, 6, 20, 6).Children(func() {
			for i, it := range items {
				it, i := it, i
				row := ui.Row(c.Key("row"+it.ID)).Gap(12).Padding(8).Radius(10).AlignItems(ui.Center).Background(cardBg(t)).Border(1, t.Border).Children(func() {
					ui.Box(c).Width(96).Radius(6).Clip().Children(func() { s.preview(c, it, 54) })
					ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
						ui.Text(c, it.Title).Bold().FontSize(13).SingleLine().Ellipsis("…")
						meta := typeNames[it.Type] + " · " + originNames[it.Origin]
						if it.Codec != "" {
							meta += " · " + strings.ToUpper(it.Codec)
						}
						ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
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

func (s *Settings) detail(c *ui.Context, v app.View, it config.Item, using []string) {
	t := c.Theme()
	ui.Column(c.Key("detail")).Width(300).MinHeight(0).Margin(0, 28, 28, 16).Radius(12).Background(cardBg(t)).Border(1, t.Border).Clip().Children(func() {
		ui.Box(c).Height(168).Children(func() { s.preview(c, it, 168) })
		ui.Scroll(c.Key("dscroll")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(14, 16).Gap(10).Children(func() {
				ui.Text(c, it.Title).Bold().FontSize(16).Wrap()
				ui.Row(c).Gap(6).Children(func() {
					chip(c, typeNames[it.Type], t.Accent)
					if it.Codec != "" {
						chip(c, strings.ToUpper(it.Codec), t.TextMuted)
					}
					switch it.Transcode {
					case "recommended":
						chip(c, "建议转码", t.Warning)
					case "done":
						chip(c, "已优化", t.Success)
					}
				})
				if len(using) > 0 {
					ui.Text(c, "正在用于："+strings.Join(using, "、")).FontSize(12).TextColor(t.Accent)
				}
				s.applyBox(c, v, it)
				// 转码建议（F-07）
				st := v.Transcoding[it.ID]
				if it.Transcode == "recommended" || st != "" {
					ui.Column(c.Key("tc")).Gap(6).Padding(10).Radius(8).Background(t.Warning.Alpha(0.10)).Border(1, t.Warning.Alpha(0.3)).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, icWand).FontSize(14).TextColor(t.Warning)
							ui.Text(c, "建议优化转码").Bold().FontSize(13)
						})
						note := it.Note
						if note == "" {
							note = "这个文件的编码或规格不利于省电播放。"
						}
						ui.Text(c, note+" 转码为 30 fps、去音轨、每秒一个关键帧，播放更省电、循环更顺滑。原文件保留。").FontSize(12).TextColor(t.TextMuted).Wrap()
						switch {
						case st == "正在转码…":
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Spinner(c).Size(14, 14)
								ui.Text(c, "正在转码，可以继续使用其他功能…").FontSize(12)
							})
						case !v.HasEncoder:
							ui.Text(c, s.noEncoderHint()).FontSize(12).TextColor(t.Warning).Wrap()
							if ui.Button(c.Key("tcset"), "设置编码器…").Clicked() {
								s.page = pgPerf
							}
						default:
							if st != "" {
								ui.Text(c, st).FontSize(12).TextColor(t.Danger).Wrap()
							}
							if iconButton(c.Key("tcgo"), icWand, "一键优化", false).Clicked() {
								s.ctl.Transcode(it.ID)
							}
						}
					})
				}
				ui.Column(c.Key("meta")).Gap(0).Children(func() {
					kv := func(k, val string) {
						ui.Row(c).Gap(8).Padding(6, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
							ui.Text(c, k).FontSize(12).TextColor(t.TextMuted).Width(64)
							ui.Text(c, val).FontSize(12).Grow(1).Wrap()
						})
					}
					kv("来源", originNames[it.Origin])
					kv("导入时间", it.AddedAt.Local().Format("2006-01-02 15:04"))
					kv("入口文件", it.Entry)
					if it.Note != "" && it.Transcode != "recommended" {
						kv("备注", it.Note)
					}
				})
				ui.Row(c.Key("acts")).Gap(6).Children(func() {
					if iconButton(c.Key("props"), icSliders, "属性", false).Grow(1).Clicked() {
						s.wip(c, "壁纸属性面板（速度、填充方式）")
					}
					if iconButton(c.Key("reveal"), icFolder, "打开位置", false).Grow(1).Disabled(s.ShowInFolder == nil).Clicked() {
						s.ShowInFolder(it.Dir)
					}
				})
				if iconButton(c.Key("rm"), icTrash, "从壁纸库删除", false).FillWidth().TextColor(t.Danger).Clicked() {
					s.confirmRm = true
				}
			})
		})
	})
	if ui.AlertDialog(c.Key("rmdlg"), &s.confirmRm, "删除「"+it.Title+"」？", "会同时删除导入时复制的文件和缩略图，使用它的显示器会恢复系统壁纸。此操作不能撤销。", "取消", "删除") == 1 {
		s.ctl.Remove(it.ID)
		s.selID = ""
		c.Toast("已删除「" + it.Title + "」")
	}
}

func (s *Settings) noEncoderHint() string {
	if s.GOOS == "darwin" {
		return "没有找到系统自带的 /usr/bin/avconvert。"
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
	c.Toast("已应用到 " + dispLabel(*d))
}

const applyAllLabel = "所有显示器"

// applyBox 是详情面板的“应用到”：下拉选择 当前显示器 / 某一块 / 所有显示器，再点“应用”。
func (s *Settings) applyBox(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	if len(v.Displays) == 0 {
		ui.Text(c, "没有检测到显示器").FontSize(12).TextColor(t.TextMuted)
		return
	}
	opts := make([]string, 0, len(v.Displays)+1)
	ids := map[string]string{}
	for _, d := range v.Displays {
		l := dispLabel(d)
		if d.Primary {
			l += "（主屏）"
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
	ui.Text(c, "应用到").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c.Key("applyrow")).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Select(c.Key("applyto"), &s.applySel, opts).Label("应用到显示器").Grow(1).Shrink(1)
		if iconButton(c.Key("apply"), icCheck, "应用", true).Clicked() {
			if s.applySel == applyAllLabel {
				s.ctl.AssignAll(it.ID)
				c.Toast("已应用到所有显示器")
			} else if id := ids[s.applySel]; id != "" {
				s.ctl.Assign(id, it.ID)
				s.dispSel = id
				c.Toast("已应用到 " + strings.TrimSuffix(s.applySel, "（主屏）"))
			}
		}
	})
}
