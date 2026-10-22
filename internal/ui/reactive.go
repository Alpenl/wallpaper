package ui

import (
	"github.com/egoist/mygo/ui"
	"wpapp/internal/app"
	"wpapp/internal/config"
)

func (s *Settings) signalDialog(c *ui.Context, v app.View) {
	if v.SignalConsent == nil {
		return
	}
	open := true
	title, body := "允许音频响应？", "仅在启用总开关且壁纸可见时采集系统音频。原始音频留在渲染进程，可在属性中撤销。"
	if v.SignalConsent.Permission == "input" {
		title, body = "允许鼠标交互？", "仅在启用总开关且指针下的桌面可见时传递鼠标位置和按钮，不读取键盘。"
	}
	switch alertDialog(c.Key("signal-consent"), &open, title, v.SignalConsent.Title+"\n"+tr(c, body), "允许", "拒绝") {
	case 0:
		s.ctl.ResolveSignalConsent(true)
	case 1:
		s.ctl.ResolveSignalConsent(false)
	default:
		if !open {
			s.ctl.ResolveSignalConsent(false)
		}
	}
}
func (s *Settings) signalProps(c *ui.Context, it config.Item) {
	for _, p := range it.Permissions {
		title, revoke, granted := "音频响应", "撤销音频权限", it.AudioGranted
		if p == "input" {
			title, revoke, granted = "鼠标交互", "撤销鼠标权限", it.InputGranted
		}
		if p != "input" && p != "audio" {
			continue
		}
		propRow(c, "signal-"+p, title, "", false, func() {
			if granted {
				if button(c.Key("revoke-"+p), revoke).Clicked() {
					s.ctl.RevokeSignal(it.ID, p)
				}
			} else {
				text(c, "权限未授予")
			}
		})
	}
}
