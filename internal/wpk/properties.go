package wpk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// PropertyValues validates overrides and fills defaults. Unknown keys are rejected.
func PropertyValues(defs []Prop, overrides string) (map[string]any, error) {
	raw := map[string]json.RawMessage{}
	if overrides != "" {
		if err := json.Unmarshal([]byte(overrides), &raw); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, fmt.Errorf("properties must be an object")
		}
	}
	values := make(map[string]any, len(defs))
	for _, p := range defs {
		b := p.Default
		if v, ok := raw[p.Key]; ok {
			b = v
			delete(raw, p.Key)
		}
		if err := p.ValidateValue(b); err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		values[p.Key] = v
	}
	if len(raw) > 0 {
		return nil, fmt.Errorf("unknown property override")
	}
	return values, nil
}

// OptionValues accepts the schema's {value,label} options and legacy scalars.
func (p Prop) OptionValues() []json.RawMessage {
	out := make([]json.RawMessage, len(p.Options))
	for i, b := range p.Options {
		var o struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(b, &o) == nil && o.Value != nil {
			out[i] = o.Value
		} else {
			out[i] = b
		}
	}
	return out
}

func (p Prop) ValidateValue(b json.RawMessage) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return bad("property %s: invalid JSON", p.Key)
	}
	invalid := func() error { return bad("property %s: invalid %s value", p.Key, p.Type) }
	switch p.Type {
	case "slider":
		n, ok := v.(float64)
		if !ok || p.Min == nil || p.Max == nil || math.IsNaN(n) || n < *p.Min || n > *p.Max {
			return invalid()
		}
	case "bool":
		if _, ok := v.(bool); !ok {
			return invalid()
		}
	case "text":
		s, ok := v.(string)
		if !ok || len([]rune(s)) > p.TextLimit() {
			return invalid()
		}
	case "color":
		s, ok := v.(string)
		if !ok || !reColor.MatchString(s) {
			return invalid()
		}
	case "select":
		for _, o := range p.OptionValues() {
			var x any
			if json.Unmarshal(o, &x) != nil {
				continue
			}
			a, _ := json.Marshal(x)
			c, _ := json.Marshal(v)
			if bytes.Equal(a, c) {
				return nil
			}
		}
		return invalid()
	default:
		return invalid()
	}
	return nil
}
func (p Prop) TextLimit() int {
	if p.MaxLength > 0 {
		return p.MaxLength
	}
	return 2000
}
