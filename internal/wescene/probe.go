package wescene

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Probe reads only the package index and scene.json, never textures, shaders or
// referenced material/model bodies. Missing references can be WE built-ins.
func Probe(pkgPath string) (r Report) {
	r = newReport()
	defer r.finish()
	fail := func(reason string) Report { r.Status = "failed"; r.Reason = reason; return r }
	if !strings.EqualFold(filepath.Ext(pkgPath), ".pkg") {
		return fail("input must be a .pkg file")
	}
	f, err := os.Open(pkgPath)
	if err != nil {
		return fail("cannot open package")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return fail("package must be a regular file")
	}
	idx, err := ReadIndex(f, st.Size())
	if err != nil {
		return fail(err.Error())
	}
	r.PKGVersion = idx.Version
	r.EntryCount = len(idx.Entries)
	entries := make(map[string]bool)
	var scene *Entry
	for i := range idx.Entries {
		e := &idx.Entries[i]
		entries[e.Name] = true
		if e.Name == "scene.json" {
			scene = e
		}
		if strings.HasPrefix(e.Name, "shaders/") && (strings.HasSuffix(e.Name, ".vert") || strings.HasSuffix(e.Name, ".frag")) {
			r.Features.CustomShaders = "present"
		}
		if strings.HasPrefix(e.Name, "scripts/") {
			r.Features.SceneScript = "present"
		}
	}
	if scene == nil {
		return fail("scene.json not found")
	}
	if scene.Size > MaxSceneBytes {
		return fail("scene.json exceeds 16 MiB budget")
	}
	v, err := decodeJSON(io.NewSectionReader(f, scene.Offset, scene.Size))
	if err != nil {
		return fail("scene.json: " + err.Error())
	}
	root, ok := v.(map[string]any)
	if !ok {
		return fail("scene.json must be an object")
	}
	objects, ok := root["objects"].([]any)
	if !ok {
		r.partial("objects array missing or invalid")
	}
	for _, obj := range objects {
		r.object(obj)
	}
	r.general(root["general"])
	r.walk(root, entries)
	return r
}

// ProbeInput resolves directory metadata only inside the helper. Probe itself
// remains .pkg-only. project.json is optional, but if present must be scene.
func ProbeInput(input string) Report {
	st, err := os.Stat(input)
	if err != nil || !st.IsDir() {
		return Probe(input)
	}
	project := filepath.Join(input, "project.json")
	if err := checkProject(project); err != nil {
		r := newReport()
		r.Status = "failed"
		r.Reason = err.Error()
		return r
	}
	return Probe(filepath.Join(input, "scene.pkg"))
}
func checkProject(p string) error {
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot open project.json")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return fmt.Errorf("invalid project.json or size budget exceeded")
	}
	v, err := decodeJSON(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("invalid project.json")
	}
	m, ok := v.(map[string]any)
	kind, _ := m["type"].(string)
	if !ok || !strings.EqualFold(kind, "scene") {
		return fmt.Errorf("project.json type must be scene")
	}
	return nil
}
func (r *Report) object(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		r.Objects.Unknown++
		r.add(&r.UnknownKinds, "invalid object")
		r.partial("unrecognized object kinds")
		return
	}
	kind, _ := m["type"].(string)
	if kind == "" {
		kind, _ = m["kind"].(string)
	}
	_, hasType := m["type"]
	_, hasKind := m["kind"]
	if kind == "" && !hasType && !hasKind {
		for _, k := range []string{"puppet", "animation", "particle", "text", "sound", "model", "image"} {
			if _, ok := m[k]; ok {
				kind = k
				break
			}
		}
	}
	if kind == "" && !hasType && !hasKind {
		r.Objects.Other++
		return
	}
	switch kind {
	case "image":
		r.Objects.Image++
	case "particle":
		r.Objects.Particle++
	case "text":
		r.Objects.Text++
	case "sound":
		r.Objects.Sound++
	case "model":
		r.Objects.Model++
	case "puppet":
		r.Objects.Puppet++
	case "animation":
		r.Objects.Animation++
	default:
		r.Objects.Unknown++
		if kind == "" {
			kind = "unspecified"
		}
		r.add(&r.UnknownKinds, kind)
		r.partial("unrecognized object kinds")
	}
}
func enabled(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		n, err := x.Float64()
		return err == nil && n != 0
	case map[string]any:
		return enabled(x["value"])
	case string:
		return x != "" && x != "0" && x != "false"
	}
	return false
}

// Flags are facts from general, not truthiness inferred from arbitrary fields.
func flag(v any) (string, *bool) {
	if b, ok := v.(bool); ok {
		if b {
			return "on", nil
		}
		return "off", nil
	}
	if m, ok := v.(map[string]any); ok {
		if v, exists := m["value"]; exists {
			if b, ok := v.(bool); ok {
				return "property", &b
			}
			return "property", nil
		}
	}
	return "unknown", nil
}
func dimension(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		return 0
	}
	f, err := n.Float64()
	if err != nil || f < 1 || f > MaxCameraDimension || f != float64(int(f)) {
		return 0
	}
	return int(f)
}
func (r *Report) general(v any) {
	g, ok := v.(map[string]any)
	if !ok {
		return
	}
	r.Features.Bloom, r.Features.BloomValue = flag(g["bloom"])
	r.Features.Parallax, r.Features.ParallaxValue = flag(g["cameraparallax"])
	r.Features.CameraShake, r.Features.CameraShakeValue = flag(g["camerashake"])
	projection := g["orthogonalprojection"]
	if projection == nil {
		r.Features.Camera = "perspective"
		return
	}
	if m, ok := projection.(map[string]any); ok {
		w, h := dimension(m["width"]), dimension(m["height"])
		if w > 0 && h > 0 {
			r.Features.Camera = "ortho"
			r.Features.CameraWidth, r.Features.CameraHeight = w, h
		}
	}
}
func (r *Report) walk(v any, entries map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, v := range x {
			r.walk(v, entries)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := x[k]
			if strings.EqualFold(k, "script") {
				r.Features.SceneScript = "present"
				r.ScriptCount++
				continue // Do not inspect or report the body.
			}
			if s, ok := v.(string); ok && (strings.HasPrefix(s, "models/") || strings.HasPrefix(s, "materials/")) {
				name, err := cleanName(s)
				if err != nil || !entries[name] {
					r.add(&r.UnresolvedReferences, s)
				}
			}
			switch strings.ToLower(k) {
			case "material", "model":
				if s, ok := v.(string); ok && s != "" {
					name, err := cleanName(s)
					if err != nil || !entries[name] {
						r.add(&r.UnresolvedReferences, s)
					}
				}
			case "effects":
				if a, ok := v.([]any); ok {
					for _, e := range a {
						r.EffectCount++
						s, _ := e.(string)
						if m, ok := e.(map[string]any); ok {
							s, _ = m["name"].(string)
							if s == "" {
								s, _ = m["file"].(string)
							}
						}
						if s != "" {
							r.add(&r.EffectNames, s)
						}
					}
				} else {
					r.partial("unrecognized effects field")
				}
			case "audioreactive", "audio-reactive", "audioresponsive":
				if enabled(v) {
					r.Features.AudioReactive = "present"
				}
			case "cursor", "mouse", "mouseinteractive", "cursorinteractive":
				if enabled(v) {
					r.Features.CursorInteraction = "present"
				}

			}
			r.walk(v, entries)
		}
	}
}
