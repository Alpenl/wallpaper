package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/render/mpv"
)

func (s *Settings) set(fn func(*config.Config)) { s.ctl.UpdateConfig(fn) }

func (s *Settings) switchRow(c *ui.Context, key, title, desc string, on bool, set func(*config.Config, bool)) {
	settingRow(c, title, desc, func() {
		b := on
		if ui.Switch(c.Key(key), &b).Label(tr(c, title+" 开关")).Changed() {
			s.set(func(x *config.Config) { set(x, b) })
		}
	})
}

// ---- 播放与性能 ----

var profiles = []struct{ id, name, desc string }{
	{"smooth", "流畅", "优先流畅，仍遵循设置的帧率上限"},
	{"balanced", "均衡", "推荐。按原帧率播放，导入时建议转码到 30 fps"},
	{"saver", "省电", "始终限制在 15 fps，适合笔记本"},
}

func (s *Settings) perf(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "播放与性能", "性能优先：可见时硬解零拷贝，不可见时自动暂停", nil, func() {
		card(c.Key("profile"), "性能档位", "省电档最高 15 fps；电源策略可进一步降低帧率或暂停", func() {
			ui.Row(c.Key("profs")).Gap(10).Padding(8, 0).Children(func() {
				for _, p := range profiles {
					p := p
					sel := cfg.Perf.Profile == p.id
					b := ui.Column(c.Key("p"+p.id)).Grow(1).Basis(0).Padding(12).Gap(4).Radius(10).Border(1, t.Border).Cursor(ui.CursorPointer).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							text(c, p.name).Bold().FontSize(14).Grow(1)
							if sel {
								ui.Icon(c, icCheck).FontSize(14).TextColor(t.Accent)
							}
						})
						text(c, p.desc).FontSize(12).TextColor(t.TextMuted).Wrap()
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
			settingRow(c, "帧率上限", "0 表示原始帧率；自动省电策略会采用更低的上限", func() {
				f := float64(cfg.Perf.FPSCap)
				if ui.NumberInput(c.Key("fps"), &f, 0, 240, 5).Label(tr(c, "帧率上限")).Changed() {
					s.set(func(x *config.Config) { x.Perf.FPSCap = int(f) })
				}
			})
			settingRow(c, "渲染缩放", "mpv 软件滤镜缩小视频帧，不改变桌面分辨率，可能增加 CPU 负载", func() {
				f := cfg.Perf.RenderScale * 100
				if ui.StepSlider(c.Key("scale"), &f, 50, 100, 25).Width(160).Label(tr(c, "渲染缩放")).Disabled(!s.mpvControls(v)).Changed() {
					s.set(func(x *config.Config) { x.Perf.RenderScale = f / 100 })
				}
				textf(c, "%.0f%%", f).Width(44)
			})
			s.switchRowDisabled(c, "mute", "静音播放", "默认设置：只作用于没有单独设置声音的壁纸；修改后重新加载动态壁纸", cfg.Render.Muted, false, func(x *config.Config, b bool) { x.Render.Muted = b })
			s.switchRow(c, "resumePos", "从上次位置继续播放", "重新启动后恢复播放位置；mpv、Media Foundation 和 AVFoundation 支持续播", cfg.Render.ResumePosition, func(x *config.Config, b bool) { x.Render.ResumePosition = b })
		})
		card(c.Key("decode"), "解码与渲染", "", func() {
			if s.GOOS == "windows" {
				settingRow(c, "渲染后端", "mpv（默认，兼容性最好）或 Media Foundation（系统组件，不需要 mpv）。切换后壁纸会重新加载（D-04）", func() {
					cur := "mpv"
					if cfg.Render.WindowsBackend == "mf" {
						cur = "Media Foundation"
					}
					if selectControl(c.Key("backend"), &cur, []string{"mpv", "Media Foundation"}).Label(tr(c, "渲染后端")).Width(180).Changed() {
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
					text(c, backend).FontSize(13).TextColor(t.TextMuted)
				})
			}
			if s.GOOS != "darwin" {
				settingRow(c, "硬件解码", "建议保持自动；遇到花屏、黑屏再关闭", func() {
					cur := "自动"
					if cfg.Render.Hwdec == "no" {
						cur = "关闭（软解）"
					}
					if selectControl(c.Key("hwdec"), &cur, []string{"自动", "关闭（软解）"}).Label(tr(c, "硬件解码")).Width(180).Changed() {
						h := ""
						if cur != "自动" {
							h = "no"
						}
						s.set(func(x *config.Config) { x.Render.Hwdec = h })
					}
				})
				s.mpvPathEditor(c, cfg)
			}
		})
		card(c.Key("transcode"), "导入与转码", "标准转换会验证 30 fps、无音轨和目标尺寸；系统兼容转换能力有限", func() {
			enc := v.Encoder.Name
			if enc == "" && v.HasEncoder {
				enc = s.Info.EncoderName
			}
			settingRow(c, "编码器", "不捆绑第三方编码器；macOS 默认使用系统 AVFoundation，可选配置自己的 ffmpeg", func() {
				if enc != "" {
					chip(c, enc, t.Success)
				} else {
					chip(c, "未找到", t.Warning)
				}
			})
			{
				if !s.ffInit {
					s.ffmpeg, s.ffInit = cfg.Render.FFmpegPath, true
				}
				settingRow(c, "ffmpeg 路径", "留空使用默认编码器（macOS 优先系统 AVFoundation）；保存后重新检测", func() {
					ui.TextInput(c.Key("ff"), &s.ffmpeg).Placeholder(tr(c, "编码器可执行文件路径")).Width(260)
					if button(c.Key("ffsave"), "保存").Disabled(s.ffmpeg == cfg.Render.FFmpegPath).Clicked() {
						p := strings.TrimSpace(s.ffmpeg)
						s.set(func(x *config.Config) { x.Render.FFmpegPath = p })
						c.Toast(tr(c, "编码器路径已提交，检测结果以状态为准"))
					}
				})
			}
			settingRow(c, "目标帧率", "只有通过产物检查的标准转换才保证 30 fps 和去音轨", func() {
				label := "依编码器能力"
				if v.Encoder.Strict {
					label = "30 fps"
				}
				text(c, label).FontSize(13).TextColor(t.TextMuted)
			})
			settingRow(c, "目标分辨率", "按边界等比缩小、不放大小图；主屏选项使用物理像素，多个屏共用一份产物", func() {
				labels := map[string]string{"display": "主屏物理分辨率", "1080p": "1920 × 1080", "1440p": "2560 × 1440", "2160p": "3840 × 2160"}
				cur := labels[cfg.Render.TranscodeResolution]
				if cur == "" {
					cur = labels["display"]
				}
				if selectControl(c.Key("target-resolution"), &cur, []string{labels["display"], labels["1080p"], labels["1440p"], labels["2160p"]}).Label(tr(c, "目标分辨率")).Width(220).Changed() {
					for key, label := range labels {
						if cur == label {
							chosen := key
							s.set(func(x *config.Config) { x.Render.TranscodeResolution = chosen })
							break
						}
					}
				}
			})
			if v.HasEncoder && !v.Encoder.Strict {
				text(c, "兼容转换不保证固定帧率、去音轨或 GIF；需要这些能力时可配置 ffmpeg").FontSize(12).TextColor(t.Warning).Wrap()
			}
			s.switchRowDisabled(c, "autotc", "导入时自动转码", "对建议优化的导入项自动转码；原文件保留", cfg.Render.AutoTranscode, !v.HasEncoder, func(x *config.Config, b bool) { x.Render.AutoTranscode = b })
		})
	})
}

// mpvPathEditor submits through UpdateConfig so the app preserves its existing
// pause, safe-mode and display lifecycle rules when reloading renderers.
func (s *Settings) mpvPathEditor(c *ui.Context, cfg config.Config) {
	if !s.mpvPathInit {
		s.mpvPath, s.mpvPathInit = cfg.Render.MpvPath, true
		if s.mpvPath != "" {
			if _, err := mpv.ResolveExecutable(s.mpvPath); err != nil {
				s.mpvPathError = err.Error()
			}
		}
	}
	settingRow(c, "mpv 路径", "留空自动查找；保存后按当前暂停与电源状态重新加载壁纸", func() {
		ui.TextInput(c.Key("mpv-path"), &s.mpvPath).Placeholder(tr(c, "mpv 可执行文件路径")).Label(tr(c, "mpv 可执行文件路径")).Width(230)
		if button(c.Key("mpv-save"), "保存 mpv 路径").Disabled(strings.TrimSpace(s.mpvPath) == cfg.Render.MpvPath).Clicked() {
			if s.saveMpvPath() {
				c.Toast(tr(c, "mpv 路径已提交，播放结果以显示器状态为准"))
			}
		}
	})
	text(c, "本安装包不捆绑 mpv。Windows/Linux 使用 mpv 后端播放动态壁纸前，请自行安装 mpv；自动查找不会下载或安装播放器。").FontSize(12).TextColor(c.Theme().Warning).Wrap()
	text(c, "Windows 请选择 mpv.exe，并保留所需 DLL 等配套文件；不要选择 libmpv DLL。macOS 使用系统 AVFoundation，不需要 mpv。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
	if s.mpvPathError != "" {
		text(c, "找不到可执行的 mpv，请检查路径和文件权限。").FontSize(12).TextColor(c.Theme().Danger).Wrap()
		ui.Text(c, s.mpvPathError).FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
	}
}

func (s *Settings) saveMpvPath() bool {
	p := strings.TrimSpace(s.mpvPath)
	if p != "" {
		if _, err := mpv.ResolveExecutable(p); err != nil {
			s.mpvPathError = err.Error()
			return false
		}
	}
	s.mpvPath, s.mpvPathError = p, ""
	s.set(func(x *config.Config) { x.Render.MpvPath = p })
	return true
}

// ---- 暂停规则 ----

func (s *Settings) pause(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "暂停与电源", "看不见的壁纸不消耗资源，按屏管理暂停与电池策略", nil, func() {
		card(c.Key("auto"), "自动暂停", "", func() {
			settingRow(c, "暂停全部壁纸", "手动暂停，和托盘菜单里的“暂停全部”相同", func() {
				b := v.PausedAll
				if ui.Switch(c.Key("all"), &b).Label(tr(c, "暂停全部壁纸")).Changed() {
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
					ui.Switch(c.Key(r.k), &b).Label(tr(c, r.title)).Disabled(true)
				})
			}
		})
		card(c.Key("resume"), "恢复", "", func() {
			settingRow(c, "恢复前等待", "条件解除后再等一会儿才恢复播放，避免切换窗口时反复启停（毫秒）", func() {
				f := float64(cfg.Pause.ResumeDebounceMs)
				if ui.NumberInput(c.Key("deb"), &f, 100, 5000, 100).Label(tr(c, "恢复前等待")).Changed() {
					n := int(f)
					s.set(func(x *config.Config) { x.Pause.ResumeDebounceMs = n })
				}
			})
		})
		s.appRules(c, v)
		s.powerSections(c.Key("power"), v)
		card(c.Key("how"), "暂停是如何分级的", "", func() {
			collapsible(c.Key("levels"), "展开说明（07 章状态机）", &s.levelsOpen, func() {
				for _, l := range []string{
					"L1 暂停：停止解码和呈现，画面停在当前帧，恢复几乎无延迟。",
					"L3 释放：暂停超过一段时间后释放解码器和显存，恢复时从原位置重新加载。",
					"L4 截图：电量过低或长时间不可见时结束渲染进程，用首帧截图作为系统壁纸，零资源占用。",
					"限帧（15 fps）：使用电池（“降到 15 fps”策略）、设备过热或省电档位时启用，不暂停。",
				} {
					text(c, "• "+tr(c, l)).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(3, 0)
				}
			})
		})
	})
}

// ---- 电源 ----

func (s *Settings) powerSections(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	ui.Row(c.Key("now")).Gap(12).Children(func() {
		stat := func(k, label, val string, col ui.Color) {
			ui.Column(c.Key(k)).Grow(1).Basis(0).Padding(14, 16).Gap(4).Radius(12).Background(cardBg(t)).Border(1, t.Border).Children(func() {
				text(c, label).FontSize(12).TextColor(t.TextMuted)
				text(c, val).Bold().FontSize(16).TextColor(col)
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
		th, thc := "未报告过热", t.TextMuted
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
				{"fps15", "降到 15 fps", "保留动态效果，降低帧率；实际功耗取决于素材和后端"},
				{"ignore", "继续正常播放", "和插电时一样，耗电最多"},
			} {
				o := o
				ui.Row(c.Key("r"+o.id)).Gap(10).Padding(6, 0).AlignItems(ui.Center).Children(func() {
					if radio(c.Key("rb"), &pol, o.id, o.title).Changed() {
						id := o.id
						s.set(func(x *config.Config) { x.Pause.BatteryPolicy = id })
					}
					text(c, o.desc).FontSize(12).TextColor(t.TextMuted)
				})
			}
		})
		settingRow(c, "低电量时停止渲染", fmt.Sprintf(tr(c, "电量低于 %d%% 时结束渲染进程、显示截图（L4）"), cfg.Pause.LowBatteryPct), func() {
			f := float64(cfg.Pause.LowBatteryPct)
			if ui.StepSlider(c.Key("low"), &f, 0, 50, 5).Width(180).Label(tr(c, "低电量阈值")).Changed() {
				n := int(f)
				s.set(func(x *config.Config) { x.Pause.LowBatteryPct = n })
			}
			textf(c, "%d%%", cfg.Pause.LowBatteryPct).FontSize(13).Width(40)
		})
	})
	card(c.Key("sys"), "系统状态联动", "", func() {
		for _, r := range []struct{ k, title, desc string }{
			{"saver", "系统省电模式开启时暂停", "Windows 节电模式 / macOS 低电量模式"},
			{"hot", "设备过热时降到 15 fps", "macOS thermalState 为“严重”时限帧，“危急”时暂停"},
		} {
			settingRow(c, r.title, tr(c, r.desc)+" · "+tr(c, "始终启用"), func() {
				b := true
				ui.Switch(c.Key(r.k), &b).Label(tr(c, r.title)).Disabled(true)
			})
		}
	})
}

// ---- 外观 ----

func (s *Settings) look(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "通用", "外观、语言与启动行为", nil, func() {
		card(c.Key("theme"), "主题", "", func() {
			settingRow(c, "颜色模式", "", func() {
				i := map[string]int{"system": 0, "light": 1, "dark": 2}[cfg.UI.Theme]
				if segmented(c.Key("mode"), &i, "跟随系统", "浅色", "深色").Label(tr(c, "颜色模式")).Changed() {
					m := []string{"system", "light", "dark"}[i]
					s.set(func(x *config.Config) { x.UI.Theme = m })
				}
			})
			settingRow(c, "强调色", "", func() {
				ui.Row(c.Key("acc")).Gap(8).Children(func() {
					for _, a := range accents {
						a := a
						sw := ui.Box(c.Key("a"+a.id)).Size(24, 24).Radius(12).Background(ui.Hex(a.hex)).Cursor(ui.CursorPointer).Label(tr(c, a.name)).Tooltip(tr(c, a.name))
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
				if cfg.UI.Lang == "en" {
					l = "English"
				}
				if selectControl(c.Key("lsel"), &l, []string{"简体中文", "English"}).Label(tr(c, "界面语言")).Width(160).Changed() {
					lang := "zh-CN"
					if l == "English" {
						lang = "en"
					}
					s.set(func(x *config.Config) { x.UI.Lang = lang })
				}
			})
			settingRow(c, "壁纸库默认视图", "", func() {
				i := 0
				if cfg.UI.LibraryView == "list" {
					i = 1
				}
				if segmented(c.Key("lv"), &i, "网格", "列表").Label(tr(c, "壁纸库视图")).Changed() {
					lv := []string{"grid", "list"}[i]
					s.set(func(x *config.Config) { x.UI.LibraryView = lv })
				}
			})
			settingRow(c, "导入后", "默认只加入壁纸库，不会改动任何显示器；也可以自动应用到当前显示器（显示器页里选中的那块，没选时为主屏）", func() {
				j := 0
				if cfg.UI.ImportApply == "current" {
					j = 1
				}
				if segmented(c.Key("impapply"), &j, "只加入壁纸库", "应用到当前显示器").Label(tr(c, "导入后")).Changed() {
					ia := []string{"none", "current"}[j]
					s.set(func(x *config.Config) { x.UI.ImportApply = ia })
				}
			})
		})
		card(c.Key("boot"), "启动", "", func() {
			settingRow(c, "开机自动启动", "Windows：当前用户的 Run 项；macOS：登录项。默认关闭（F-10）", func() {
				b := cfg.Startup.LaunchAtLogin
				if ui.Switch(c.Key("login"), &b).Label(tr(c, "开机自动启动 开关")).Disabled(s.SetLaunchAtLogin == nil).Changed() {
					s.set(func(x *config.Config) { x.Startup.LaunchAtLogin = b; x.Startup.Asked = true })
				}
			})
			s.switchRow(c, "open", "启动时打开设置窗口", "关闭后仅在托盘运行；首次运行和安全模式仍会显示窗口", cfg.Startup.OpenSettings, func(x *config.Config, b bool) { x.Startup.OpenSettings = b })
			settingRow(c, "关闭窗口后继续运行", "关闭设置窗口不会退出壁纸，退出请使用托盘菜单", func() { chip(c, "始终启用", t.TextMuted) })
		})

		s.storageCard(c, v)

	})
}

// ---- 启动与更新 ----

func (s *Settings) startup(c *ui.Context, v app.View) {
	t := c.Theme()
	cfg := v.Config
	page(c, "版本管理", "签名验证、增量更新与本地回滚", nil, func() {
		card(c.Key("upd"), "更新", "增量更新：只下载变化的文件；签名校验失败或新版本连续启动失败会自动回滚", func() {
			s.mu.Lock()
			u := s.upd
			s.mu.Unlock()
			settingRow(c, "当前版本", s.Info.Platform, func() {
				text(c, s.Info.Version).Bold().FontSize(13)
			})
			s.switchRow(c, "auto", "自动检查更新", "启动后检查一次；只下载清单，不上传任何信息", cfg.Update.AutoCheck, func(x *config.Config, b bool) { x.Update.AutoCheck = b })
			if !s.feedInit {
				s.feed, s.feedInit = cfg.Update.FeedURL, true
			}
			settingRow(c, "更新源", "HTTPS 目录地址，包含平台清单与签名；留空时不会联网", func() {
				ui.TextInput(c.Key("feed"), &s.feed).Placeholder(tr(c, "https://example.com/wallpaper/updates")).Width(280)
				if button(c.Key("feedsave"), "保存").Disabled(s.feed == cfg.Update.FeedURL || u.busy).Clicked() {
					f := strings.TrimSpace(s.feed)
					if err := validateUpdateFeed(f); err != nil {
						s.setUpd(func(u *updState) { u.msg = err.Error() })
					} else {
						s.set(func(x *config.Config) { x.Update.FeedURL = f })
						s.setUpd(func(u *updState) { u.info = nil; u.msg = "更新源已保存，尚未检查" })
					}
				}
			})
			ui.Row(c.Key("updacts")).Gap(8).Padding(10, 0, 4, 0).AlignItems(ui.Center).Children(func() {
				if u.info == nil {
					if iconButton(c.Key("check"), icRefresh, "检查更新", true).Disabled(u.busy || s.CheckUpdate == nil || !s.Info.UpdateKey || cfg.Update.FeedURL == "").Clicked() {
						s.checkUpdate()
					}
				} else if iconButton(c.Key("install"), icDownload, "下载并安装 "+u.info.Version, true).Disabled(u.busy || s.ApplyUpdate == nil).Clicked() {
					s.applyUpdate()
				}
				if s.CanRollback != nil {
					if prev, ok := s.CanRollback(); ok {
						if button(c.Key("rb"), "回滚到 "+prev).Disabled(u.busy || s.Rollback == nil).Clicked() {
							s.rollbackAsk, s.rollbackVersion = true, prev
						}
					}
				}
				if u.busy {
					ui.Spinner(c).Size(14, 14)
				}
				if u.msg != "" {
					text(c, u.msg).FontSize(12).TextColor(t.TextMuted).Wrap().Grow(1)
				}
			})
			if u.busy && u.progress > 0 {
				ui.Progress(c.Key("prog"), u.progress)
			}
			if u.info != nil && u.info.Notes != "" {
				text(c, "更新说明").Bold().FontSize(13).Padding(10, 0, 4, 0)
				ui.Text(c, u.info.Notes).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(0, 0, 8, 0)
			}
			if cfg.Update.FeedURL == "" {
				text(c, "尚未配置更新源。未检查更新，版本状态未知。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(4, 0)
			}
			if !s.Info.UpdateKey {
				text(c, "这个构建没有内置更新公钥，无法验证更新包，因此不能在线更新。正式发布版会在构建时写入公钥。").FontSize(12).TextColor(t.Warning).Wrap().Padding(4, 0)
			}
			if !s.Info.SignedBuild {
				text(c, "这个构建未做代码签名：Windows 可能弹出 SmartScreen 提示，macOS 需要右键“打开”。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(4, 0, 8, 0)
			}
		})
		if alertDialog(c.Key("rollbackConfirm"), &s.rollbackAsk, "回滚到 "+s.rollbackVersion+"？", "应用将退出并重启为本地保存的旧版本。壁纸库不会被删除。", "取消", "回滚并重启") == 1 {
			s.rollback()
		}

	})
}

func (s *Settings) setUpd(fn func(u *updState)) {
	s.mu.Lock()
	fn(&s.upd)
	s.mu.Unlock()
	s.invalidate()
}

func (s *Settings) checkUpdate() {
	if s.CheckUpdate == nil {
		return
	}
	s.mu.Lock()
	if s.upd.busy {
		s.mu.Unlock()
		return
	}
	s.upd.busy, s.upd.msg, s.upd.info, s.upd.progress = true, "正在检查…", nil, 0
	s.mu.Unlock()
	s.invalidate()
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
	if s.ApplyUpdate == nil {
		return
	}
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
				u.msg = "更新失败：" + err.Error()
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
	if s.Rollback == nil {
		return
	}
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

func (s *Settings) diagSections(c *ui.Context, v app.View) {
	t := c.Theme()
	card(c.Key("export"), "诊断包", "", func() {
		ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			s.mu.Lock()
			busy, path := s.diagBusy, s.diagPath
			s.mu.Unlock()
			if iconButton(c.Key("diagbtn"), icDownload, "导出诊断包", true).Disabled(s.ExportDiag == nil || busy).Clicked() {
				s.exportDiagnostics()
			}
			if busy {
				ui.Spinner(c).Size(14, 14)
			}
			if path != "" && s.ShowInFolder != nil {
				if button(c.Key("revealDiag"), "显示诊断包").Clicked() {
					s.ShowInFolder(path)
				}
			}
			if iconButton(c.Key("logs"), icFolder, "打开日志目录", false).Disabled(s.OpenPath == nil || s.Info.LogsDir == "").Clicked() {
				if err := s.OpenPath(s.Info.LogsDir); err != nil {
					s.mu.Lock()
					s.diagMsg = err.Error()
					s.mu.Unlock()
				}
			}
		})
		s.mu.Lock()
		message := s.diagMsg
		s.mu.Unlock()
		if message != "" {
			text(c, message).FontSize(12).TextColor(t.TextMuted).Wrap().Padding(0, 0, 8, 0)
		}
	})
	card(c.Key("rt"), "运行状态", "每块屏当前的级别、后端、解码状态与丢帧数", func() {
		for _, d := range v.Displays {
			d := d
			settingRow(c.Key("rt"+d.ID), dispLabel(c, d), reasonsText(c, d.Reasons), func() {
				ui.Row(c).Gap(6).Children(func() {
					chip(c, d.Level+" "+tr(c, levelNames[d.Level]), t.Accent)
					if d.Backend != "" {
						chip(c, d.Backend, t.TextMuted)
					}
					hardwareBadge(c, d)
					if d.FPSCap > 0 {
						chip(c, fmt.Sprintf("%d fps", d.FPSCap), t.Warning)
					}
				})
			})
			if d.Backend != "" && d.Backend != "image" {
				textf(c, "当前渲染会话丢帧 %d", d.Dropped).FontSize(11).TextColor(t.TextMuted)
			}
		}
		settingRow(c, "安全模式", "连续异常退出后自动进入，壁纸不启动", func() {
			if v.SafeMode {
				chip(c, "已进入", t.Warning)
			} else {
				chip(c, "未进入", t.Success)
			}
		})
	})
	s.metricsCard(c, v)
	card(c.Key("priv"), "隐私", "", func() {
		text(c, "不收集任何遥测数据（D-15），没有崩溃自动上报。日志只保存在本机，诊断包只有你手动导出并发送时才会离开这台电脑。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(8, 0)
	})
	card(c.Key("reset"), "重置", "", func() {
		settingRow(c, "恢复默认设置", "不影响壁纸库和显示器分配", func() {
			if button(c.Key("resetbtn"), "恢复默认").Clicked() {
				s.resetAsk = true
			}
		})
	})
	if alertDialog(c.Key("resetdlg"), &s.resetAsk, "恢复默认设置？", "暂停规则、电源策略、性能档位和外观会恢复为默认值。", "取消", "恢复默认") == 1 {
		s.set(func(x *config.Config) {
			keep := x.Startup
			upd := x.Update
			r := x.Render
			*x = config.Defaults()
			x.Startup, x.Update = keep, upd
			x.Render.MpvPath, x.Render.FFmpegPath = r.MpvPath, r.FFmpegPath
		})
		c.Toast(tr(c, "已恢复默认设置"))
	}
}

// ---- 关于 ----

func (s *Settings) about(c *ui.Context, v app.View) {
	t := c.Theme()
	page(c, "关于与诊断", "版本信息、运行状态与本地诊断", nil, func() {
		ui.Row(c.Key("hero")).Gap(16).Padding(20).Radius(14).Background(cardBg(t)).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(64, 64).Radius(16).Gradient(t.Accent, ui.Hex("#22d3ee"), 135).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Icon(c, icImage).FontSize(30).TextColor(ui.Hex("#ffffff"))
			})
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				text(c, "动态壁纸").Bold().FontSize(20)
				textf(c, "版本 %s · %s", s.Info.Version, s.Info.Platform).FontSize(13).TextColor(t.TextMuted)
				text(c, "性能优先的跨平台动态壁纸。纯 Go 编写（CGO_ENABLED=0），原生窗口渲染，不用 WebView。").FontSize(12).TextColor(t.TextMuted).Wrap()
			})
		})
		s.diagSections(c.Key("diagnostics"), v)
		s.capabilityCard(c, v)
		card(c.Key("lic"), "开源组件与许可", "", func() {
			for _, l := range []struct{ name, desc string }{
				{"MyGo", "原生 UI 与托盘（egoist/mygo）"},
				{"purego", "无 cgo 调用系统 API（ebitengine/purego，Apache-2.0）"},
				{"mpv", "Windows / Linux 视频播放，作为独立进程调用；本安装包不捆绑 mpv，由用户自行安装（许可证取决于所选构建）"},
				{"编码器", "不捆绑第三方编码器；macOS 调用系统 AVFoundation，ffmpeg 由用户自行提供"},
			} {
				settingRow(c, l.name, l.desc, func() {})
			}
		})
		card(c.Key("data"), "数据位置", "", func() {
			settingRow(c, "配置与壁纸库", s.Info.DataDir, func() {
				if button(c.Key("opendata"), "打开").Disabled(s.OpenPath == nil || s.Info.DataDir == "").Clicked() {
					if err := s.OpenPath(s.Info.DataDir); err != nil {
						c.Toast(err.Error())
					}
				}
			})
		})
	})
}

func (s *Settings) metricsCard(c *ui.Context, v app.View) {
	t := c.Theme()
	card(c.Key("metrics"), "本机统计", "仅统计动态壁纸运行期间的 L0–L4 时长，不估算耗电或节电量。", func() {
		if len(v.Displays) == 0 {
			text(c, "没有检测到显示器。").FontSize(12).TextColor(t.TextMuted).Padding(8, 0)
		}
		for _, d := range v.Displays {
			settingRow(c.Key("metric"+d.ID), dispLabel(c, d), "本周暂停时长", func() {
				ui.Text(c, metricDuration(c.Locale(), d.Metrics.Paused)).Bold().TextColor(t.Accent)
			})
			metricPeriod(c, d.Metrics)
			ui.Row(c.Key("levels"+d.ID)).Wrap().Gap(10).Padding(4, 0, 8, 0).Children(func() {
				for level, duration := range d.Metrics.Levels {
					ui.Text(c, fmt.Sprintf("L%d · %s", level, metricDuration(c.Locale(), duration))).FontSize(11).TextColor(t.TextMuted)
				}
			})
		}
		text(c, "只保存在本机；每周一 00:00 UTC 重置。应用退出、系统睡眠、断开显示器、静态图片和恢复中的时间不计入。采样间隔超过 45 秒时整段跳过。").FontSize(11).TextColor(t.TextMuted).Wrap().Padding(6, 0)
	})
}
