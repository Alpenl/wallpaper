package config

import "encoding/json"

// A schema upgrade never grants network access, even if old unknown fields did.
var libraryMigrations = map[int]Migration{3: func(raw map[string]any) error {
	items, _ := raw["items"].([]any)
	for _, v := range items {
		if it, ok := v.(map[string]any); ok {
			it["networkGranted"] = false
		}
	}
	return nil
}}

// Clone detaches the nested manifest metadata shared by library snapshots.
func (it Item) Clone() Item {
	it.Permissions = append([]string(nil), it.Permissions...)
	it.Tags = append([]string(nil), it.Tags...)
	it.Colors = append([]string(nil), it.Colors...)
	if it.Web != nil {
		web := *it.Web
		web.Permissions = append([]string(nil), web.Permissions...)
		web.NetworkAllow = append([]string(nil), web.NetworkAllow...)
		it.Web = &web
	}
	if it.PropertyDefs != nil {
		b, _ := json.Marshal(it.PropertyDefs)
		it.PropertyDefs = nil
		_ = json.Unmarshal(b, &it.PropertyDefs)
	}
	return it
}
