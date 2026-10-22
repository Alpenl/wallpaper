package ui

import (
	"github.com/egoist/mygo/ui"
	"testing"
	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/ui/uidemo"
)

func TestSignalConsentUI(t *testing.T) {
	for _, permission := range []string{"audio", "input"} {
		for _, key := range []ui.Key{ui.KeyEnter, ui.KeyEscape} {
			ctl := uidemo.New(nil)
			ctl.V.SignalConsent = &app.SignalConsent{Title: "test", Permission: permission}
			s := NewSettings(ctl, "darwin")
			tt := ui.NewTester(func(c *ui.Context) { s.signalDialog(c, ctl.View()) }, 700, 450)
			tt.Frame()
			tt.Key(0, key)
			tt.Frame()
			if len(ctl.SignalAnswers) != 1 || ctl.SignalAnswers[0] {
				t.Fatal(permission, key, ctl.SignalAnswers)
			}
		}
		ctl := uidemo.New(nil)
		ctl.V.SignalConsent = &app.SignalConsent{Title: "test", Permission: permission}
		s := NewSettings(ctl, "windows")
		tt := ui.NewTester(func(c *ui.Context) { s.signalDialog(c, ctl.View()) }, 700, 450)
		tt.Frame()
		click(t, tt, "允许")
		if len(ctl.SignalAnswers) != 1 || !ctl.SignalAnswers[0] {
			t.Fatal("explicit consent")
		}
	}
}
func TestSignalSwitchesAndRevocation(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	if ctl.View().Config.Privacy.AudioReactive || ctl.View().Config.Privacy.MouseInput {
		t.Fatal("defaults")
	}
	click(t, tt, "设置")
	click(t, tt, "通用")
	click(t, tt, "音频响应 开关")
	click(t, tt, "鼠标交互 开关")
	if !ctl.View().Config.Privacy.AudioReactive || !ctl.View().Config.Privacy.MouseInput {
		t.Fatal("switches")
	}
	ctl.V.Library = []config.Item{{ID: "signal", Type: "shader", Permissions: []string{"audio", "input"}, AudioGranted: true, InputGranted: true}}
	s := NewSettings(ctl, "windows")
	props := ui.NewTester(func(c *ui.Context) { s.webProps(c, ctl.View(), ctl.View().Library[0]) }, 700, 600)
	props.Frame()
	click(t, props, "撤销音频权限")
	if ctl.View().Library[0].AudioGranted || !ctl.View().Library[0].InputGranted {
		t.Fatal("independent audio revoke")
	}
	click(t, props, "撤销鼠标权限")
	if ctl.View().Library[0].InputGranted {
		t.Fatal("input revoke")
	}
}
