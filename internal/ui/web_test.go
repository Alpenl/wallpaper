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

func TestShaderNativePropertyPanelAndFilters(t *testing.T) {
	ctl := uidemo.New(nil)
	s := NewSettings(ctl, "darwin")
	s.SelectItem("shader-waves")
	tt := ui.NewTester(s.View, 1120, 800)
	tt.Frame()
	must(t, tt, "渲染比例", "帧率上限", "筛选与显示")
	s.filter = 5
	if got := s.filtered(ctl.View()); len(got) != 1 || got[0].Type != "shader" {
		t.Fatal(got)
	}
	s.filter = 4
	if got := s.filtered(ctl.View()); len(got) != 1 || got[0].Type != "web" {
		t.Fatal(got)
	}
}

func TestWebSliderNilAndInvalidBoundsDoNotPanic(t *testing.T) {
	f0, f5, f10, f100 := 0.0, 5.0, 10.0, 100.0
	ctl := uidemo.New(nil)
	s := NewSettings(ctl, "darwin")
	it := config.Item{
		ID:   "shader-malformed",
		Type: "shader",
		PropertyDefs: []wpk.Prop{
			{Key: "nilAll", Type: "slider", Default: json.RawMessage(`0.5`), Label: wpk.I18n{"en": "Nil All"}},
			{Key: "nilMax", Type: "slider", Min: &f10, Default: json.RawMessage(`10.5`), Label: wpk.I18n{"en": "Nil Max"}},
			{Key: "zeroStep", Type: "slider", Min: &f0, Max: &f100, Step: &f0, Default: json.RawMessage(`50`), Label: wpk.I18n{"en": "Zero Step"}},
			{Key: "inverted", Type: "slider", Min: &f10, Max: &f5, Default: json.RawMessage(`7`), Label: wpk.I18n{"en": "Inverted"}},
		},
	}
	ctl.V.Library = []config.Item{it}
	tt := ui.NewTester(func(c *ui.Context) { s.webProps(c, ctl.View(), ctl.View().Library[0]) }, 700, 600)
	tt.Frame()
	must(t, tt, "Nil All", "Nil Max", "Zero Step", "Inverted")
}

func TestWebSelectNormalizedJSONEquality(t *testing.T) {
	if !jsonValuesEqual(float64(1), json.RawMessage(`1.0`)) {
		t.Fatal("1 must equal 1.0")
	}
	if !jsonValuesEqual(1, json.RawMessage(` 1.00 `)) {
		t.Fatal("int 1 must equal 1.00")
	}
	if !jsonValuesEqual("dark", json.RawMessage(` "dark" `)) {
		t.Fatal("whitespace-padded JSON string must match")
	}
	if jsonValuesEqual(1, json.RawMessage(`"1"`)) {
		t.Fatal("number 1 must not equal string \"1\"")
	}
	if jsonValuesEqual(1, json.RawMessage(`2.0`)) {
		t.Fatal("1 must not equal 2.0")
	}

	ctl := uidemo.New(nil)
	s := NewSettings(ctl, "darwin")
	it := config.Item{
		ID:   "web-select",
		Type: "web",
		Props: config.ItemProps{
			WebValues: `{"choice":1}`,
		},
		PropertyDefs: []wpk.Prop{
			{
				Key:     "choice",
				Type:    "select",
				Default: json.RawMessage(`1.0`),
				Label:   wpk.I18n{"en": "Choice"},
				Options: []json.RawMessage{
					json.RawMessage(`{"value": 1.0, "label": {"en": "One"}}`),
					json.RawMessage(`{"value": 2.0, "label": {"en": "Two"}}`),
				},
			},
		},
	}
	ctl.V.Library = []config.Item{it}
	ctl.V.Config.UI.Lang = "en"
	tt := ui.NewTester(func(c *ui.Context) { s.webProps(c, ctl.View(), ctl.View().Library[0]) }, 700, 450)
	tt.SetLocale("en")
	tt.Frame()
	must(t, tt, "Choice", "One")
}
