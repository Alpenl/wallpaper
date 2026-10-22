package web

import (
	"encoding/json"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"wpapp/internal/render"

	"wpapp/internal/wpk"
)

func TestRequestAndNavigationPolicy(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		origin := Origin("a", platform)
		for _, tt := range []struct {
			url          string
			grant, allow bool
		}{
			{origin + "/index.html", false, true}, {origin + "/a.js", true, true},
			{"https://example.com/a", false, false}, {"https://example.com/a", true, true},
			{origin + ".evil/a", false, false}, {origin + ":443/a", false, false}, {"file:///etc/passwd", true, false},
			{"javascript:alert(1)", true, false}, {"data:text/html,evil", true, false}, {"https://a.wpk.invalid@evil/a", false, false},
			{"https://user:pass@example.com/a", true, false}, {"//example.com/a", true, false}, {"wpk://other/a", true, false},
		} {
			if got := AllowRequest(origin, tt.url, tt.grant); got != tt.allow {
				t.Errorf("%s %q grant %v: %v", platform, tt.url, tt.grant, got)
			}
		}
		if !AllowNavigation(origin, origin+"/index.html", false) || AllowNavigation(origin, "https://example.com", false) || AllowNavigation(origin, origin+"/index.html", true) {
			t.Fatal("navigation escaped origin")
		}
	}
}
func TestPropertyInjectionRoundTrip(t *testing.T) {
	attack := "</script><script>alert('x')</script>\u2028\u2029\"\\\n"
	value, _ := json.Marshal(attack)
	defs := []wpk.Prop{{Key: "caption", Type: "text", Default: value}, {Key: "__proto__", Type: "text", Default: json.RawMessage(`"safe"`)}}
	s, e := PropertyScript(defs, "")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(s, "</script>") || strings.Contains(s, "\u2028") || strings.Contains(s, "\u2029") {
		t.Fatal("unescaped script content")
	}
	i := strings.LastIndex(s, "JSON.parse(")
	literal := strings.TrimSuffix(s[i+len("JSON.parse("):], "));")
	var inner string
	if e = json.Unmarshal([]byte(literal), &inner); e != nil {
		t.Fatal(e, s)
	}
	var got map[string]string
	if e = json.Unmarshal([]byte(inner), &got); e != nil {
		t.Fatal(e)
	}
	if got["caption"] != attack || got["__proto__"] != "safe" {
		t.Fatal(got)
	}
	if _, e := PropertyScript(defs, `{"unknown":1}`); e == nil {
		t.Fatal("accepted unknown key")
	}
	if _, e := PropertyScript(defs, `{"caption":true}`); e == nil {
		t.Fatal("accepted wrong type")
	}
}
func TestFPSProfileLimit(t *testing.T) {
	for _, x := range []struct{ m, p, w int }{{0, 0, 30}, {60, 15, 15}, {15, 60, 15}, {24, 30, 24}, {30, 0, 30}} {
		if EffectiveFPS(x.m, x.p) != x.w {
			t.Fatal(x)
		}
	}
	if _, e := FPSScript(-1); e == nil {
		t.Fatal("negative FPS")
	}
}

type fakeView struct{ calls []string }

func (f *fakeView) Load(loadRequest) error { f.calls = append(f.calls, "load"); return nil }
func (f *fakeView) Pause(b bool) error {
	if b {
		f.calls = append(f.calls, "pause")
	} else {
		f.calls = append(f.calls, "resume")
	}
	return nil
}
func (f *fakeView) Script(s string) error     { f.calls = append(f.calls, s); return nil }
func (f *fakeView) Properties(s string) error { return f.Script(s) }
func (f *fakeView) FPS(fps int) error         { s, _ := FPSScript(fps); return f.Script(s) }
func (f *fakeView) Close()                    {}
func TestHostControlValidation(t *testing.T) {
	f := &fakeView{}
	h := hostSession{view: f, loaded: true, manifestFPS: 30, defsScript: func(s string) (string, error) { return PropertyScript(nil, s) }}
	for _, x := range []struct{ method, params string }{{"pause", "1"}, {"pause", "2"}, {"pause", "3"}, {"pause", "0"}, {"fps", "15"}, {"properties", `"{}"`}} {
		if _, e := h.handle(x.method, json.RawMessage(x.params)); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.calls) != 6 || f.calls[0] != "pause" || f.calls[3] != "resume" || f.calls[4] != "window.__wpkFPS(15);" {
		t.Fatal(f.calls)
	}
	for _, x := range []struct{ method, params string }{{"pause", "4"}, {"fps", "-1"}, {"properties", `"{\"bad\":1}"`}, {"unknown", "null"}} {
		if _, e := h.handle(x.method, json.RawMessage(x.params)); e == nil {
			t.Fatal(x)
		}
	}
	if len(f.calls) != 6 {
		t.Fatal("invalid operation reached native host")
	}
}

func TestHostLoadIdentity(t *testing.T) {
	dir, e := filepath.Abs("../../../examples/web-orbit")
	if e != nil {
		t.Fatal(e)
	}
	request := loadRequest{Item: render.Item{ID: "1e7541cf-d58d-4d24-9639-d2938017f120", Kind: "web", PackageDir: dir, Path: filepath.Join(dir, "content", "index.html"), FPSCap: 15}, Bounds: image.Rect(0, 0, 640, 360)}
	f := &fakeView{}
	h := hostSession{view: f}
	b, _ := json.Marshal(request)
	if _, err := h.handle("load", b); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != "load" {
		t.Fatal(f.calls)
	}
	for _, mutate := range []func(*loadRequest){func(x *loadRequest) { x.Item.ID = "wrong" }, func(x *loadRequest) { x.Item.Path = filepath.Join(dir, "preview.png") }, func(x *loadRequest) { x.Item.NetworkGranted = true }, func(x *loadRequest) { x.Item.PackageDir = "relative" }, func(x *loadRequest) { x.Item.Properties = `{"speed":100}` }} {
		x := request
		mutate(&x)
		if _, e := validateLoad(x); e == nil {
			t.Fatal("invalid load accepted")
		}
	}
}
