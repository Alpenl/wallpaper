package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

func (s *Settings) set(fn func(*config.Config)) { s.ctl.UpdateConfig(fn) }

func (s *Settings) switchRow(c *ui.Context, key, title, desc string, on bool, set func(*config.Config, bool)) {
	settingRow(c, title, desc, func() {
		b := on
		if ui.Switch(c.Key(key), &b).Label(title + " 开关").Changed() {
			s.set(func(x *config.Config) { set(x, b) })
		}
	})
}

// wipSwitch 是 v1 功能的开关：显示“开发中”，点击时提示。
func (s *Settings) wipSwitch(c *ui.Context, key, title, desc, feature string) {
	settingRow(c, title, desc, func() {
		devTag(c)
		b := false
		if ui.Switch(c.Key(key), &b).Label(title).Changed() {
			s.wip(c, feature)
		}
	})
}

// ---- 发现 ----

func (s *Settings) discover(c *ui.Context, v app.View) {
	t := c.Theme()
	page(c, "发现与创意工坊", "在线壁纸、网页壁纸和 Shader 壁纸计划在 v1 提供", nil, func() {
		ui.Column(c.Key("hero")).Padding(24).Gap(8).Radius(14).Gradient(t.Accent, ui.Hex("#22d3ee"), 120).Children(func() {
			ui.Text(c, "更多壁纸，即将到来").Bold().FontSize(20).TextColor(ui.Hex("#ffffff"))
			ui.Text(c, "MVP 专注把视频壁纸做到省电、稳定。下面这些功能已经在设计文档里规划，正在开发中。").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.9)).Wrap()
		})
		type feat struct {
			ic          *ui.SVG
			title, desc string
		}
		feats := []feat{
			{icGlobe, "创意工坊 / 在线壁纸", "浏览、搜索和一键下载社区壁纸"},
			{icGlobe, "网页壁纸", "HTML 壁纸：沙箱、默认禁网、限帧 30 fps（08 章、10 章 10.6）"},
			{icSparkle, "Shader 壁纸", "Shadertoy 单 pass，自动降分辨率保证 GPU 帧时间 ≤ 4 ms"},
			{icSearch, "标签与颜色搜索", "按名称、标签、主色搜索 1000+ 张壁纸（F-13）"},
			{icSliders, "壁纸属性面板", "播放速度、填充方式、网页壁纸的自定义属性（F-18）"},
			{icShield, "按应用暂停", "指定的程序运行时自动暂停（F-19）"},
		}
		ui.Grid(c.Key("feats")).Columns(2).Gap(12).Children(func() {
			for i, f := range feats {
				f := f
				ui.Column(c.Key("feat"+itoa(i))).Padding(16).Gap(6).Radius(12).Background(cardBg(t)).Border(1, t.Border).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, f.ic).FontSize(18).TextColor(t.Accent)
						ui.Text(c, f.title).Bold().FontSize(14).Grow(1)
						devTag(c)
					})
					ui.Text(c, f.desc).FontSize(12).TextColor(t.TextMuted).Wrap()
					if ui.Button(c.Key("try"), "了解进度").AlignSelf(ui.Start).Clicked() {
						s.wip(c, f.title)
					}
				})
			}
		})
	})
}

// ---- 播放与性能 ----

var profiles = []struct{ id, name, desc string }{
	{"smooth", "流畅", "按素材原帧率播放"},
	{"balanced", "均衡", "推荐。按原帧率播放，导入时建议转码到 30 fps"},
	{"saver", "省电", "始终限制在 15 fps，适合笔记本"},
}

func (s *Settings) perf(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "播放与性能", "性能优先：可见时硬解零拷贝，不可见时自动暂停", nil, func() {
		card(c.Key("profile"), "性能档位", "限帧时帧率降到 15 fps；暂停规则见“暂停规则”页", func() {
			ui.Row(c.Key("profs")).Gap(10).Padding(8, 0).Children(func() {
				for _, p := range profiles {
					p := p
					sel := cfg.Perf.Profile == p.id
					b := ui.Column(c.Key("p"+p.id)).Grow(1).Basis(0).Padding(12).Gap(4).Radius(10).Border(1, t.Border).Cursor(ui.CursorPointer).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, p.name).Bold().FontSize(14).Grow(1)
							if sel {
								ui.Icon(c, icCheck).FontSize(14).TextColor(t.Accent)
							}
						})
						ui.Text(c, p.desc).FontSize(12).TextColor(t.TextMuted).Wrap()
					})
					if sel {
						b.Border(2, t.Accent).Background(t.Accent.Alpha(0.06))
					}
					if b.Clicked() && !sel {
						id := p.id
						s.set(func(x *config.Config) { x.Perf.Profile = id })
					}
				}
			})
			settingRow(c, "渲染分辨率", "降低分辨率渲染再放大，适合 Shader / 网页壁纸", func() {
				devTag(c)
				f := cfg.Perf.RenderScale * 100
				if ui.StepSlider(c.Key("scale"), &f, 50, 100, 25).Width(160).Label("渲染分辨率").Changed() {
					s.wip(c, "渲染分辨率")
				}
			})
			s.wipSwitch(c, "audio", "播放壁纸声音", "MVP 一律静音播放（06 章：去音轨利于无缝循环）", "壁纸声音")
		})
		card(c.Key("decode"), "解码与渲染", "", func() {
			if s.GOOS == "windows" {
				settingRow(c, "渲染后端", "mpv（默认，兼容性最好）或 Media Foundation（系统组件，不需要 mpv）。切换后壁纸会重新加载（D-04）", func() {
					cur := "mpv"
					if cfg.Render.WindowsBackend == "mf" {
						cur = "Media Foundation"
					}
					if ui.Select(c.Key("backend"), &cur, []string{"mpv", "Media Foundation"}).Label("渲染后端").Width(180).Changed() {
						b := "mpv"
						if cur == "Media Foundation" {
							b = "mf"
						}
						s.set(func(x *config.Config) { x.Render.WindowsBackend = b })
					}
				})
			} else {
				backend := "mpv（--wid 嵌入）"
				if s.GOOS == "darwin" {
					backend = "AVFoundation（AVPlayerLayer，系统合成）"
				}
				settingRow(c, "渲染后端", "本平台只有一个后端", func() {
					ui.Text(c, backend).FontSize(13).TextColor(t.TextMuted)
				})
			}
			if s.GOOS != "darwin" {
				settingRow(c, "硬件解码", "建议保持自动；遇到花屏、黑屏再关闭", func() {
					cur := "自动"
					if cfg.Render.Hwdec == "no" {
						cur = "关闭（软解）"
					}
					if ui.Select(c.Key("hwdec"), &cur, []string{"自动", "关闭（软解）"}).Label("硬件解码").Width(180).Changed() {
						h := ""
						if cur != "自动" {
							h = "no"
						}
						s.set(func(x *config.Config) { x.Render.Hwdec = h })
					}
				})
				if s.Info.MpvPath != "" {
					settingRow(c, "mpv 位置", "", func() {
						ui.Text(c, s.Info.MpvPath).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MaxWidth(360)
					})
				}
			}
		})
		card(c.Key("transcode"), "导入转码（F-07）", "把超规格或不利于循环的视频转成 30 fps、去音轨、每秒一个关键帧的版本", func() {
			enc := s.Info.EncoderName
			if !v.HasEncoder {
				enc = ""
			}
			settingRow(c, "编码器", "本应用不附带编码器：macOS 用系统自带 avconvert，Windows / Linux 使用你自己安装的 ffmpeg", func() {
				if enc != "" {
					chip(c, enc, t.Success)
				} else {
					chip(c, "未找到", t.Warning)
				}
			})
			if s.GOOS != "darwin" {
				if !s.ffInit {
					s.ffmpeg, s.ffInit = cfg.Render.FFmpegPath, true
				}
				settingRow(c, "ffmpeg 路径", "留空则在 PATH 里查找；修改后重启应用生效", func() {
					ui.TextInput(c.Key("ff"), &s.ffmpeg).Placeholder("例如 C:\\ffmpeg\\bin\\ffmpeg.exe").Width(260)
					if ui.Button(c.Key("ffsave"), "保存").Disabled(s.ffmpeg == cfg.Render.FFmpegPath).Clicked() {
						p := strings.TrimSpace(s.ffmpeg)
						s.set(func(x *config.Config) { x.Render.FFmpegPath = p })
						c.Toast("已保存，重启应用后生效")
					}
				})
			}
			settingRow(c, "目标帧率", "06 章 6.4：30 fps 兼顾流畅和功耗", func() {
				ui.Text(c, "30 fps").FontSize(13).TextColor(t.TextMuted)
			})
			s.wipSwitch(c, "autotc", "导入时自动转码", "目前在壁纸详情里一键转码；自动转码计划在 v1", "导入时自动转码")
		})
	})
}

// ---- 暂停规则 ----

func (s *Settings) pause(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "暂停规则", "看不见的壁纸不消耗资源：按屏判定，事件发生后 500 ms 内暂停（F-04）", nil, func() {
		card(c.Key("auto"), "自动暂停", "", func() {
			settingRow(c, "暂停全部壁纸", "手动暂停，和托盘菜单里的“暂停全部”相同", func() {
				b := v.PausedAll
				if ui.Switch(c.Key("all"), &b).Label("暂停全部壁纸").Changed() {
					s.ctl.SetPausedAll(b)
				}
			})
			s.switchRow(c, "fs", "其他程序全屏时暂停", "游戏、视频播放器全屏时，所在屏的壁纸暂停", cfg.Pause.OnFullscreen, func(x *config.Config, b bool) { x.Pause.OnFullscreen = b })
			s.switchRow(c, "cov", "桌面被窗口完全遮挡时暂停", "最大化窗口或多个窗口盖满 95% 以上桌面时暂停", cfg.Pause.OnCovered, func(x *config.Config, b bool) { x.Pause.OnCovered = b })
			for _, r := range []struct{ k, title, desc string }{
				{"lock", "锁屏、睡眠、显示器关闭时暂停", "始终启用"},
				{"rdp", "远程桌面会话时暂停", "始终启用（远程传输动态壁纸很耗带宽）"},
			} {
				settingRow(c, r.title, r.desc, func() {
					b := true
					ui.Switch(c.Key(r.k), &b).Label(r.title).Disabled(true)
				})
			}
		})
		card(c.Key("resume"), "恢复", "", func() {
			settingRow(c, "恢复前等待", "条件解除后再等一会儿才恢复播放，避免切换窗口时反复启停（毫秒）", func() {
				f := float64(cfg.Pause.ResumeDebounceMs)
				if ui.NumberInput(c.Key("deb"), &f, 100, 5000, 100).Label("恢复前等待").Width(120).Changed() {
					n := int(f)
					s.set(func(x *config.Config) { x.Pause.ResumeDebounceMs = n })
				}
			})
		})
		card(c.Key("apps"), "按应用暂停（F-19）", "指定的程序在前台运行时暂停，只按可执行文件匹配，不读取窗口标题", func() {
			ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "还没有规则").FontSize(13).TextColor(t.TextMuted).Grow(1)
				devTag(c)
				if iconButton(c.Key("addapp"), icPlus, "添加应用…", false).Clicked() {
					s.wip(c, "按应用暂停")
				}
			})
		})
		open := false
		card(c.Key("how"), "暂停是如何分级的", "", func() {
			ui.Collapsible(c.Key("levels"), "展开说明（07 章状态机）", &open, func() {
				for _, l := range []string{
					"L1 暂停：停止解码和呈现，画面停在当前帧，恢复几乎无延迟。",
					"L3 释放：暂停超过一段时间后释放解码器和显存，恢复时从原位置重新加载。",
					"L4 截图：电量过低或长时间不可见时结束渲染进程，用首帧截图作为系统壁纸，零资源占用。",
					"限帧（15 fps）：使用电池（“降到 15 fps”策略）、设备过热或省电档位时启用，不暂停。",
				} {
					ui.Text(c, "• "+l).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(3, 0)
				}
			})
		})
	})
}

// ---- 电源 ----

func (s *Settings) power(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "电源与电池", "拔掉电源后 1 秒内生效（F-05）", nil, func() {
		ui.Row(c.Key("now")).Gap(12).Children(func() {
			stat := func(k, label, val string, col ui.Color) {
				ui.Column(c.Key(k)).Grow(1).Basis(0).Padding(14, 16).Gap(4).Radius(12).Background(cardBg(t)).Border(1, t.Border).Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
					ui.Text(c, val).Bold().FontSize(16).TextColor(col)
				})
			}
			src, col := "交流电源", t.Success
			if v.Power.OnBattery {
				src, col = "电池", t.Warning
			}
			stat("src", "当前电源", src, col)
			pct := "—"
			if v.Power.Percent >= 0 {
				pct = fmt.Sprintf("%d%%", v.Power.Percent)
			}
			stat("pct", "电量", pct, t.Text)
			saver := "关闭"
			if v.Power.Saver {
				saver = "开启"
			}
			stat("saver", "系统省电模式", saver, t.Text)
			th, thc := "正常", t.Success
			switch v.Power.Thermal {
			case "ThermalSerious":
				th, thc = "过热（已限帧）", t.Warning
			case "ThermalCritical":
				th, thc = "严重过热（已暂停）", t.Danger
			}
			stat("therm", "设备温度", th, thc)
		})
		card(c.Key("batt"), "使用电池时", "", func() {
			pol := cfg.Pause.BatteryPolicy
			ui.Column(c.Key("pol")).Gap(4).Padding(8, 0).Children(func() {
				for _, o := range []struct{ id, title, desc string }{
					{"pause", "暂停壁纸", "最省电（默认，D-07）"},
					{"fps15", "降到 15 fps", "保留动态效果，解码和呈现负载约减半"},
					{"ignore", "继续正常播放", "和插电时一样，耗电最多"},
				} {
					o := o
					ui.Row(c.Key("r"+o.id)).Gap(10).Padding(6, 0).AlignItems(ui.Center).Children(func() {
						if ui.Radio(c.Key("rb"), &pol, o.id, o.title).Changed() {
							id := o.id
							s.set(func(x *config.Config) { x.Pause.BatteryPolicy = id })
						}
						ui.Text(c, o.desc).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
			settingRow(c, "低电量时停止渲染", fmt.Sprintf("电量低于 %d%% 时结束渲染进程、显示截图（L4）", cfg.Pause.LowBatteryPct), func() {
				f := float64(cfg.Pause.LowBatteryPct)
				if ui.StepSlider(c.Key("low"), &f, 0, 50, 5).Width(180).Label("低电量阈值").Changed() {
					n := int(f)
					s.set(func(x *config.Config) { x.Pause.LowBatteryPct = n })
				}
				ui.Textf(c, "%d%%", cfg.Pause.LowBatteryPct).FontSize(13).Width(40)
			})
		})
		card(c.Key("sys"), "系统状态联动", "", func() {
			for _, r := range []struct{ k, title, desc string }{
				{"saver", "系统省电模式开启时暂停", "Windows 节电模式 / macOS 低电量模式"},
				{"hot", "设备过热时降到 15 fps", "macOS thermalState 为“严重”时限帧，“危急”时暂停"},
			} {
				settingRow(c, r.title, r.desc+" · 始终启用", func() {
					b := true
					ui.Switch(c.Key(r.k), &b).Label(r.title).Disabled(true)
				})
			}
		})
	})
}

// ---- 外观 ----

func (s *Settings) look(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "外观", "", nil, func() {
		card(c.Key("theme"), "主题", "", func() {
			settingRow(c, "颜色模式", "", func() {
				i := map[string]int{"system": 0, "light": 1, "dark": 2}[cfg.UI.Theme]
				if ui.Segmented(c.Key("mode"), &i, "跟随系统", "浅色", "深色").Label("颜色模式").Changed() {
					m := []string{"system", "light", "dark"}[i]
					s.set(func(x *config.Config) { x.UI.Theme = m })
				}
			})
			settingRow(c, "强调色", "", func() {
				ui.Row(c.Key("acc")).Gap(8).Children(func() {
					for _, a := range accents {
						a := a
						sw := ui.Box(c.Key("a"+a.id)).Size(24, 24).Radius(12).Background(ui.Hex(a.hex)).Cursor(ui.CursorPointer).Label(a.name).Tooltip(a.name)
						if cfg.UI.Accent == a.id {
							sw.Border(3, t.Text.Alpha(0.6))
						}
						if sw.Clicked() {
							id := a.id
							s.set(func(x *config.Config) { x.UI.Accent = id })
						}
					}
				})
			})
		})
		card(c.Key("lang"), "语言与布局", "", func() {
			settingRow(c, "界面语言", "", func() {
				l := "简体中文"
				if ui.Select(c.Key("lsel"), &l, []string{"简体中文", "English"}).Label("界面语言").Width(160).Changed() && l != "简体中文" {
					s.wip(c, "英文界面")
				}
			})
			settingRow(c, "壁纸库默认视图", "", func() {
				i := 0
				if cfg.UI.LibraryView == "list" {
					i = 1
				}
				if ui.Segmented(c.Key("lv"), &i, "网格", "列表").Label("壁纸库视图").Changed() {
					lv := []string{"grid", "list"}[i]
					s.set(func(x *config.Config) { x.UI.LibraryView = lv })
				}
			})
		})
	})
}

// ---- 启动与更新 ----

func (s *Settings) startup(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "启动与更新", "", nil, func() {
		card(c.Key("boot"), "启动", "", func() {
			settingRow(c, "开机自动启动", "Windows：当前用户的 Run 项；macOS：登录项。默认关闭（F-10）", func() {
				b := cfg.Startup.LaunchAtLogin
				if ui.Switch(c.Key("login"), &b).Label("开机自动启动 开关").Disabled(s.SetLaunchAtLogin == nil).Changed() {
					s.set(func(x *config.Config) { x.Startup.LaunchAtLogin = b; x.Startup.Asked = true })
				}
			})
			s.wipSwitch(c, "minimized", "启动时不打开设置窗口", "目前首次运行时打开，之后只在托盘常驻", "启动行为设置")
		})
		card(c.Key("upd"), "更新（F-15）", "增量更新：只下载变化的文件；签名校验失败或新版本连续启动失败会自动回滚", func() {
			s.mu.Lock()
			u := s.upd
			s.mu.Unlock()
			settingRow(c, "当前版本", s.Info.Platform, func() {
				ui.Text(c, s.Info.Version).Bold().FontSize(13)
			})
			s.switchRow(c, "auto", "自动检查更新", "启动后检查一次；只下载清单，不上传任何信息", cfg.Update.AutoCheck, func(x *config.Config, b bool) { x.Update.AutoCheck = b })
			if !s.feedInit {
				s.feed, s.feedInit = cfg.Update.FeedURL, true
			}
			settingRow(c, "更新源", "HTTPS 目录地址，包含 <平台>/manifest.json 和签名", func() {
				ui.TextInput(c.Key("feed"), &s.feed).Placeholder("https://example.com/wallpaper/updates").Width(280)
				if ui.Button(c.Key("feedsave"), "保存").Disabled(s.feed == cfg.Update.FeedURL).Clicked() {
					f := strings.TrimSpace(s.feed)
					s.set(func(x *config.Config) { x.Update.FeedURL = f })
					c.Toast("更新源已保存")
				}
			})
			ui.Row(c.Key("updacts")).Gap(8).Padding(10, 0, 4, 0).AlignItems(ui.Center).Children(func() {
				if u.info == nil {
					if iconButton(c.Key("check"), icRefresh, "检查更新", true).Disabled(u.busy || s.CheckUpdate == nil).Clicked() {
						s.checkUpdate()
					}
				} else if iconButton(c.Key("install"), icDownload, "下载并安装 "+u.info.Version, true).Disabled(u.busy || s.ApplyUpdate == nil).Clicked() {
					s.applyUpdate()
				}
				if s.CanRollback != nil {
					if prev, ok := s.CanRollback(); ok {
						if ui.Button(c.Key("rb"), "回滚到 "+prev).Disabled(u.busy).Clicked() {
							s.rollback()
						}
					}
				}
				if u.busy {
					ui.Spinner(c).Size(14, 14)
				}
				if u.msg != "" {
					ui.Text(c, u.msg).FontSize(12).TextColor(t.TextMuted).Wrap().Grow(1)
				}
			})
			if u.busy && u.progress > 0 {
				ui.Progress(c.Key("prog"), u.progress)
			}
			if !s.Info.UpdateKey {
				ui.Text(c, "这个构建没有内置更新公钥，无法验证更新包，因此不能在线更新。正式发布版会在构建时写入公钥。").FontSize(12).TextColor(t.Warning).Wrap().Padding(4, 0)
			}
			if !s.Info.SignedBuild {
				ui.Text(c, "这个构建未做代码签名：Windows 可能弹出 SmartScreen 提示，macOS 需要右键“打开”。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(4, 0, 8, 0)
			}
		})
	})
}

func (s *Settings) setUpd(fn func(u *updState)) {
	s.mu.Lock()
	fn(&s.upd)
	s.mu.Unlock()
	s.invalidate()
}

func (s *Settings) checkUpdate() {
	s.setUpd(func(u *updState) { u.busy, u.msg = true, "正在检查…" })
	go func() {
		info, err := s.CheckUpdate()
		s.setUpd(func(u *updState) {
			u.busy = false
			switch {
			case err != nil:
				u.msg = err.Error()
			case info == nil:
				u.msg = "已是最新版本"
			default:
				u.info = info
				u.msg = fmt.Sprintf("发现新版本 %s，增量下载约 %s", info.Version, humanBytes(info.Size))
			}
		})
	}()
}

func (s *Settings) applyUpdate() {
	s.setUpd(func(u *updState) { u.busy, u.msg, u.progress = true, "正在下载…", 0 })
	go func() {
		err := s.ApplyUpdate(func(done, total int64) {
			s.setUpd(func(u *updState) {
				if total > 0 {
					u.progress = float64(done) / float64(total)
				}
			})
		})
		s.setUpd(func(u *updState) {
			u.busy = false
			if err != nil {
				u.msg = "更新失败（已还原）：" + err.Error()
				return
			}
			u.info = nil
			u.msg = "更新已安装，重启应用后生效"
		})
		if err == nil && s.Restart != nil {
			s.Restart()
		}
	}()
}

func (s *Settings) rollback() {
	s.setUpd(func(u *updState) { u.busy, u.msg = true, "正在回滚…" })
	go func() {
		err := s.Rollback()
		s.setUpd(func(u *updState) {
			u.busy = false
			if err != nil {
				u.msg = "回滚失败：" + err.Error()
				return
			}
			u.msg = "已回滚，重启应用后生效"
		})
		if err == nil && s.Restart != nil {
			s.Restart()
		}
	}()
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ---- 诊断 ----

func (s *Settings) diag(c *ui.Context, v app.View) {
	t := c.Theme()
	page(c, "诊断", "出问题时导出诊断包发给开发者；诊断包不含素材路径和窗口标题（F-16）", nil, func() {
		card(c.Key("export"), "诊断包", "", func() {
			ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
				if iconButton(c.Key("diagbtn"), icDownload, "导出诊断包", true).Disabled(s.ExportDiag == nil).Clicked() {
					if p, err := s.ExportDiag(); err != nil {
						s.diagMsg = "导出失败：" + err.Error()
					} else {
						s.diagMsg = "已导出：" + p
						if s.ShowInFolder != nil {
							s.ShowInFolder(p)
						}
					}
				}
				if iconButton(c.Key("logs"), icFolder, "打开日志目录", false).Disabled(s.OpenPath == nil || s.Info.LogsDir == "").Clicked() {
					if err := s.OpenPath(s.Info.LogsDir); err != nil {
						s.diagMsg = err.Error()
					}
				}
			})
			if s.diagMsg != "" {
				ui.Text(c, s.diagMsg).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(0, 0, 8, 0)
			}
		})
		card(c.Key("rt"), "运行状态", "每块屏当前的级别、后端和硬解状态（F-17 资源徽章的雏形）", func() {
			for _, d := range v.Displays {
				d := d
				settingRow(c.Key("rt"+d.ID), d.Name, ReasonsText(d.Reasons), func() {
					ui.Row(c).Gap(6).Children(func() {
						chip(c, d.Level+" "+levelNames[d.Level], t.Accent)
						if d.Backend != "" {
							chip(c, d.Backend, t.TextMuted)
						}
						if d.HWDec != "" {
							chip(c, "hwdec "+d.HWDec, t.TextMuted)
						}
						if d.FPSCap > 0 {
							chip(c, fmt.Sprintf("%d fps", d.FPSCap), t.Warning)
						}
					})
				})
			}
			settingRow(c, "本周省电时长", "按暂停时长统计的省电报告", func() { devTag(c) })
			settingRow(c, "安全模式", "连续异常退出后自动进入，壁纸不启动", func() {
				if v.SafeMode {
					chip(c, "已进入", t.Warning)
				} else {
					chip(c, "未进入", t.Success)
				}
			})
		})
		card(c.Key("priv"), "隐私", "", func() {
			ui.Text(c, "不收集任何遥测数据（D-15），没有崩溃自动上报。日志只保存在本机，诊断包只有你手动导出并发送时才会离开这台电脑。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(8, 0)
		})
		card(c.Key("reset"), "重置", "", func() {
			settingRow(c, "恢复默认设置", "不影响壁纸库和显示器分配", func() {
				if ui.Button(c.Key("resetbtn"), "恢复默认").Clicked() {
					s.resetAsk = true
				}
			})
		})
		if ui.AlertDialog(c.Key("resetdlg"), &s.resetAsk, "恢复默认设置？", "暂停规则、电源策略、性能档位和外观会恢复为默认值。", "取消", "恢复默认") == 1 {
			s.set(func(x *config.Config) {
				keep := x.Startup
				upd := x.Update
				r := x.Render
				*x = config.Defaults()
				x.Startup, x.Update = keep, upd
				x.Render.MpvPath, x.Render.FFmpegPath = r.MpvPath, r.FFmpegPath
			})
			c.Toast("已恢复默认设置")
		}
	})
}

// ---- 关于 ----

func (s *Settings) about(c *ui.Context, v app.View) {
	t := c.Theme()
	page(c, "关于", "", nil, func() {
		ui.Row(c.Key("hero")).Gap(16).Padding(20).Radius(14).Background(cardBg(t)).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(64, 64).Radius(16).Gradient(t.Accent, ui.Hex("#22d3ee"), 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Icon(c, icImage).FontSize(30).TextColor(ui.Hex("#ffffff"))
			})
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, "动态壁纸").Bold().FontSize(20)
				ui.Textf(c, "版本 %s · %s", s.Info.Version, s.Info.Platform).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "性能优先的跨平台动态壁纸。纯 Go 编写（CGO_ENABLED=0），原生窗口渲染，不用 WebView。").FontSize(12).TextColor(t.TextMuted).Wrap()
			})
		})
		card(c.Key("lic"), "开源组件与许可", "（非法律意见，见 11 章）", func() {
			for _, l := range []struct{ name, desc string }{
				{"MyGo", "原生 UI 与托盘（egoist/mygo）"},
				{"purego", "无 cgo 调用系统 API（ebitengine/purego，Apache-2.0）"},
				{"mpv", "Windows / Linux 视频播放，作为独立进程调用（GPL / LGPL，随安装包附带的 mpv.exe 来自 shinchiro 构建）"},
				{"编码器", "不附带任何编码器；转码调用系统 avconvert 或用户自己安装的 ffmpeg"},
			} {
				settingRow(c, l.name, l.desc, func() {})
			}
		})
		card(c.Key("data"), "数据位置", "", func() {
			settingRow(c, "配置与壁纸库", s.Info.DataDir, func() {
				if ui.Button(c.Key("opendata"), "打开").Disabled(s.OpenPath == nil || s.Info.DataDir == "").Clicked() {
					_ = s.OpenPath(s.Info.DataDir)
				}
			})
		})
	})
}
