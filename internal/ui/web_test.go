package ui

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/ui/uidemo"
	"wpapp/internal/wpk"
)

func TestWebConsentAndRevokeUI(t *testing.T) {
	ctl := uidemo.New(nil)
	s := NewSettings(ctl, "darwin")
	ctl.V.NetworkConsent = &app.NetworkConsent{Title: "Web test", Hosts: []string{"api.example.com"}}
	tt := ui.NewTester(func(c *ui.Context) { s.networkDialog(c, ctl.View()) }, 700, 450)
	tt.Frame()
	must(t, tt, "允许壁纸联网？", "拒绝联网", "允许联网")
	click(t, tt, "拒绝联网")
	if ctl.View().NetworkConsent != nil {
		t.Fatal("dialog still pending")
	}
	it := config.Item{ID: "web", Type: "web", NetworkGranted: true, PropertyDefs: []wpk.Prop{{Key: "clock", Type: "bool", Default: json.RawMessage(`true`), Label: wpk.I18n{"en": "Show clock"}}}}
	ctl.V.Library = []config.Item{it}
	props := ui.NewTester(func(c *ui.Context) { s.webProps(c, ctl.View(), ctl.View().Library[0]) }, 700, 450)
	props.Frame()
	must(t, props, "撤销联网权限", "Show clock")
	click(t, props, "撤销联网权限")
	if ctl.View().Library[0].NetworkGranted {
		t.Fatal("permission not revoked")
	}
	click(t, props, "Show clock 开关")
	if ctl.View().Library[0].Props.WebValues != `{"clock":false}` {
		t.Fatal(ctl.View().Library[0].Props)
	}
}

func TestNetworkDialogDefaultDeny(t *testing.T) {
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeyEscape} {
		ctl := uidemo.New(nil)
		ctl.V.NetworkConsent = &app.NetworkConsent{Title: "test"}
		s := NewSettings(ctl, "darwin")
		tt := ui.NewTester(func(c *ui.Context) { s.networkDialog(c, ctl.View()) }, 700, 450)
		tt.Frame()
		tt.Key(0, key)
		tt.Frame()
		if len(ctl.NetworkAnswers) != 1 || ctl.NetworkAnswers[0] {
			t.Fatalf("key %v granted or did not resolve: %v", key, ctl.NetworkAnswers)
		}
	}
	ctl := uidemo.New(nil)
	ctl.V.NetworkConsent = &app.NetworkConsent{Title: "test"}
	s := NewSettings(ctl, "darwin")
	tt := ui.NewTester(func(c *ui.Context) { s.networkDialog(c, ctl.View()) }, 700, 450)
	tt.Frame()
	click(t, tt, "允许联网")
	if len(ctl.NetworkAnswers) != 1 || !ctl.NetworkAnswers[0] {
		t.Fatal("explicit consent lost")
	}
}
