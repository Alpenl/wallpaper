package wescene

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type asset struct{ name, body string }

func packageBytes(assets ...asset) []byte {
	var b bytes.Buffer
	put := func(n int) { _ = binary.Write(&b, binary.LittleEndian, int32(n)) }
	put(8)
	b.WriteString("PKGV0024")
	put(len(assets))
	offset := 0
	for _, a := range assets {
		put(len(a.name))
		b.WriteString(a.name)
		put(offset)
		put(len(a.body))
		offset += len(a.body)
	}
	for _, a := range assets {
		b.WriteString(a.body)
	}
	return b.Bytes()
}
func writePackage(t *testing.T, assets ...asset) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "scene.pkg")
	if err := os.WriteFile(p, packageBytes(assets...), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestReadIndex(t *testing.T) {
	good := packageBytes(asset{"scene.json", "{}"}, asset{"models/a.json", "123"})
	idx, err := ReadIndex(bytes.NewReader(good), int64(len(good)))
	if err != nil || idx.Version != 24 || len(idx.Entries) != 2 {
		t.Fatalf("%+v %v", idx, err)
	}
	if string(good[idx.Entries[1].Offset:]) != "123" {
		t.Fatal("offset is not relative to data area")
	}
	for _, version := range []string{"0001", "0024"} {
		b := bytes.Clone(good)
		copy(b[8:12], version)
		if _, err := ReadIndex(bytes.NewReader(b), int64(len(b))); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string][]byte{}
	for name, change := range map[string]func([]byte){
		"negative count":  func(b []byte) { binary.LittleEndian.PutUint32(b[12:], 0xffffffff) },
		"count budget":    func(b []byte) { binary.LittleEndian.PutUint32(b[12:], MaxEntries+1) },
		"negative name":   func(b []byte) { binary.LittleEndian.PutUint32(b[16:], 0xffffffff) },
		"long name":       func(b []byte) { binary.LittleEndian.PutUint32(b[16:], 1025) },
		"negative offset": func(b []byte) { binary.LittleEndian.PutUint32(b[30:], 0xffffffff) },
		"negative size":   func(b []byte) { binary.LittleEndian.PutUint32(b[34:], 0xffffffff) },
		"out of range":    func(b []byte) { binary.LittleEndian.PutUint32(b[30:], 100000) },
		"32 bit sum overflow": func(b []byte) {
			binary.LittleEndian.PutUint32(b[30:], 0x7fffffff)
			binary.LittleEndian.PutUint32(b[34:], 0x7fffffff)
		},
		"future":               func(b []byte) { copy(b[8:12], "0025") },
		"zero version":         func(b []byte) { copy(b[8:12], "0000") },
		"invalid magic":        func(b []byte) { b[4] = 'X' },
		"invalid digits":       func(b []byte) { b[8] = 'x' },
		"invalid magic length": func(b []byte) { b[0] = 9 },
		"overlap":              func(b []byte) { binary.LittleEndian.PutUint32(b[55:], 1) },
	} {
		b := bytes.Clone(good)
		change(b)
		cases[name] = b
	}
	for _, name := range []string{"", "/root", "C:foo", "C:/foo", "a\\b", "a/../b", "a\x00b", ".", string([]byte{0xff})} {
		cases["name "+name] = packageBytes(asset{name, "{}"})
	}
	cases["duplicate"] = packageBytes(asset{"a/b", "x"}, asset{"a//./b", "y"})
	assets := make([]asset, 4200)
	for i := range assets {
		assets[i] = asset{fmt.Sprintf("%04d", i) + strings.Repeat("a", 1020), ""}
	}
	cases["index budget"] = packageBytes(assets...)
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadIndex(bytes.NewReader(b), int64(len(b))); err == nil {
				t.Fatal("accepted invalid package")
			}
		})
	}
	for n := 0; n < len(good); n++ {
		if _, err := ReadIndex(bytes.NewReader(good[:n]), int64(n)); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		}
	}
}
func TestProbeFeatures(t *testing.T) {
	scene := `{"objects":[{"image":"models/a.json","material":"materials/missing.json","effects":[{"file":"effects/glow.json"}]},{"particle":"particles/a.json"},{"text":"hi"},{"sound":"sounds/a.ogg"},{"model":"models/builtin.json"},{"puppet":{}},{"animation":{}},{"type":"future"}],"camera":{"orthogonal":{}},"general":{"orthogonalprojection":{"width":3840,"height":2160},"bloom":true,"cameraparallax":true},"audioreactive":true,"mouseinteractive":true}`
	p := writePackage(t, asset{"scene.json", scene}, asset{"models/a.json", "invalid json deliberately unread"}, asset{"shaders/x.frag", "not read"}, asset{"scripts/x.js", "not run"}, asset{"x.tex", "not decoded"})
	r := Probe(p)
	if r.Status != "partial" || r.Truncated || r.Objects != (ObjectCounts{Image: 1, Particle: 1, Text: 1, Sound: 1, Model: 1, Puppet: 1, Animation: 1, Unknown: 1}) || r.EffectCount != 1 || len(r.EffectNames) != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Features != (Features{CustomShaders: "present", SceneScript: "present", AudioReactive: "present", CursorInteraction: "present", Camera: "ortho", CameraWidth: 3840, CameraHeight: 2160, Bloom: "on", Parallax: "on", CameraShake: "unknown"}) {
		t.Fatalf("%+v", r.Features)
	}
	if strings.Join(r.UnresolvedReferences, ",") != "materials/missing.json,models/builtin.json" {
		t.Fatal(r.UnresolvedReferences)
	}
	b, _ := json.Marshal(r)
	if _, err := ValidateReport(b); err != nil {
		t.Fatal(err)
	}
	r = Probe(writePackage(t, asset{"scene.json", `{"objects":[]}`}))
	if r.Status != "complete" || r.Features != newReport().Features {
		t.Fatalf("%+v", r)
	}
}
func TestProbeBudgetsAndDuplicateKeys(t *testing.T) {
	for name, scene := range map[string]string{
		"deep":       `{"objects":[],"x":` + strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1) + "}",
		"nodes":      `{"objects":[],"x":[` + strings.Repeat("0,", MaxNodes) + "0]}",
		"bytes":      strings.Repeat(" ", MaxSceneBytes+1),
		"duplicate":  `{"objects":[],"x":{"a":1,"a":2}}`,
		"multiple":   `{"objects":[]} {}`,
		"not object": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			r := Probe(writePackage(t, asset{"scene.json", scene}))
			if r.Status != "failed" || r.Reason == "" || r.Truncated {
				t.Fatalf("%+v", r)
			}
			b, _ := json.Marshal(r)
			if _, err := ValidateReport(b); err != nil {
				t.Fatal(err)
			}
		})
	}
	r := Probe(writePackage(t, asset{"x", ""}))
	if r.Status != "failed" {
		t.Fatal(r)
	}
}
func TestTruncation(t *testing.T) {
	for _, n := range []int{MaxList, MaxList + 1} {
		objects := make([]any, n)
		for i := range objects {
			objects[i] = map[string]any{"type": fmt.Sprintf("kind-%d", i)}
		}
		scene, _ := json.Marshal(map[string]any{"objects": objects})
		r := Probe(writePackage(t, asset{"scene.json", string(scene)}))
		if n > MaxList && r.Reason != "report labels truncated" {
			t.Fatal("missing truncation reason", r.Reason)
		}
		if r.Truncated != (n > MaxList) || len(r.UnknownKinds) != MaxList || r.Objects.Unknown != n {
			t.Fatalf("%+v", r)
		}
	}
	r := newReport()
	r.PKGVersion = 1
	for i := 0; i < MaxList; i++ {
		s := fmt.Sprintf("%03d", i) + strings.Repeat("\x01", MaxLabel-3)
		r.add(&r.UnknownKinds, s)
		r.add(&r.EffectNames, s)
		r.add(&r.UnresolvedReferences, s)
	}
	r.Objects.Unknown = MaxList
	r.EffectCount = MaxList
	r.finish()
	b, _ := json.Marshal(r)
	if len(b)+1 > MaxReportBytes || !r.Truncated || r.Status != "partial" || r.Reason == "" {
		t.Fatal("byte truncation not marked")
	}
	if _, err := ValidateReport(b); err != nil {
		t.Fatal(err)
	}
	r = newReport()
	r.add(&r.EffectNames, strings.Repeat("x", MaxLabel+1))
	if !r.Truncated {
		t.Fatal("long label not marked")
	}
}
func TestProjectInput(t *testing.T) {
	p := writePackage(t, asset{"scene.json", `{"objects":[]}`})
	dir := filepath.Dir(p)
	if Probe(dir).Status != "failed" || ProbeInput(dir).Status != "complete" {
		t.Fatal("directory policy")
	}
	for _, v := range []struct{ body, status string }{{`{"type":"scene"}`, "complete"}, {`{"type":"Scene"}`, "complete"}, {`{"type":"SCENE"}`, "complete"}, {`{"type":"web"}`, "failed"}, {`{"type":"scene","type":"scene"}`, "failed"}, {`{`, "failed"}} {
		if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(v.body), 0600); err != nil {
			t.Fatal(err)
		}
		if r := ProbeInput(dir); r.Status != v.status {
			t.Fatal(r)
		}
	}
}
func TestValidateReport(t *testing.T) {
	r := newReport()
	r.PKGVersion = 1
	b, _ := json.Marshal(r)
	for name, change := range map[string]func(*Report){
		"schema": func(r *Report) { r.SchemaVersion++ }, "parser": func(r *Report) { r.ParserVersion = "future" }, "negative": func(r *Report) { r.Objects.Image = -1 }, "count": func(r *Report) { r.EntryCount = MaxEntries + 1 }, "list": func(r *Report) { r.UnresolvedReferences = make([]string, MaxList+1) }, "status": func(r *Report) { r.Truncated = true }, "enum": func(r *Report) { r.Features.Bloom = "absent" }, "reason": func(r *Report) { r.Status = "failed" }, "pkg": func(r *Report) { r.PKGVersion = 25 },
	} {
		t.Run(name, func(t *testing.T) {
			x := r
			change(&x)
			b, _ := json.Marshal(x)
			if _, err := ValidateReport(b); err == nil {
				t.Fatal("accepted invalid report")
			}
		})
	}
	for _, s := range []string{`{}`, `null`, string(b) + string(b), strings.Replace(string(b), `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1), strings.Replace(string(b), `"entry_count":0,`, "", 1), strings.Replace(string(b), `"objects":{`, `"objects":{"extra":0,`, 1), strings.Replace(string(b), `"effect_names":[]`, `"effect_names":null`, 1)} {
		if _, err := ValidateReport([]byte(s)); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestValidateReportEncodedBudget(t *testing.T) {
	r := newReport()
	r.PKGVersion = 1
	r.Objects.Unknown = MaxList
	r.EffectCount = MaxList
	for i := 0; i < MaxList; i++ {
		s := fmt.Sprintf("%03d", i) + strings.Repeat("<", MaxLabel-3)
		r.UnknownKinds = append(r.UnknownKinds, s)
		r.EffectNames = append(r.EffectNames, s)
		r.UnresolvedReferences = append(r.UnresolvedReferences, s)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		t.Fatal(err)
	}
	if b.Len() > MaxReportBytes {
		t.Fatal("fixture exceeds input budget")
	}
	if _, err := ValidateReport(b.Bytes()); err == nil {
		t.Fatal("accepted output encoding expansion")
	}
}

func FuzzReadIndex(f *testing.F) {
	f.Add(packageBytes(asset{"scene.json", "{}"}))
	f.Add([]byte("PKGV"))
	f.Fuzz(func(t *testing.T, b []byte) {
		idx, err := ReadIndex(bytes.NewReader(b), int64(len(b)))
		if err == nil {
			for _, e := range idx.Entries {
				if e.Offset < idx.DataOffset || e.Size < 0 || e.Offset > int64(len(b))-e.Size {
					t.Fatal("invalid accepted range")
				}
			}
		}
	})
}

func TestGeneralShapes(t *testing.T) {
	for _, tc := range []struct {
		name, general, state, camera string
		value                        *bool
		width, height                int
	}{
		{name: "bools", general: `{"bloom":false,"cameraparallax":false,"camerashake":false}`, state: "off", camera: "perspective"},
		{name: "on", general: `{"bloom":true,"cameraparallax":true,"camerashake":true,"orthogonalprojection":null}`, state: "on", camera: "perspective"},
		{name: "bound false", general: `{"bloom":{"value":false},"cameraparallax":{"value":false},"camerashake":{"value":false},"orthogonalprojection":{"width":3840,"height":2160}}`, state: "property", value: boolPtr(false), camera: "ortho", width: 3840, height: 2160},
		{name: "bound true", general: `{"bloom":{"value":true},"cameraparallax":{"value":true},"camerashake":{"value":true}}`, state: "property", value: boolPtr(true), camera: "perspective"},
		{name: "complex", general: `{"bloom":{"value":{}},"cameraparallax":{"value":null},"camerashake":{"value":[]},"orthogonalprojection":{}}`, state: "property", camera: "unknown"},
		{name: "unrecognized", general: `{"bloom":1,"cameraparallax":"false","camerashake":{},"orthogonalprojection":{"width":0,"height":2160}}`, state: "unknown", camera: "unknown"},
		{name: "bounded", general: `{"orthogonalprojection":{"width":1048577,"height":2160}}`, state: "unknown", camera: "unknown"},
		{name: "fractional", general: `{"orthogonalprojection":{"width":1.5,"height":2160}}`, state: "unknown", camera: "unknown"},
		{name: "invalid general", general: `null`, state: "unknown", camera: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Probe(writePackage(t, asset{"scene.json", `{"objects":[],"bloom":true,"camera":{"ortho":{}},"general":` + tc.general + `}`}))
			f := r.Features
			if r.Status != "complete" || f.Bloom != tc.state || f.Parallax != tc.state || f.CameraShake != tc.state || f.Camera != tc.camera || f.CameraWidth != tc.width || f.CameraHeight != tc.height {
				t.Fatalf("%+v", r)
			}
			for _, v := range []*bool{f.BloomValue, f.ParallaxValue, f.CameraShakeValue} {
				if (v == nil) != (tc.value == nil) || (v != nil && *v != *tc.value) {
					t.Fatalf("wrong property value: %v", v)
				}
			}
			b, _ := json.Marshal(r)
			if _, err := ValidateReport(b); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func boolPtr(b bool) *bool { return &b }

func TestRealObjectAndNestedScriptShapes(t *testing.T) {
	scene := `{"objects":[{"image":"a","origin":{"script":"materials/not-a-reference","value":"0 0 0"},"alpha":{"value":1,"script":null},"effects":[],"parallaxDepth":"0 0","visible":true},{"particle":"b","instanceoverride":{"properties":[{"value":{"script":"secret script body"}}]}},{"text":{"value":"hello","script":""}},{"sound":"c"},{"name":"group","origin":"0 0 0","visible":{"value":true}},{"name":"null"}]}`
	r := Probe(writePackage(t, asset{"scene.json", scene}))
	if r.Status != "complete" || r.Objects != (ObjectCounts{Image: 1, Particle: 1, Text: 1, Sound: 1, Other: 2}) || r.ScriptCount != 4 || r.Features.SceneScript != "present" || len(r.UnresolvedReferences) != 0 {
		t.Fatalf("%+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "secret") {
		t.Fatal("script body leaked")
	}
	if _, err := ValidateReport(b); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []string{`{"kind":"future"}`, `{"type":"future"}`, `{"type":{}}`} {
		r := Probe(writePackage(t, asset{"scene.json", `{"objects":[` + obj + `]}`}))
		if r.Status != "partial" || r.Objects.Unknown != 1 {
			t.Fatalf("%+v", r)
		}
	}
}

func TestValidateV2Fields(t *testing.T) {
	for _, mutate := range []func(*Report){
		func(r *Report) { r.SchemaVersion = 1 },
		func(r *Report) { r.Features.Camera = "both" },
		func(r *Report) { r.Features.Camera = "ortho" },
		func(r *Report) { r.Features.CameraWidth = 1 },
		func(r *Report) {
			r.Features.Camera = "ortho"
			r.Features.CameraWidth = MaxCameraDimension + 1
			r.Features.CameraHeight = 1
		},
		func(r *Report) { r.Features.BloomValue = boolPtr(false) },
		func(r *Report) { r.Features.CameraShake = "present" },
		func(r *Report) { r.Objects.Other = -1 },
		func(r *Report) { r.ScriptCount = MaxNodes + 1 },
		func(r *Report) { r.ScriptCount = 1 },
	} {
		r := newReport()
		r.PKGVersion = 1
		mutate(&r)
		b, _ := json.Marshal(r)
		if _, err := ValidateReport(b); err == nil {
			t.Fatalf("accepted %s", b)
		}
	}
}
