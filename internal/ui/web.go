package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/wpk"
)

func (s *Settings) networkDialog(c *ui.Context, v app.View) {
	if v.NetworkConsent == nil {
		return
	}
	open := true
	body := v.NetworkConsent.Title + "\n" + tr(c, "该壁纸请求联网。默认拒绝；允许后可在属性中撤销。") + "\n" + strings.Join(v.NetworkConsent.Hosts, ", ")
	// MyGo focuses the last alert button; Enter must deny, never grant.
	switch alertDialog(c.Key("network-consent"), &open, "允许壁纸联网？", body, "允许联网", "拒绝联网") {
	case 0:
		s.ctl.ResolveNetworkConsent(true)
	case 1:
		s.ctl.ResolveNetworkConsent(false)
	default:
		if !open {
			s.ctl.ResolveNetworkConsent(false)
		}
	}
}
func (s *Settings) webProps(c *ui.Context, v app.View, it config.Item) {
	sectionHeader(c, "propshdr", tr(c, "属性"), nil)
	values, err := wpk.PropertyValues(it.PropertyDefs, it.Props.WebValues)
	if err != nil {
		values, _ = wpk.PropertyValues(it.PropertyDefs, "")
	}
	set := func(k string, x any) {
		values[k] = x
		b, e := json.Marshal(values)
		if e != nil {
			return
		}
		p := it.Props
		p.WebValues = string(b)
		s.ctl.SetItemProps(it.ID, p)
	}
	group(c.Key("web-properties"), func() {
		propRow(c, "network", "网络访问", "", len(it.PropertyDefs) == 0, func() {
			if it.NetworkGranted {
				if button(c.Key("revoke"), "撤销联网权限").Clicked() {
					s.ctl.RevokeNetwork(it.ID)
				}
			} else {
				text(c, "已拒绝联网")
			}
		})
		for i, p := range it.PropertyDefs {
			label := p.Label.Pick(v.Config.UI.Lang)
			if label == "" {
				label = p.Key
			}
			propRow(c, "web-"+p.Key, label, "", i == len(it.PropertyDefs)-1, func() {
				switch p.Type {
				case "bool":
					x, _ := values[p.Key].(bool)
					if ui.Switch(c.Key(p.Key), &x).Label(label + " " + tr(c, "开关")).Changed() {
						set(p.Key, x)
					}
				case "slider":
					x, _ := values[p.Key].(float64)
					if ui.Slider(c.Key(p.Key), &x, *p.Min, *p.Max).Label(label).Width(130).Changed() {
						if p.Step != nil {
							x = *p.Min + math.Round((x-*p.Min) / *p.Step)**p.Step
						}
						set(p.Key, max(*p.Min, min(*p.Max, x)))
					}
				case "select":
					opts := p.OptionValues()
					labels := make([]string, len(opts))
					selected := ""
					for j, o := range opts {
						var x any
						_ = json.Unmarshal(o, &x)
						labels[j] = fmt.Sprint(x)
						var named struct {
							Label wpk.I18n `json:"label"`
						}
						_ = json.Unmarshal(p.Options[j], &named)
						if n := named.Label.Pick(v.Config.UI.Lang); n != "" {
							labels[j] = n
						}
						b, _ := json.Marshal(values[p.Key])
						if string(b) == string(o) {
							selected = labels[j]
						}
					}
					if selectControl(c.Key(p.Key), &selected, labels).Label(label).Width(150).Changed() {
						for j, l := range labels {
							if l == selected {
								var x any
								_ = json.Unmarshal(opts[j], &x)
								set(p.Key, x)
								break
							}
						}
					}
				default:
					saved, _ := values[p.Key].(string)
					key := it.ID + ":" + p.Key
					if s.webDrafts == nil {
						s.webDrafts = map[string]*string{}
						s.webDraftBase = map[string]string{}
					}
					if s.webDrafts[key] == nil || s.webDraftBase[key] != saved {
						draft := saved
						s.webDrafts[key] = &draft
						s.webDraftBase[key] = saved
					}
					draft := s.webDrafts[key]
					if ui.TextInput(c.Key(p.Key), draft).Placeholder(label).Label(label + " " + tr(c, "属性值")).Width(150).Changed() {
						b, _ := json.Marshal(*draft)
						if p.ValidateValue(b) == nil {
							set(p.Key, *draft)
							s.webDraftBase[key] = *draft
						}
					}
				}
			})
		}
	})
}
