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
	// Only clear selection after a refreshed snapshot confirms removal, never
	// merely because an asynchronous delete request was submitted or rejected.
	if s.selID != "" && s.itemByID(v, s.selID) == nil {
		s.selID, s.confirmRm = "", false
	}
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
			inUse[d.ItemID] = append(inUse[d.ItemID], dispLabel(c, d))
		}
	}
	drop := ui.Column(c.Key("libdrop")).Grow(1).MinHeight(0).Children(func() {
		// 筛选条：类型筛选 | 数量 | 网格/列表（搜索与导入在顶部导航条）；空库时只显示拖放区
		if len(v.Library) > 0 {
			ui.Row(c.Key("filters")).Padding(14, 20, 10, 20).Gap(6).AlignItems(ui.Center).Children(func() {
				labels := []string{"全部", "视频", "图片", "建议转码"}
				for i, l := range labels {
					n := 0
					for _, it := range v.Library {
						switch {
						case i == 0, i == 1 && it.Type == "video", i == 2 && it.Type == "image", i == 3 && it.Transcode == "recommended":
							n++
						}
					}
					b := button(c.Key("f"+itoa(i)), fmt.Sprintf("%s %d", tr(c, l), n)).Radius(999).Padding(3, 12).FontSize(12)
					if s.filter == i {
						b.Background(t.Accent.Alpha(0.14)).TextColor(t.Accent).Border(1, t.Accent.Alpha(0.4))
					}
					if b.Clicked() {
						s.filter = i
					}
				}
				ui.Box(c).Grow(1).Children(func() {})
				textf(c, "共 %d 张壁纸 · 拖入文件即可导入", len(v.Library)).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).Ellipsis("…")
				view := 0
				if v.Config.UI.LibraryView == "list" {
					view = 1
				}
				if segmented(c.Key("view"), &view, "网格", "列表").Label(tr(c, "视图")).Changed() {
					lv := "grid"
					if view == 1 {
						lv = "list"
					}
					s.ctl.UpdateConfig(func(x *config.Config) { x.UI.LibraryView = lv })
				}
			})
		}
		if v.Importing || v.LastImport != "" {
			ui.Row(c.Key("importStatus")).Padding(0, 20, 10, 20).Gap(6).AlignItems(ui.Center).Children(func() {
				if v.Importing {
					ui.Spinner(c).Size(14, 14)
				}
				text(c, v.LastImport).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1)
				if v.ImportQueued > 0 {
					textf(c, "队列中还有 %d 个请求", v.ImportQueued).FontSize(12).TextColor(t.TextMuted)
				}
			})
		}

		if len(v.Library) == 0 {
			s.emptyLibrary(c, v)
			return
		}
		ui.Row(c.Key("content")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(0, 0, 0, 14).Children(func() {
			ui.Column(c.Key("gridcol")).Grow(1).Shrink(1).MinWidth(0).MinHeight(0).Children(func() {
				if len(items) == 0 {
					ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
						ui.Icon(c, icSearch).FontSize(28).TextColor(t.TextMuted)
						text(c, "没有符合条件的壁纸").TextColor(t.TextMuted)
					})
					return
				}
				if v.Config.UI.LibraryView == "list" {
					s.libList(c, v, items, inUse)
				} else {
					s.libGrid(c, v, items, inUse)
				}
			})
			// 检查器只在选中时出现，未选中时网格占满宽度（内容居中）。
			if s.gridSel >= 0 {
				s.detail(c, v, items[s.gridSel], inUse[items[s.gridSel].ID])
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
	ui.Column(c.Key("empty")).Grow(1).Margin(20).Radius(16).Border(2, t.Border).BorderStyle(ui.BorderDashed).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
		ui.Box(c).Size(64, 64).Radius(16).Gradient(t.Accent, ui.Hex("#22d3ee"), 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, icImport).FontSize(30).TextColor(ui.Hex("#ffffff"))
		})
		text(c, "把视频、GIF 或图片拖到这里").Bold().FontSize(17)
		text(c, "支持 mp4 / mov / m4v / webm / gif / png / jpg / webp，以及 .wpk、Lively 壁纸包和 Wallpaper Engine 视频壁纸").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Padding(8, 0, 0, 0).Children(func() {
			if iconButton(c.Key("imp2"), icPlus, "选择文件…", true).Disabled(s.PickFiles == nil).Clicked() {
				s.importFiles()
			}
			if iconButton(c.Key("folder2"), icFolder, "导入文件夹…", false).Disabled(s.PickFolder == nil).Clicked() {
				s.importFolder()
			}
		})
	})
}

func (s *Settings) libGrid(c *ui.Context, v app.View, items []config.Item, inUse map[string][]string) {
	t := c.Theme()
	screens := map[string][]string{}
	for _, d := range v.Displays {
		if d.ItemID != "" {
			screens[d.ItemID] = append(screens[d.ItemID], itoa(d.Index))
		}
	}
	s.grid.Key = func(i int) any { return items[i].ID }
	s.grid.Label = func(i int) string { return items[i].Title }
	// 卡片就是一组壁纸的封面：画面铺满，标题叠在底部渐变上（Wallpaper Engine 式）。
	g := ui.GridView(c.Key("grid"), &s.grid, len(items), 210, 136, func(i int) {
		it := items[i]
		sel := i == s.gridSel
		card := ui.Box(c.Key("card"+it.ID)).Grow(1).Margin(5).Radius(10).Clip().Background(t.Surface).Border(1, t.Border).Children(func() {
			s.preview(c, it, 126)
			ui.Row(c).Absolute().Top(7).Left(7).Gap(4).Children(func() {
				chip(c, typeNames[it.Type], ui.Hex("#ffffff")).Background(ui.RGBA(0, 0, 0, 0.55))
				if it.Transcode == "recommended" {
					chip(c, "建议转码", ui.Hex("#fbbf24")).Background(ui.RGBA(0, 0, 0, 0.55))
				}
			})
			if n := screens[it.ID]; len(n) > 0 {
				ui.Row(c).Absolute().Top(7).Right(7).Gap(4).AlignItems(ui.Center).Padding(1, 7).Radius(999).Background(t.Accent).Children(func() {
					ui.Icon(c, icMonitor).FontSize(11).TextColor(ui.Hex("#ffffff"))
					ui.Text(c, strings.Join(n, " ")).FontSize(11).TextColor(ui.Hex("#ffffff"))
				}).Label(tr(c, "使用中"))
			}
			ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(18, 10, 7, 10).LinearGradient(ui.LinearGradient{From: ui.RGBA(0, 0, 0, 0), To: ui.RGBA(0, 0, 0, 0.72), Angle: 180}).Children(func() {
				ui.Text(c, it.Title).Bold().FontSize(13).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
				meta := tr(c, originNames[it.Origin])
				if it.Codec != "" {
					meta = strings.ToUpper(it.Codec) + " · " + meta
				}
				text(c, meta).FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.75)).SingleLine().Ellipsis("…")
			})
		})
		if sel {
			card.Border(3, t.Accent).Shadow(0, 4, 14, 0, t.Accent.Alpha(0.30))
		} else if card.Hovered() {
			card.Border(1, t.Accent.Alpha(0.6)).Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.18))
		}
	}).Grow(1).MinHeight(0).Padding(0, 0, 16, 0)
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

// detail 是检查器：把一张壁纸当作一“组”展示——画面、声音、播放属性和应用目标放在一起。
func (s *Settings) detail(c *ui.Context, v app.View, it config.Item, using []string) {
	t := c.Theme()
	ui.Column(c.Key("detail")).Width(340).Shrink(0).MinHeight(0).Margin(0, 14, 14, 6).Radius(12).Background(cardBg(t)).Border(1, t.Border).Clip().Children(func() {
		ui.Box(c).Height(190).Shrink(0).Children(func() {
			s.preview(c, it, 190)
			ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(28, 16, 12, 16).LinearGradient(ui.LinearGradient{From: ui.RGBA(0, 0, 0, 0), To: ui.RGBA(0, 0, 0, 0.75), Angle: 180}).Children(func() {
				ui.Text(c, it.Title).Bold().FontSize(17).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
				if it.Author != "" {
					ui.Text(c, it.Author).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.8)).SingleLine().Ellipsis("…")
				}
			})
		})
		ui.Scroll(c.Key("dscroll")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(12, 16, 16, 16).Gap(12).Children(func() {
				ui.Row(c).Gap(6).Wrap().Children(func() {
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
					text(c, "正在用于："+strings.Join(using, "、")).FontSize(12).TextColor(t.Accent).Wrap()
				}
				s.applyBox(c, v, it)
				s.groupContents(c, v, it)
				// 转码建议（F-07）
				st := v.Transcoding[it.ID]
				if it.Transcode == "recommended" || st != "" {
					ui.Column(c.Key("tc")).Gap(6).Padding(10).Radius(8).Background(t.Warning.Alpha(0.10)).Border(1, t.Warning.Alpha(0.3)).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, icWand).FontSize(14).TextColor(t.Warning)
							text(c, "建议优化转码").Bold().FontSize(13)
						})
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
							if button(c.Key("tcset"), "设置编码器…").Clicked() {
								s.page = pgPerf
							}
						default:
							if st != "" {
								text(c, st).FontSize(12).TextColor(t.Danger).Wrap()
							}
							if iconButton(c.Key("tcgo"), icWand, "一键优化", false).Clicked() {
								s.ctl.Transcode(it.ID)
							}
						}
					})
				}
				sectionTitle(c, "详细信息")
				ui.Column(c.Key("meta")).Gap(0).Children(func() {
					kv := func(k, val string) {
						ui.Row(c).Gap(8).Padding(6, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
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
				ui.Row(c.Key("acts")).Gap(6).Children(func() {
					if iconButton(c.Key("props"), icSliders, "屏幕属性", false).Grow(1).Clicked() {
						s.page = pgDisplays
					}
					if iconButton(c.Key("reveal"), icFolder, "打开位置", false).Grow(1).Disabled(s.ShowInFolder == nil).Clicked() {
						s.ShowInFolder(filepath.Join(it.Dir, it.Entry))
					}
				})
				if iconButton(c.Key("rm"), icTrash, "从壁纸库删除", false).FillWidth().TextColor(t.Danger).Disabled(v.Importing).Clicked() {
					s.confirmRm = true
				}
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

// sectionTitle 是检查器里的小节标题。
func sectionTitle(c *ui.Context, label string) {
	text(c, label).Bold().FontSize(12).TextColor(c.Theme().TextMuted).Padding(4, 0, 0, 0)
}

var (
	fitNames  = map[string]string{"fill": "填充", "fit": "适应", "stretch": "拉伸"}
	fitIDs    = map[string]string{"填充": "fill", "适应": "fit", "拉伸": "stretch"}
	fitLabels = []string{"填充", "适应", "拉伸"}
)

// groupContents 列出这组壁纸包含什么：画面、声音（开关 + 音量）、播放属性。
// 声音和播放属性随壁纸保存；目标显示器若覆盖了其中某项，在下方提示并可一键清除覆盖。
func (s *Settings) groupContents(c *ui.Context, v app.View, it config.Item) {
	t := c.Theme()
	props := it.Props
	setProps := func(fn func(p *config.ItemProps)) {
		p := props
		fn(&p)
		s.ctl.SetItemProps(it.ID, p)
	}
	ui.Row(c.Key("grouphdr")).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1).Children(func() { sectionTitle(c, "壁纸组内容") })
		if props != (config.ItemProps{}) {
			if button(c.Key("resetprops"), "恢复默认").FontSize(11).Padding(1, 8).Clicked() {
				s.ctl.SetItemProps(it.ID, config.ItemProps{})
			}
		}
	})
	eff := config.ResolvePlayback(props, config.DisplayOptions{}, v.Config.Render.Muted)
	ui.Column(c.Key("group")).Radius(10).Border(1, t.Border).Children(func() {
		part := func(key string, ic *ui.SVG, title, desc string, last bool, control func(), extra func()) {
			ui.Column(c.Key(key)).Gap(8).Padding(10, 12).BorderWidth(0, 0, b2w(!last), 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(28, 28).Shrink(0).Radius(7).Background(t.Accent.Alpha(0.12)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
						ui.Icon(c, ic).FontSize(14).TextColor(t.Accent)
					})
					ui.Column(c).Grow(1).Shrink(1).Children(func() {
						text(c, title).FontSize(13)
						if desc != "" {
							ui.Text(c, desc).FontSize(11).TextColor(t.TextMuted).Wrap()
						}
					})
					if control != nil {
						control()
					}
				})
				if extra != nil {
					extra()
				}
			})
		}
		// 画面
		ic, spec := icVideo, []string{tr(c, typeNames[it.Type])}
		if it.Type == "image" {
			ic = icImage
		}
		if it.Codec != "" {
			spec = append(spec, strings.ToUpper(it.Codec))
		}
		if it.Width > 0 && it.Height > 0 {
			spec = append(spec, fmt.Sprintf("%d × %d", it.Width, it.Height))
		}
		if it.DurationSec > 0 {
			sec := int(it.DurationSec + 0.5)
			spec = append(spec, fmt.Sprintf("%d:%02d", sec/60, sec%60))
		}
		part("g-pic", ic, "画面", strings.Join(spec, " · "), false, nil, nil)
		// 声音：开关 + 音量，随壁纸保存；未设置时跟随“播放与性能”里的默认静音
		if it.Type == "image" {
			part("g-snd", icSound, "声音", tr(c, "图片壁纸没有声音"), false, nil, nil)
		} else {
			desc := tr(c, "这张壁纸自己的设置")
			if props.Audio == "" {
				desc = tr(c, "未单独设置，跟随默认静音设置")
			}
			on := !eff.Muted
			part("g-snd", icSound, "声音", desc, false, func() {
				if ui.Switch(c.Key("sound"), &on).Label(tr(c, "播放声音 开关")).Changed() {
					b := on
					setProps(func(p *config.ItemProps) {
						p.Audio = "mute"
						if b {
							p.Audio = "sound"
						}
					})
				}
			}, func() {
				if eff.Muted {
					return
				}
				ui.Row(c.Key("vol")).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 38).Children(func() {
					text(c, "音量").FontSize(12).TextColor(t.TextMuted).Width(36)
					vol := float64(eff.Volume)
					if ui.StepSlider(c.Key("volume"), &vol, 5, 100, 5).Label(tr(c, "音量")).Grow(1).Changed() {
						n := int(vol)
						setProps(func(p *config.ItemProps) { p.Volume = n })
					}
					textf(c, "%d%%", eff.Volume).FontSize(12).Width(36)
				})
			})
		}
		// 播放属性
		part("g-props", icSliders, "播放属性", tr(c, "随壁纸保存，应用到任何显示器都生效"), true, nil, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 38).Children(func() {
				text(c, "填充").FontSize(12).TextColor(t.TextMuted).Width(36)
				fit := fitNames[eff.Fit]
				if selectControl(c.Key("ifit"), &fit, fitLabels).Label(tr(c, "壁纸填充方式")).Grow(1).Changed() {
					id := fitIDs[fit]
					setProps(func(p *config.ItemProps) { p.Fit = id })
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 38).Children(func() {
				text(c, "速度").FontSize(12).TextColor(t.TextMuted).Width(36)
				speed := eff.Speed
				if it.Type == "image" {
					text(c, "静态图片不支持速度").FontSize(12).TextColor(t.TextMuted)
					return
				}
				if ui.NumberInput(c.Key("ispeed"), &speed, 0.25, 4, 0.25).Label(tr(c, "壁纸播放速度")).Grow(1).Changed() {
					sp := speed
					setProps(func(p *config.ItemProps) { p.Speed = sp })
				}
				text(c, "倍速").FontSize(12).TextColor(t.TextMuted)
			})
		})
	})
	s.overrideNotice(c, v)
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

func b2w(b bool) float32 {
	if b {
		return 1
	}
	return 0
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
	text(c, "应用到").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c.Key("applyrow")).Gap(8).AlignItems(ui.Center).Children(func() {
		selectControl(c.Key("applyto"), &s.applySel, opts).Label(tr(c, "应用到显示器")).Grow(1).Shrink(1)
		if iconButton(c.Key("apply"), icCheck, "应用", true).Clicked() {
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
