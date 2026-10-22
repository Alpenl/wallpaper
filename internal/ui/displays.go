package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

var levelNames = map[string]string{"L0": "播放中", "L1": "已暂停", "L2": "已暂停（冻结）", "L3": "已暂停（释放解码器）", "L4": "已停止（显示截图）", "-": "未运行"}

var reasonNames = map[string]string{
	"Fullscreen": "其他程序全屏", "Covered": "被窗口遮挡", "Locked": "已锁屏", "Remote": "远程桌面", "Battery": "使用电池",
	"BatterySaver": "省电模式", "ThermalSerious": "设备过热（限帧）", "ThermalCritical": "设备严重过热", "DisplayOff": "显示器关闭",
	"Suspend": "睡眠", "User": "手动暂停", "LowBattery": "电量过低", "FocusedApp": "指定应用运行中",
}

// ReasonsText 把 07 章的原因名（| 分隔）翻成中文。
func ReasonsText(r string) string {
	if r == "" {
		return ""
	}
	var out []string
	for _, x := range strings.Split(r, "|") {
		if n, ok := reasonNames[x]; ok {
			out = append(out, n)
		} else {
			out = append(out, x)
		}
	}
	return strings.Join(out, "、")
}

func (s *Settings) itemByID(v app.View, id string) *config.Item {
	for i := range v.Library {
		if v.Library[i].ID == id {
			return &v.Library[i]
		}
	}
	return nil
}

func (s *Settings) displays(c *ui.Context, v app.View) {
	t := c.Theme()
	if d := s.curDisplay(v); d != nil {
		s.dispSel = d.ID
	}
	page(c, "显示器", "每块屏可以单独设置壁纸；暂停和省电按屏判定", nil, func() {
		if len(v.Displays) == 0 {
			card(c, "", "", func() {
				text(c, "没有检测到显示器。").TextColor(t.TextMuted).Padding(12, 0)
			})
			return
		}
		// 布局示意图（按虚拟屏坐标等比缩放，点击选择）
		card(c.Key("layout"), "布局", "按系统里的实际排列显示；点击一块屏查看和修改它的设置", func() {
			minX, minY, maxX, maxY := v.Displays[0].X, v.Displays[0].Y, v.Displays[0].X+v.Displays[0].W, v.Displays[0].Y+v.Displays[0].H
			for _, d := range v.Displays {
				minX, minY = min(minX, d.X), min(minY, d.Y)
				maxX, maxY = max(maxX, d.X+d.W), max(maxY, d.Y+d.H)
			}
			windowW, _ := c.Size()
			const boxH = 200.0
			boxW := min(float32(640), max(float32(280), windowW-120))
			sc := min(boxW/float32(max(1, maxX-minX)), boxH/float32(max(1, maxY-minY)))
			offX := (boxW - float32(maxX-minX)*sc) / 2
			ui.Box(c.Key("canvas")).Height(boxH+24).Width(boxW).AlignSelf(ui.Center).Padding(12, 0).Children(func() {
				for _, d := range v.Displays {
					d := d
					x := offX + float32(d.X-minX)*sc
					y := 12 + float32(d.Y-minY)*sc
					w, h := float32(d.W)*sc-6, float32(d.H)*sc-6
					sel := d.ID == s.dispSel
					mon := ui.Box(c.Key("mon"+d.ID)).Absolute().Left(x).Top(y).Size(w, h).Radius(8).Clip().Border(2, t.Border).Cursor(ui.CursorPointer).Children(func() {
						if it := s.itemByID(v, d.ItemID); it != nil {
							s.preview(c, *it, h)
						} else {
							ui.Box(c).Height(h).FillWidth().Background(t.Surface).Children(func() {})
						}
						ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Padding(4, 8).Background(ui.RGBA(0, 0, 0, 0.55)).Children(func() {
							name := dispLabel(c, d)
							if d.Primary {
								name += tr(c, " · 主屏")
							}
							text(c, name).FontSize(11).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
						})
					})
					if sel {
						mon.Border(3, t.Accent)
					}
					if mon.Clicked() && s.dispSel != d.ID {
						s.dispSel = d.ID
						s.renameID = "" // 切换屏时放弃未保存的重命名，避免把名字存到另一块屏上
					}
				}
			})
		})
		var d *app.DisplayView
		for i := range v.Displays {
			if v.Displays[i].ID == s.dispSel {
				d = &v.Displays[i]
			}
		}
		if d == nil {
			d = &v.Displays[0]
			s.dispSel = d.ID
		}
		s.displayCard(c, v, *d)
		if v.Layout == "span" {
			card(c.Key("span-wallpaper"), "跨屏壁纸", "整个虚拟桌面共享同一张壁纸、填充方式和播放速度；暂停与性能档位仍按屏生效", func() {
				s.itemSelect(c.Key("spansel"), v, "", v.Default)
			})
		} else {
			card(c.Key("default"), "默认壁纸", "没有单独设置壁纸的显示器（包括以后新接入的）使用它；已单独设置的屏不受影响", func() {
				settingRow(c, "默认壁纸", "要让所有屏都换成同一张，请在壁纸详情里选“应用到 → 所有显示器”", func() {
					s.itemSelect(c.Key("defsel"), v, "", v.Default)
				})
			})
		}
		card(c.Key("advanced"), "多显示器高级选项", "", func() {
			settingRow(c, "布局方式", "跨屏模式按真实坐标裁切同一画布；保留独立模式设置，可随时切回", func() {
				independent, span := button, button
				if v.Layout == "span" {
					span = primaryButton
				} else {
					independent = primaryButton
				}
				if independent(c.Key("independent"), "独立显示器").Clicked() {
					s.ctl.SetLayout("independent", "")
				}
				if span(c.Key("span"), "跨屏拼接").Disabled(!v.Capabilities.Spanning).Clicked() {
					s.ctl.SetLayout("span", d.ItemID)
				}
			})
			if !v.Capabilities.Spanning {
				text(c, "当前桌面宿主不支持跨屏画布；复制壁纸仍可使用。").FontSize(12).TextColor(t.Warning).Wrap()
			}
			if v.LayoutNote != "" {
				text(c, v.LayoutNote).FontSize(12).TextColor(t.Warning).Wrap()
			}
			if v.Layout == "span" {
				text(c, "各屏保留独立解码与暂停，不保证视频逐帧同步。混合缩放按系统桌面坐标拼接。").FontSize(12).TextColor(t.TextMuted).Wrap()
			} else {
				settingRow(c, "复制到所有显示器", fmt.Sprintf(tr(c, "把「%s」的壁纸设为默认，并清除所有屏的单独设置"), displayName(c, *d)), func() {
					if button(c.Key("dup"), "复制").Disabled(d.ItemID == "").Clicked() {
						s.ctl.AssignAll(d.ItemID)
						c.Toast(tr(c, "已复制到所有显示器"))
					}
				})
			}
		})
	})
}

// itemSelect 是壁纸下拉框。display 为空表示默认壁纸。
func (s *Settings) itemSelect(c *ui.Context, v app.View, display, cur string) {
	lib := append([]config.Item(nil), v.Library...)
	sort.Slice(lib, func(i, j int) bool { return lib[i].AddedAt.Before(lib[j].AddedAt) })
	none := tr(c, "（不设置，显示系统壁纸）")
	if display != "" && v.Layout != "span" {
		none = tr(c, "（跟随默认壁纸）")
	}
	opts := []string{none}
	label := map[string]string{}
	ids := map[string]string{}
	for _, it := range lib {
		l := it.Title
		if _, dup := ids[l]; dup {
			l = it.Title + " · " + it.ID[:min(6, len(it.ID))]
		}
		opts = append(opts, l)
		label[it.ID] = l
		ids[l] = it.ID
	}
	sel := none
	if l, ok := label[cur]; ok {
		sel = l
	}
	if ui.Select(c, &sel, opts).Label(tr(c, "壁纸")).Width(260).Changed() {
		s.ctl.Assign(display, ids[sel])
	}
}

func (s *Settings) displayCard(c *ui.Context, v app.View, d app.DisplayView) {
	t := c.Theme()
	title := dispLabel(c, d)
	if d.Primary {
		title += tr(c, "（主屏）")
	}
	pixelW, pixelH := d.PixelW, d.PixelH
	if pixelW <= 0 || pixelH <= 0 {
		pixelW, pixelH = d.W, d.H
	}
	sub := fmt.Sprintf("%d × %d", pixelW, pixelH)
	if d.Alias != "" && d.Friendly != "" {
		sub += tr(c, " · 系统名称：") + d.Friendly
	}
	card(c.Key("disp"+d.ID), title, sub, func() {
		s.renameRow(c, d)
		// 状态条
		ui.Row(c.Key("state")).Wrap().Gap(8).Padding(6, 0, 10, 0).AlignItems(ui.Center).Children(func() {
			col := t.Success
			lv := levelNames[d.Level]
			switch {
			case d.State == "failed":
				col, lv = t.Danger, "已停用（崩溃次数过多）"
			case d.State == "recovering":
				col, lv = t.Warning, "正在恢复"
			case d.State == "idle" || d.ItemID == "":
				col, lv = t.TextMuted, "未设置壁纸"
			case d.Level != "L0":
				col = t.Warning
			}
			if d.ItemID != "" && d.State != "idle" {
				lv = d.Level + " · " + tr(c, lv)
			}
			chip(c, lv, col)
			if d.FPSCap > 0 {
				chip(c, fmt.Sprintf("限帧 %d fps", d.FPSCap), t.Accent)
			}
			if d.Backend != "" {
				chip(c, "后端 "+d.Backend, t.TextMuted)
			}
			hardwareBadge(c, d)

		})
		if r := reasonsText(c, d.Reasons); r != "" {
			ui.Text(c, tr(c, "暂停原因：")+r).FontSize(12).TextColor(t.TextMuted).Padding(0, 0, 6, 0)
		}
		if d.Note != "" {
			text(c, d.Note).FontSize(12).TextColor(t.Warning).Padding(0, 0, 6, 0)
		}
		hint := "当前跟随默认壁纸"
		if d.Custom {
			hint = "已单独设置；选“跟随默认壁纸”可取消"
		}
		if v.Layout == "span" {
			hint = "修改后应用到整个跨屏画布"
		}
		settingRow(c, "壁纸", hint, func() {
			s.itemSelect(c.Key("sel"+d.ID), v, d.ID, d.ItemID)
		})
		// 填充、速度、声音默认跟随壁纸组自带的属性；这里选具体值就是这块屏的覆盖项。
		follow := tr(c, "跟随壁纸")
		eff := d.Effective
		settingRow(c, "填充方式", fmt.Sprintf(tr(c, "填充会裁切边缘，适应保留全图，拉伸铺满屏幕。当前：%s"), tr(c, fitNames[eff.Fit])), func() {
			fit := follow
			if d.Options.Fit != "" {
				fit = tr(c, fitNames[d.Options.Fit])
			}
			opts := []string{follow}
			for _, l := range fitLabels {
				opts = append(opts, tr(c, l))
			}
			if ui.Select(c.Key("fit"+d.ID), &fit, opts).Label(tr(c, "填充方式")).Disabled(!d.Playback.Fit).Width(180).Changed() {
				o := d.Options
				o.Fit = ""
				for _, l := range fitLabels {
					if tr(c, l) == fit {
						o.Fit = fitIDs[l]
					}
				}
				s.ctl.SetDisplayOptions(d.ID, o)
			}
		})
		speedHint := "跟随壁纸；修改即为这块屏单独设置。不支持即时调整的后端会重新加载，静态图片不支持速度"
		if d.Options.Speed != 0 {
			speedHint = "已单独设置，不再跟随壁纸"
		}
		settingRow(c, "播放速度", speedHint, func() {
			speed := eff.Speed
			if d.Options.Speed != 0 && button(c.Key("speedreset"+d.ID), "跟随壁纸").Clicked() {
				o := d.Options
				o.Speed = 0
				s.ctl.SetDisplayOptions(d.ID, o)
			}
			if ui.NumberInput(c.Key("speed"+d.ID), &speed, 0.25, 4, 0.25).Label(tr(c, "播放速度")).Disabled(!d.Playback.Speed).Changed() {
				o := d.Options
				o.Speed = speed
				s.ctl.SetDisplayOptions(d.ID, o)
			}
			text(c, "倍速").FontSize(12).TextColor(t.TextMuted)
		})
		audioNow := "播放声音"
		if eff.Muted {
			audioNow = "静音"
		}
		settingRow(c, "声音", fmt.Sprintf(tr(c, "默认使用壁纸自己的声音设置。当前：%s"), tr(c, audioNow)), func() {
			names := map[string]string{"": follow, "mute": tr(c, "静音"), "sound": tr(c, "播放声音")}
			audio := names[d.Options.Audio]
			if ui.Select(c.Key("audio"+d.ID), &audio, []string{follow, tr(c, "静音"), tr(c, "播放声音")}).Label(tr(c, "这块屏的声音")).Disabled(!d.Playback.Audio).Width(180).Changed() {
				o := d.Options
				for id, n := range names {
					if n == audio {
						o.Audio = id
					}
				}
				s.ctl.SetDisplayOptions(d.ID, o)
			}
		})
		settingRow(c, "性能档位", "只覆盖这块屏幕，电源与暂停策略仍优先", func() {
			profile := map[string]string{"": "跟随全局", "smooth": "流畅", "balanced": "均衡", "saver": "省电"}[d.Options.Profile]
			if selectControl(c.Key("profile"+d.ID), &profile, []string{"跟随全局", "流畅", "均衡", "省电"}).Label(tr(c, "屏幕性能档位")).Width(180).Changed() {
				opts := d.Options
				opts.Profile = map[string]string{"跟随全局": "", "流畅": "smooth", "均衡": "balanced", "省电": "saver"}[profile]
				s.ctl.SetDisplayOptions(d.ID, opts)
			}
		})
		settingRow(c, "暂停此屏幕", "其他屏幕不受影响；继续后仍会遵循自动暂停规则", func() {
			b := d.Options.Paused
			if ui.Switch(c.Key("pz"+d.ID), &b).Label(tr(c, "暂停此屏幕 开关")).Changed() {
				opts := d.Options
				opts.Paused = b
				s.ctl.SetDisplayOptions(d.ID, opts)
			}
		})
		if !d.Playback.Fit || !d.Playback.Speed || !d.Playback.Audio {
			text(c, "当前后端或壁纸类型不支持部分播放属性，相关控件已禁用。").FontSize(12).TextColor(t.Warning).Wrap().Padding(6, 0)
		}
		if d.Backend != "" && d.Backend != "image" {
			textf(c, "当前渲染会话丢帧 %d", d.Dropped).FontSize(12).TextColor(t.TextMuted).Padding(6, 0, 0, 0)
		}
		settingRow(c.Key("pausedDuration"), "本周暂停时长", "仅统计已启动动态壁纸的暂停时间；不代表实际节电量", func() {
			ui.Text(c, metricDuration(c.Locale(), d.Metrics.Paused)).Bold().TextColor(t.Accent)
		})
		metricPeriod(c, d.Metrics)
	})
}

// renameRow 是显示器名称：别名按稳定 ID 保存，显示在系统名称的位置上；可恢复默认。
func (s *Settings) renameRow(c *ui.Context, d app.DisplayView) {
	editing := s.renameID == d.ID
	desc := "系统名称"
	switch {
	case d.Alias != "":
		desc = "自定义名称"
	case d.Friendly == "":
		desc = "系统没有提供名称，按排列顺序编号"
	}
	settingRow(c.Key("rn"+d.ID), "名称", desc, func() {
		if !editing {
			ui.Text(c, displayName(c, d)).Bold().FontSize(13).SingleLine().Ellipsis("…")
			if button(c.Key("rename"+d.ID), "重命名").Clicked() {
				s.renameID, s.renameBuf = d.ID, displayName(c, d)
			}
			if d.Alias != "" {
				if button(c.Key("rnreset"+d.ID), "恢复默认名称").Clicked() {
					s.ctl.SetAlias(d.ID, "")
					c.Toast(tr(c, "已恢复默认名称"))
				}
			}
			return
		}
		in := ui.TextInput(c.Key("alias"+d.ID), &s.renameBuf).Placeholder(displayName(c, app.DisplayView{Index: d.Index, Name: DefaultName(d), Friendly: d.Friendly})).Label(tr(c, "显示器名称")).Width(200)
		save := func() {
			id, name := d.ID, strings.TrimSpace(s.renameBuf)
			if name == DefaultName(d) || name == displayName(c, app.DisplayView{Index: d.Index, Name: DefaultName(d), Friendly: d.Friendly}) {
				name = "" // 和系统名称相同就不存别名
			}
			s.ctl.SetAlias(id, name)
			s.renameID, s.renameBuf = "", ""
			c.Toast(tr(c, "已保存名称"))
		}
		if in.Submitted() {
			save()
			return
		}
		if primaryButton(c.Key("rnsave"+d.ID), "保存").Clicked() {
			save()
			return
		}
		if button(c.Key("rncancel"+d.ID), "取消").Clicked() {
			s.renameID, s.renameBuf = "", ""
		}
	})
}

func reasonsText(c *ui.Context, reasons string) string {
	if reasons == "" {
		return ""
	}
	parts := strings.Split(reasons, "|")
	for i, reason := range parts {
		if name, ok := reasonNames[reason]; ok {
			parts[i] = tr(c, name)
		}
	}
	separator := "、"
	if strings.HasPrefix(c.Locale(), "en") {
		separator = ", "
	}
	return strings.Join(parts, separator)
}

// DefaultName 是没有别名时的名称（系统友好名或“显示器 N”）。
func DefaultName(d app.DisplayView) string { return app.DisplayName("", d.Friendly, d.Index) }

// Unknown/empty hardware status is not evidence that hardware decoding worked.
func hardwareBadge(c *ui.Context, d app.DisplayView) {
	t := c.Theme()
	if d.Backend == "" || d.Backend == "image" {
		return
	}
	switch d.HWDec {
	case "", "unknown":
		chip(c, "硬解状态未验证", t.TextMuted)
	case "no":
		chip(c, "软解", t.Warning)
	default:
		chip(c, "硬解 "+d.HWDec, t.Success)
	}
}

func metricDuration(lang string, d time.Duration) string {
	if d <= 0 {
		return fmt.Sprintf(translate(lang, "%d 分钟"), 0)
	}
	if d < time.Minute {
		return translate(lang, "不足 1 分钟")
	}
	if d < time.Hour {
		return fmt.Sprintf(translate(lang, "%d 分钟"), int64(d/time.Minute))
	}
	return fmt.Sprintf(translate(lang, "%d 小时 %d 分钟"), int64(d/time.Hour), int64(d/time.Minute)%60)
}

func metricPeriod(c *ui.Context, m app.DisplayMetricsView) {
	if m.ClockAdjusted {
		text(c, "检测到系统时钟调整，已跳过不可靠的计时间隔；历史记录保留。").FontSize(11).TextColor(c.Theme().Warning).Wrap().Padding(4, 0)
	}
	if m.WeekStart == "" {
		text(c, "尚无运行记录").FontSize(11).TextColor(c.Theme().TextMuted).Padding(4, 0)
		return
	}
	textf(c, "统计周起始：%s（UTC，周一）", m.WeekStart).FontSize(11).TextColor(c.Theme().TextMuted).Wrap().Padding(4, 0)
}
