package ui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
	"wpapp/internal/app"
	"wpapp/internal/config"
)

func (s *Settings) switchRowDisabled(c *ui.Context, key, title, desc string, on, disabled bool, set func(*config.Config, bool)) {
	settingRow(c, title, desc, func() {
		b := on
		if ui.Switch(c.Key(key), &b).Label(tr(c, title+" 开关")).Disabled(disabled).Changed() {
			s.set(func(x *config.Config) { set(x, b) })
		}
	})
}

func (s *Settings) mpvControls(v app.View) bool {
	return s.GOOS != "darwin" && (s.GOOS != "windows" || v.Config.Render.WindowsBackend != "mf")
}

func (s *Settings) importFolder() {
	if s.PickFolder != nil {
		if path := s.PickFolder(); path != "" {
			s.importPath(path)
		}
	}
}

func (s *Settings) appRules(c *ui.Context, v app.View) {
	t := c.Theme()
	card(c.Key("apps"), "应用规则", "仅匹配前台程序的可执行文件名，不读取窗口标题。忽略规则不会覆盖锁屏、电源或手动暂停。", func() {
		if !v.Capabilities.ForegroundApp {
			text(c, "当前桌面会话无法读取前台程序，规则暂不生效。已保存的规则仍会保留。").FontSize(12).TextColor(t.Warning).Wrap().Padding(8, 0)
		}
		for _, rule := range v.Config.Pause.AppRules {
			rule := rule
			settingRow(c.Key("app"+rule.Executable), rule.Executable, "", func() {
				action := map[string]string{"pause": "暂停", "saver": "省电档", "ignore": "忽略全屏"}[rule.Action]
				if selectControl(c.Key("action"), &action, []string{"暂停", "省电档", "忽略全屏"}).Label(tr(c, "规则动作 "+rule.Executable)).Width(130).Changed() {
					id := map[string]string{"暂停": "pause", "省电档": "saver", "忽略全屏": "ignore"}[action]
					s.updateAppRule(rule.Executable, id)
				}
				if button(c.Key("remove"), "删除规则").Label(tr(c, "删除规则 "+rule.Executable)).Clicked() {
					s.removeAppRule(rule.Executable)
				}
			})
		}
		if len(v.Config.Pause.AppRules) == 0 {
			text(c, "还没有规则").FontSize(12).TextColor(t.TextMuted).Padding(8, 0)
		}
		if s.appAction == "" {
			s.appAction = "暂停"
		}
		ui.Row(c.Key("newrule")).Gap(8).Padding(10, 0).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("executable"), &s.appExecutable).Placeholder(tr(c, "例如 game.exe / Safari / firefox")).Label(tr(c, "程序文件名")).Grow(1).MinWidth(120)
			selectControl(c.Key("newaction"), &s.appAction, []string{"暂停", "省电档", "忽略全屏"}).Label(tr(c, "新规则动作")).Width(130)
			if primaryButton(c.Key("addapp"), "添加规则").Disabled(strings.TrimSpace(s.appExecutable) == "").Clicked() {
				if s.addAppRule() {
					c.Toast(tr(c, "规则已提交"))
				}
			}
		})
		if s.appError != "" {
			text(c, s.appError).FontSize(12).TextColor(t.Danger).Wrap()
		}
	})
}

func (s *Settings) addAppRule() bool {
	exe := strings.TrimSpace(s.appExecutable)
	if exe == "" || len(exe) > 256 || strings.ContainsAny(exe, "/\\\r\n\x00") || exe == "." || exe == ".." {
		s.appError = "请输入可执行文件名，不是完整路径或窗口标题。"
		return false
	}
	rules := s.ctl.View().Config.Pause.AppRules
	if len(rules) >= 64 {
		found := false
		for _, r := range rules {
			if s.sameExecutable(r.Executable, exe) {
				found = true
				break
			}
		}
		if !found {
			s.appError = "最多保存 64 条应用规则，请先删除不再使用的规则。"
			return false
		}
	}
	action := map[string]string{"暂停": "pause", "省电档": "saver", "忽略全屏": "ignore"}[s.appAction]
	if action == "" {
		action = "pause"
	}
	s.set(func(x *config.Config) {
		for i := range x.Pause.AppRules {
			if s.sameExecutable(x.Pause.AppRules[i].Executable, exe) {
				x.Pause.AppRules[i].Action = action
				return
			}
		}
		x.Pause.AppRules = append(x.Pause.AppRules, config.AppRule{Executable: exe, Action: action})
	})
	s.appExecutable, s.appError = "", ""
	return true
}

func (s *Settings) capabilityCard(c *ui.Context, v app.View) {
	t := c.Theme()
	card(c.Key("capabilities"), "能力与边界", "只展示当前构建的真实能力，不模拟下载、统计或未实现的渲染器。", func() {
		for _, item := range []struct{ title, description string }{
			{"本地壁纸", "视频、图片、GIF，以及受支持的 .wpk、Lively 视频包、Wallpaper Engine 视频目录。普通文件夹可批量导入。"},
			{"网页与 Shader", "当前构建没有网页沙箱或 Shader 渲染管线，不能导入或播放这两类壁纸。"},
			{"在线创意工坊", "尚未接入内容服务或账号授权。请下载已有本地视频素材后导入。"},
			{"跨屏拼接", "Windows 与 macOS 支持按真实布局裁切跨屏画布，保留按屏暂停；各屏视频不保证逐帧同步。Linux 当前不支持多输出跨屏。"},
			{"版本历史与本机统计", "版本页显示真实的本地回滚版本；显示器和诊断页提供本周暂停时长，不估算节电量。"},
		} {
			settingRow(c, item.title, item.description, func() {})
		}
		text(c, "平台能力由当前桌面会话和渲染后端决定；不可用控件会标明原因。").FontSize(12).TextColor(t.TextMuted).Wrap().Padding(8, 0)
	})
}

func validateUpdateFeed(feed string) error {
	if feed == "" {
		return nil
	}
	u, err := url.Parse(feed)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("更新源必须是无账号、查询参数和片段的 HTTPS 目录地址。")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return fmt.Errorf("更新源必须使用 HTTPS；仅本机测试允许 HTTP。")
	}
	return nil
}

// CheckUpdates checks once without downloading or installing a release.
func (s *Settings) CheckUpdates() { s.checkUpdate() }

func (s *Settings) exportDiagnostics() {
	if s.ExportDiag == nil {
		return
	}
	s.mu.Lock()
	if s.diagBusy {
		s.mu.Unlock()
		return
	}
	s.diagBusy, s.diagMsg, s.diagPath = true, "正在导出诊断包…", ""
	s.mu.Unlock()
	go func() {
		path, err := s.ExportDiag()
		s.mu.Lock()
		s.diagBusy = false
		if err != nil {
			s.diagMsg = "导出失败：" + err.Error()
		} else {
			s.diagPath, s.diagMsg = path, "诊断包已导出。请检查内容后再分享。"
		}
		s.mu.Unlock()
		s.invalidate()
	}()
}

func (s *Settings) storageCard(c *ui.Context, v app.View) {
	t := c.Theme()
	card(c.Key("storage"), "存储", "清理只删除已不再被壁纸库引用的缓存，不删除原始壁纸或仍在使用的预览图。", func() {
		settingRow(c, "壁纸库", humanBytes(v.Storage.LibraryBytes), func() {
			if button(c.Key("refreshStorage"), "重新统计").Disabled(v.Storage.Scanning).Clicked() {
				s.ctl.RefreshStorage()
			}
		})
		settingRow(c, "缓存", humanBytes(v.Storage.CacheBytes), func() {
			if button(c.Key("clearCache"), "清理未使用缓存").Disabled(v.Storage.Scanning).Clicked() {
				s.confirmCache = true
			}
		})
		if v.Storage.Scanning {
			text(c, "正在统计磁盘占用…").TextColor(t.TextMuted).FontSize(12)
		}
		if v.Storage.Error != "" {
			text(c, v.Storage.Error).TextColor(t.Danger).FontSize(12).Wrap()
		}
	})
	if alertDialog(c.Key("clearCacheConfirm"), &s.confirmCache, "清理未使用缓存？", "只删除不再被库引用的缓存文件。壁纸文件和有效预览图将保留。", "取消", "清理") == 1 {
		s.ctl.ClearCache()
	}
}

func (s *Settings) sameExecutable(a, b string) bool {
	return a == b || s.GOOS == "windows" && strings.EqualFold(a, b)
}

// Rule commands resolve stable executable identities when the app consumes the
// queued callback, not stale slice indices from an earlier UI snapshot.
func (s *Settings) updateAppRule(executable, action string) {
	s.set(func(x *config.Config) {
		for i := range x.Pause.AppRules {
			if s.sameExecutable(x.Pause.AppRules[i].Executable, executable) {
				x.Pause.AppRules[i].Action = action
				return
			}
		}
	})
}
func (s *Settings) removeAppRule(executable string) {
	s.set(func(x *config.Config) {
		for i := range x.Pause.AppRules {
			if s.sameExecutable(x.Pause.AppRules[i].Executable, executable) {
				x.Pause.AppRules = append(x.Pause.AppRules[:i], x.Pause.AppRules[i+1:]...)
				return
			}
		}
	})
}
