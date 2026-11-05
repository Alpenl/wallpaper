package wescene

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = 2
const ParserVersion = "1"
const MaxReportBytes = 64 << 10
const MaxList = 64
const MaxLabel = 256
const MaxCameraDimension = 1 << 20

type ObjectCounts struct {
	Other     int `json:"other"`
	Image     int `json:"image"`
	Particle  int `json:"particle"`
	Text      int `json:"text"`
	Sound     int `json:"sound"`
	Model     int `json:"model"`
	Puppet    int `json:"puppet"`
	Animation int `json:"animation"`
	Unknown   int `json:"unknown"`
}
type Features struct {
	CameraWidth       int    `json:"camera_width"`
	CameraHeight      int    `json:"camera_height"`
	BloomValue        *bool  `json:"bloom_value"`
	ParallaxValue     *bool  `json:"parallax_value"`
	CameraShake       string `json:"camerashake"`
	CameraShakeValue  *bool  `json:"camerashake_value"`
	CustomShaders     string `json:"custom_shaders"`
	SceneScript       string `json:"scene_script"`
	AudioReactive     string `json:"audio_reactive"`
	CursorInteraction string `json:"cursor_interaction"`
	Camera            string `json:"camera"`
	Bloom             string `json:"bloom"`
	Parallax          string `json:"parallax"`
}
type Report struct {
	ScriptCount          int          `json:"script_count"`
	SchemaVersion        int          `json:"schema_version"`
	ParserVersion        string       `json:"parser_version"`
	PKGVersion           int          `json:"pkg_version"`
	EntryCount           int          `json:"entry_count"`
	Status               string       `json:"status"`
	Truncated            bool         `json:"truncated"`
	Reason               string       `json:"reason"`
	Objects              ObjectCounts `json:"objects"`
	UnknownKinds         []string     `json:"unknown_kinds"`
	EffectCount          int          `json:"effect_count"`
	EffectNames          []string     `json:"effect_names"`
	Features             Features     `json:"features"`
	UnresolvedReferences []string     `json:"unresolved_in_package_references"`
}

func newReport() Report {
	return Report{SchemaVersion: SchemaVersion, ParserVersion: ParserVersion, Status: "complete",
		UnknownKinds: []string{}, EffectNames: []string{}, UnresolvedReferences: []string{},
		Features: Features{CustomShaders: "unknown", SceneScript: "unknown", AudioReactive: "unknown", CursorInteraction: "unknown", Camera: "unknown", Bloom: "unknown", Parallax: "unknown", CameraShake: "unknown"}}
}
func (r *Report) partial(reason string) {
	if r.Status != "failed" {
		r.Status = "partial"
	}
	if r.Reason == "" {
		r.Reason = reason
	}
}
func (r *Report) add(list *[]string, s string) {
	for _, v := range *list {
		if v == s {
			return
		}
	}
	if len(*list) >= MaxList || len(s) > MaxLabel || !utf8.ValidString(s) {
		r.Truncated = true
		r.partial("report labels truncated")
		r.Reason = "report labels truncated"
		return
	}
	*list = append(*list, s)
}
func (r *Report) finish() {
	lists := []*[]string{&r.UnknownKinds, &r.EffectNames, &r.UnresolvedReferences}
	for _, list := range lists {
		sort.Strings(*list)
	}
	for {
		b, _ := json.Marshal(r)
		if len(b)+1 <= MaxReportBytes {
			return
		}
		r.Truncated = true
		r.partial("report byte budget exceeded")
		r.Reason = "report byte budget exceeded"
		for _, list := range lists {
			if len(*list) > 0 {
				*list = (*list)[:len(*list)-1]
			}
		}
	}
}

// ValidateReport is the main-process trust boundary. It rejects unknown fields,
// duplicate keys, missing fields, invalid enums/counts and multiple JSON values.
func ValidateReport(b []byte) (Report, error) {
	var r Report
	if len(b) > MaxReportBytes {
		return r, fmt.Errorf("report byte budget exceeded")
	}
	v, err := decodeJSON(bytes.NewReader(b))
	if err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&r); err != nil {
		return r, err
	}
	// Require the complete shape, including fields whose zero values are valid.
	canonical, _ := json.Marshal(newReport())
	shape, _ := decodeJSON(bytes.NewReader(canonical))
	if !sameShape(v, shape) {
		return r, fmt.Errorf("incomplete report shape")
	}
	if r.SchemaVersion != SchemaVersion || r.ParserVersion != ParserVersion || r.PKGVersion < 0 || r.PKGVersion > 24 || r.EntryCount < 0 || r.EntryCount > MaxEntries {
		return r, fmt.Errorf("invalid report version or entry count")
	}
	if r.Status != "complete" && r.Status != "partial" && r.Status != "failed" {
		return r, fmt.Errorf("invalid report status")
	}
	if len(r.Reason) > 512 || !utf8.ValidString(r.Reason) || (r.Status == "complete" && (r.Reason != "" || r.Truncated)) || (r.Status != "complete" && r.Reason == "") || (r.Status != "failed" && r.PKGVersion == 0) {
		return r, fmt.Errorf("inconsistent report status")
	}
	counts := []int{r.Objects.Image, r.Objects.Particle, r.Objects.Text, r.Objects.Sound, r.Objects.Model, r.Objects.Puppet, r.Objects.Animation, r.Objects.Unknown, r.Objects.Other, r.EffectCount, r.ScriptCount}
	total := 0
	for _, n := range counts {
		if n < 0 || n > MaxNodes {
			return r, fmt.Errorf("invalid report count")
		}
		total += n
	}
	if total > MaxNodes {
		return r, fmt.Errorf("report counts exceed budget")
	}
	for _, list := range [][]string{r.UnknownKinds, r.EffectNames, r.UnresolvedReferences} {
		if len(list) > MaxList {
			return r, fmt.Errorf("report list exceeds budget")
		}
		seen := map[string]bool{}
		for _, s := range list {
			if s == "" || len(s) > MaxLabel || !utf8.ValidString(s) || seen[s] {
				return r, fmt.Errorf("invalid report label")
			}
			seen[s] = true
		}
	}
	if len(r.UnknownKinds) > r.Objects.Unknown || len(r.EffectNames) > r.EffectCount {
		return r, fmt.Errorf("inconsistent report names")
	}
	for _, s := range []string{r.Features.CustomShaders, r.Features.SceneScript, r.Features.AudioReactive, r.Features.CursorInteraction} {
		if s != "present" && s != "unknown" {
			return r, fmt.Errorf("invalid feature state")
		}
	}
	if !strings.Contains("|unknown|ortho|perspective|", "|"+r.Features.Camera+"|") || r.Features.Camera == "" {
		return r, fmt.Errorf("invalid camera state")
	}
	for _, flag := range []struct {
		state string
		value *bool
	}{
		{r.Features.Bloom, r.Features.BloomValue},
		{r.Features.Parallax, r.Features.ParallaxValue},
		{r.Features.CameraShake, r.Features.CameraShakeValue},
	} {
		if flag.state != "on" && flag.state != "off" && flag.state != "property" && flag.state != "unknown" {
			return r, fmt.Errorf("invalid general flag")
		}
		if flag.value != nil && flag.state != "property" {
			return r, fmt.Errorf("unexpected property value")
		}
	}
	w, h := r.Features.CameraWidth, r.Features.CameraHeight
	if (r.Features.Camera == "ortho" && (w <= 0 || h <= 0 || w > MaxCameraDimension || h > MaxCameraDimension)) || (r.Features.Camera != "ortho" && (w != 0 || h != 0)) {
		return r, fmt.Errorf("invalid camera dimensions")
	}
	if r.ScriptCount > 0 && r.Features.SceneScript != "present" {
		return r, fmt.Errorf("inconsistent script count")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded)+1 > MaxReportBytes {
		return r, fmt.Errorf("encoded report byte budget exceeded")
	}
	return r, nil
}
func sameShape(v, template any) bool {
	switch t := template.(type) {
	case nil:
		_, ok := v.(bool)
		return v == nil || ok
	case map[string]any:
		m, ok := v.(map[string]any)
		if !ok || len(m) != len(t) {
			return false
		}
		for k, x := range t {
			if !sameShape(m[k], x) {
				return false
			}
		}
		return true
	case []any:
		_, ok := v.([]any)
		return ok
	case string:
		_, ok := v.(string)
		return ok
	case bool:
		_, ok := v.(bool)
		return ok
	case json.Number:
		_, ok := v.(json.Number)
		return ok
	}
	return false
}
