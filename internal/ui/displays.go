package ui

import (
	"fmt"
	"sort"
	"strings"

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
	page(c, "显示器", "每块屏可以单独设置壁纸；暂停和省电按屏判定（F-03 / F-04）", nil, func() {
		if len(v.Displays) == 0 {
			card(c, "", "", func() {
				ui.Text(c, "没有检测到显示器。").TextColor(t.TextMuted).Padding(12, 0)
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
			const boxW, boxH = 640.0, 200.0
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
							name := dispLabel(d)
							if d.Primary {
								name += " · 主屏"
							}
							ui.Text(c, name).FontSize(11).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
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
		// 显示器选择条（与布局图同序同号）
		if len(v.Displays) > 1 {
			ui.Row(c.Key("dtabs")).Gap(8).Children(func() {
				for _, o := range v.Displays {
					o := o
					mk := ui.Button
					if o.ID == s.dispSel {
						mk = ui.PrimaryButton
					}
					if mk(c.Key("tab"+o.ID), dispLabel(o)).Clicked() && s.dispSel != o.ID {
						s.dispSel, s.renameID = o.ID, ""
					}
				}
			})
		}
		s.displayCard(c, v, *d)
		card(c.Key("default"), "默认壁纸", "没有单独设置壁纸的显示器（包括以后新接入的）使用它；已单独设置的屏不受影响", func() {
			settingRow(c, "默认壁纸", "要让所有屏都换成同一张，请在壁纸详情里选“应用到 → 所有显示器”", func() {
				s.itemSelect(c.Key("defsel"), v, "", v.Default)
			})
		})
		card(c.Key("advanced"), "多显示器高级选项", "", func() {
			settingRow(c, "跨屏拼接", "把一张壁纸铺满多块屏（Wallpaper Engine 的“跨越显示器”）", func() {
				devTag(c)
				b := false
				if ui.Switch(c.Key("span"), &b).Label("跨屏拼接").Changed() {
					s.wip(c, "跨屏拼接")
				}
			})
			settingRow(c, "复制到所有显示器", "把「"+d.Name+"」的壁纸设为默认，并清除所有屏的单独设置", func() {
				if ui.Button(c.Key("dup"), "复制").Disabled(d.ItemID == "").Clicked() {
					s.ctl.AssignAll(d.ItemID)
					c.Toast("已复制到所有显示器")
				}
			})
		})
	})
}

// itemSelect 是壁纸下拉框。display 为空表示默认壁纸。
func (s *Settings) itemSelect(c *ui.Context, v app.View, display, cur string) {
	lib := append([]config.Item(nil), v.Library...)
	sort.Slice(lib, func(i, j int) bool { return lib[i].AddedAt.Before(lib[j].AddedAt) })
	none := "（不设置，显示系统壁纸）"
	if display != "" {
		none = "（跟随默认壁纸）"
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
	if ui.Select(c, &sel, opts).Label("壁纸").Width(260).Changed() {
		s.ctl.Assign(display, ids[sel])
	}
}

func (s *Settings) displayCard(c *ui.Context, v app.View, d app.DisplayView) {
	t := c.Theme()
	title := dispLabel(d)
	if d.Primary {
		title += "（主屏）"
	}
	sub := fmt.Sprintf("%d × %d", d.W, d.H)
	if d.Alias != "" && d.Friendly != "" {
		sub += " · 系统名称：" + d.Friendly
	}
	card(c.Key("disp"+d.ID), title, sub, func() {
		s.renameRow(c, d)
		// 状态条
		ui.Row(c.Key("state")).Gap(8).Padding(6, 0, 10, 0).AlignItems(ui.Center).Children(func() {
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
			chip(c, lv, col)
			if d.FPSCap > 0 {
				chip(c, fmt.Sprintf("限帧 %d fps", d.FPSCap), t.Accent)
			}
			if d.Backend != "" {
				chip(c, "后端 "+d.Backend, t.TextMuted)
			}
			if d.HWDec != "" {
				hw := "硬解 " + d.HWDec
				col := t.Success
				if d.HWDec == "no" {
					hw, col = "软解", t.Warning
				}
				chip(c, hw, col)
			}
		})
		if r := ReasonsText(d.Reasons); r != "" {
			ui.Text(c, "暂停原因："+r).FontSize(12).TextColor(t.TextMuted).Padding(0, 0, 6, 0)
		}
		if d.Note != "" {
			ui.Text(c, d.Note).FontSize(12).TextColor(t.Warning).Padding(0, 0, 6, 0)
		}
		hint := "当前跟随默认壁纸"
		if d.Custom {
			hint = "已单独设置；选“跟随默认壁纸”可取消"
		}
		settingRow(c, "壁纸", hint, func() {
			s.itemSelect(c.Key("sel"+d.ID), v, d.ID, d.ItemID)
		})
		settingRow(c, "填充方式", "填充（裁切）/ 适应（留边）/ 拉伸 / 居中", func() {
			devTag(c)
			fit := "填充"
			if ui.Select(c.Key("fit"+d.ID), &fit, []string{"填充", "适应", "拉伸", "居中"}).Label("填充方式").Changed() {
				s.wip(c, "填充方式")
			}
		})
		settingRow(c, "单独暂停这块屏", "其他屏照常播放", func() {
			devTag(c)
			b := false
			if ui.Switch(c.Key("pz"+d.ID), &b).Label("单独暂停").Changed() {
				s.wip(c, "单独暂停某块屏")
			}
		})
		if d.Dropped > 0 {
			ui.Textf(c, "累计丢帧 %d", d.Dropped).FontSize(12).TextColor(t.TextMuted).Padding(6, 0, 0, 0)
		}
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
			ui.Text(c, d.Name).Bold().FontSize(13).SingleLine().Ellipsis("…")
			if ui.Button(c.Key("rename"+d.ID), "重命名").Clicked() {
				s.renameID, s.renameBuf = d.ID, d.Name
			}
			if d.Alias != "" {
				if ui.Button(c.Key("rnreset"+d.ID), "恢复默认名称").Clicked() {
					s.ctl.SetAlias(d.ID, "")
					c.Toast("已恢复默认名称")
				}
			}
			return
		}
		in := ui.TextInput(c.Key("alias"+d.ID), &s.renameBuf).Placeholder(DefaultName(d)).Label("显示器名称").Width(200)
		save := func() {
			id, name := d.ID, strings.TrimSpace(s.renameBuf)
			if name == DefaultName(d) {
				name = "" // 和系统名称相同就不存别名
			}
			s.ctl.SetAlias(id, name)
			s.renameID, s.renameBuf = "", ""
			c.Toast("已保存名称")
		}
		if in.Submitted() {
			save()
			return
		}
		if ui.PrimaryButton(c.Key("rnsave"+d.ID), "保存").Clicked() {
			save()
			return
		}
		if ui.Button(c.Key("rncancel"+d.ID), "取消").Clicked() {
			s.renameID, s.renameBuf = "", ""
		}
	})
}

// DefaultName 是没有别名时的名称（系统友好名或“显示器 N”）。
func DefaultName(d app.DisplayView) string { return app.DisplayName("", d.Friendly, d.Index) }
