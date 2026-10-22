package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wpapp/internal/wpk"
)

func foreignProperties(dir, origin string) ([]wpk.Prop, []string, error) {
	raw := map[string]json.RawMessage{}
	if origin == "lively" {
		if err := readJSON(filepath.Join(dir, "LivelyProperties.json"), &raw); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	} else {
		var project struct {
			General struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Audio      bool                       `json:"supportsaudioprocessing"`
			} `json:"general"`
		}
		if err := readJSON(filepath.Join(dir, "project.json"), &project); err != nil {
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
		var c struct {
			Type, Text     string
			Value          json.RawMessage
			Min, Max, Step *float64
			Items          []json.RawMessage
			Options        []json.RawMessage
			MaxLength      int
		}
		if err := json.Unmarshal(raw[k], &c); err != nil {
			return nil, nil, err
		}
		p := wpk.Prop{Key: k, Label: wpk.I18n{"en": c.Text}, Default: c.Value, Min: c.Min, Max: c.Max, Step: c.Step, MaxLength: c.MaxLength}
		switch strings.ToLower(c.Type) {
		case "slider":
			p.Type = "slider"
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
					if _, err := fmt.Sscanf(value, "%f %f %f", &r, &g, &b); err == nil && r >= 0 && r <= 1 && g >= 0 && g <= 1 && b >= 0 && b <= 1 {
						p.Default, _ = json.Marshal(fmt.Sprintf("#%02x%02x%02x", int(r*255+.5), int(g*255+.5), int(b*255+.5)))
					}
				}
			}
		case "dropdown", "scalerdropdown", "combo":
			p.Type = "select"
			p.Options = c.Options
			if origin == "lively" {
				for i, label := range c.Items {
					var text string
					_ = json.Unmarshal(label, &text)
					option, _ := json.Marshal(map[string]any{"value": i, "label": wpk.I18n{"en": text}})
					p.Options = append(p.Options, option)
				}
			}
		default:
			warnings = append(warnings, "Unsupported property control omitted: "+k+" ("+c.Type+")")
			continue
		}
		if len(p.Default) == 0 {
			return nil, nil, fmt.Errorf("property %s has no value", k)
		}
		defs = append(defs, p)
	}
	return defs, warnings, nil
}
