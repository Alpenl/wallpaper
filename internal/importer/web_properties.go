package importer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"wpapp/internal/wpk"
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func normalizeSelectOptions(rawOpts []json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	for _, rawOpt := range rawOpts {
		var opt struct {
			Value json.RawMessage `json:"value"`
			Label json.RawMessage `json:"label"`
		}
		if err := json.Unmarshal(rawOpt, &opt); err != nil || len(opt.Value) == 0 {
			continue
		}
		entry := map[string]any{"value": opt.Value}
		if len(opt.Label) > 0 {
			var s string
			if json.Unmarshal(opt.Label, &s) == nil {
				entry["label"] = wpk.I18n{"en": trunc(s, 2000)}
			} else {
				var i18n wpk.I18n
				if json.Unmarshal(opt.Label, &i18n) == nil && len(i18n) > 0 {
					entry["label"] = i18n
				}
			}
		}
		if b, err := json.Marshal(entry); err == nil {
			out = append(out, b)
		}
	}
	return out
}

func foreignProperties(dir, origin string) ([]wpk.Prop, []string, error) {
	raw := map[string]json.RawMessage{}
	if origin == "lively" {
		if propPath, ok := findFileFold(dir, "LivelyProperties.json"); ok {
			if err := readJSON(propPath, &raw); err != nil && !os.IsNotExist(err) {
				return nil, nil, err
			}
		}
	} else {
		projPath, ok := findFileFold(dir, "project.json")
		if !ok {
			return nil, nil, fmt.Errorf("import: project.json: %w", os.ErrNotExist)
		}
		var project struct {
			General struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Audio      bool                       `json:"supportsaudioprocessing"`
			} `json:"general"`
		}
		if err := readJSON(projPath, &project); err != nil {
			return nil, nil, err
		}
		if project.General.Audio {
			return nil, nil, fmt.Errorf("%w: Wallpaper Engine audio processing", ErrUnsupported)
		}
		raw = project.General.Properties
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var defs []wpk.Prop
	var warnings []string
	for _, k := range keys {
		if len(defs) >= 64 {
			warnings = append(warnings, "Excess property omitted (>64): "+k)
			continue
		}
		var c struct {
			Type, Text     string
			Value          json.RawMessage
			Min, Max, Step *float64
			Items          []json.RawMessage
			Options        []json.RawMessage
			MaxLength      int
		}
		if err := json.Unmarshal(raw[k], &c); err != nil {
			warnings = append(warnings, fmt.Sprintf("Malformed property omitted: %s (%v)", k, err))
			continue
		}
		p := wpk.Prop{
			Key:       k,
			Label:     wpk.I18n{"en": trunc(c.Text, 2000)},
			Default:   c.Value,
			Min:       c.Min,
			Max:       c.Max,
			Step:      c.Step,
			MaxLength: c.MaxLength,
		}
		switch strings.ToLower(c.Type) {
		case "slider":
			p.Type = "slider"
			if p.Step != nil && (math.IsNaN(*p.Step) || math.IsInf(*p.Step, 0) || *p.Step <= 0) {
				p.Step = nil
			}
		case "checkbox", "bool":
			p.Type = "bool"
		case "textbox", "textinput":
			p.Type = "text"
		case "color":
			p.Type = "color"
			if origin == "we" {
				var value string
				if json.Unmarshal(c.Value, &value) == nil {
					var r, g, b float64
					if _, err := fmt.Sscanf(value, "%f %f %f", &r, &g, &b); err == nil &&
						!math.IsNaN(r) && !math.IsInf(r, 0) &&
						!math.IsNaN(g) && !math.IsInf(g, 0) &&
						!math.IsNaN(b) && !math.IsInf(b, 0) {
						r, g, b = clamp01(r), clamp01(g), clamp01(b)
						p.Default, _ = json.Marshal(fmt.Sprintf("#%02x%02x%02x", int(r*255+.5), int(g*255+.5), int(b*255+.5)))
					}
				}
			}
		case "dropdown", "scalerdropdown", "combo":
			p.Type = "select"
			p.Options = normalizeSelectOptions(c.Options)
			if origin == "lively" {
				for i, label := range c.Items {
					var text string
					_ = json.Unmarshal(label, &text)
					option, _ := json.Marshal(map[string]any{"value": i, "label": wpk.I18n{"en": trunc(text, 2000)}})
					p.Options = append(p.Options, option)
				}
			}
		default:
			warnings = append(warnings, "Unsupported property control omitted: "+k+" ("+c.Type+")")
			continue
		}
		if len(p.Default) == 0 {
			warnings = append(warnings, "Malformed property omitted (missing default value): "+k)
			continue
		}
		if err := p.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("Malformed property omitted: %s (%v)", k, err))
			continue
		}
		defs = append(defs, p)
	}
	return defs, warnings, nil
}
