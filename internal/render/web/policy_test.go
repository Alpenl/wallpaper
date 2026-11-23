package web

import (
	"encoding/json"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
	"wpapp/internal/wpk"
)

func TestRequestAndNavigationPolicy(t *testing.T) {
	allowed := []string{"Example.COM", "api.weather.org"}
	for _, platform := range []string{"windows", "darwin"} {
		origin := Origin("a", platform)
		for _, tt := range []struct {
			url   string
			grant bool
			hosts []string
			allow bool
		}{
			{origin + "/index.html", false, nil, true},
			{origin + "/a.js", true, nil, true},
			{"https://example.com/a", false, allowed, false},
			// Network granted with empty networkAllow must allow NO external hosts.
			{"https://example.com/a", true, nil, false},
			{"https://example.com/a", true, []string{}, false},
			// Exact case-insensitive match against networkAllow.
			{"https://example.com/a", true, allowed, true},
			{"https://EXAMPLE.COM/a", true, allowed, true},
			{"http://api.weather.org:8080/v1", true, allowed, true},
			// Subdomain or unlisted host must be rejected.
			{"https://sub.example.com/a", true, allowed, false},
			{"https://notexample.com/a", true, allowed, false},
			// SSRF targets must always be blocked even if listed in networkAllow.
			{"http://localhost/admin", true, []string{"localhost"}, false},
			{"http://localhost./admin", true, []string{"localhost"}, false},
			{"http://foo.localhost/admin", true, []string{"foo.localhost"}, false},
			{"http://127.0.0.1/admin", true, []string{"127.0.0.1"}, false},
			{"http://127.255.0.1/admin", true, []string{"127.255.0.1"}, false},
			{"http://0.0.0.0/admin", true, []string{"0.0.0.0"}, false},
			{"http://169.254.169.254/latest/meta-data", true, []string{"169.254.169.254"}, false},
			{"http://10.0.0.1/secret", true, []string{"10.0.0.1"}, false},
			{"http://172.16.0.1/secret", true, []string{"172.16.0.1"}, false},
			{"http://192.168.1.1/secret", true, []string{"192.168.1.1"}, false},
			{"http://100.64.0.1/secret", true, []string{"100.64.0.1"}, false},
			{"http://[::1]/secret", true, []string{"::1"}, false},
			{"http://[fc00::1]/secret", true, []string{"fc00::1"}, false},
			{"http://[fe80::1]/secret", true, []string{"fe80::1"}, false},
			{"http://[::ffff:127.0.0.1]/secret", true, []string{"::ffff:127.0.0.1"}, false},
			{"http://[::ffff:169.254.169.254]/secret", true, []string{"::ffff:169.254.169.254"}, false},
			{"http://2130706433/", true, []string{"2130706433"}, false},
			{"http://127.1/", true, []string{"127.1"}, false},
			{"http://0x7f000001/", true, []string{"0x7f000001"}, false},
			// Protocols, credentials, origin confusion.
			{origin + ".evil/a", false, allowed, false},
			{origin + ":443/a", false, allowed, false},
			{"file:///etc/passwd", true, allowed, false},
			{"javascript:alert(1)", true, allowed, false},
			{"data:text/html,evil", true, allowed, false},
			{"https://a.wpk.invalid@evil/a", false, allowed, false},
			{"https://user:pass@example.com/a", true, allowed, false},
			{"//example.com/a", true, allowed, false},
			{"wpk://other/a", true, allowed, false},
			{"wss://example.com/ws", true, allowed, false},
		} {
			if got := AllowRequest(origin, tt.url, tt.grant, tt.hosts); got != tt.allow {
				t.Errorf("%s %q grant=%v hosts=%v: got %v want %v", platform, tt.url, tt.grant, tt.hosts, got, tt.allow)
			}
		}
		if !AllowNavigation(origin, origin+"/index.html", false) || AllowNavigation(origin, "https://example.com", false) || AllowNavigation(origin, origin+"/index.html", true) {
			t.Fatal("navigation escaped origin")
		}
	}
}

func TestContentRuleListJSON(t *testing.T) {
	// Offline mode: only block rules, no ignore-previous-rules.
	offline := contentRuleListJSON(false, []string{"example.com"})
	if !strings.Contains(offline, `"type":"block"`) || strings.Contains(offline, "ignore-previous-rules") {
		t.Fatalf("offline rules leaked allow: %s", offline)
	}
	// Online mode with SSRF and valid hosts: SSRF stripped, valid host exempted for http(s) only, ws(s) stays blocked.
	online := contentRuleListJSON(true, []string{"localhost", "127.0.0.1", "169.254.169.254", "::ffff:127.0.0.1", "API.Example.com"})
	if strings.Contains(online, "localhost") || strings.Contains(online, "127") || strings.Contains(online, "169") {
		t.Fatalf("SSRF host leaked into content rules: %s", online)
	}
	if !strings.Contains(online, `^https?://api\\.example\\.com(:[0-9]+)?/`) || !strings.Contains(online, `ignore-previous-rules`) {
		t.Fatalf("expected exact-host allow rule: %s", online)
	}
	if strings.Contains(online, `^wss?://api`) {
		t.Fatalf("ws/wss must not be exempted by default: %s", online)
	}
}

func TestBootstrapAndBrowserArgs(t *testing.T) {
	offline := bootstrapScript("tok", 30, false)
	for _, api := range []string{"RTCPeerConnection", "webkitRTCPeerConnection", "mozRTCPeerConnection", "RTCDataChannel", "WebTransport", "WebSocket", "EventSource"} {
		if !strings.Contains(offline, api) {
			t.Errorf("offline bootstrap missing %s lockdown", api)
		}
	}
	if !strings.Contains(offline, "if(!false)") || !strings.Contains(offline, "q.size>0&&fps>0") {
		t.Fatalf("unexpected offline bootstrap: %s", offline)
	}
	online := bootstrapScript("tok", 30, true)
	if !strings.Contains(online, "if(!true)") {
		t.Fatalf("unexpected online bootstrap: %s", online)
	}
	if args := browserArgs(false); !strings.Contains(args, `--host-resolver-rules="MAP * ~NOTFOUND"`) || !strings.Contains(args, `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`) {
		t.Fatalf("offline browserArgs incomplete: %s", args)
	}
	if args := browserArgs(true); strings.Contains(args, "--host-resolver-rules") || !strings.Contains(args, `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`) {
		t.Fatalf("online browserArgs wrong: %s", args)
	}
}

func TestWindowAndViewRectsSpanning(t *testing.T) {
	canvas := image.Rect(0, 0, 3840, 1080)
	right := image.Rect(1920, 0, 3840, 1080)
	winRect, vx, vy, vw, vh, err := windowAndViewRects(canvas, right)
	if err != nil {
		t.Fatal(err)
	}
	if winRect != right || vx != -1920 || vy != 0 || vw != 3840 || vh != 1080 {
		t.Fatalf("got win=%v view=(%v,%v,%v,%v)", winRect, vx, vy, vw, vh)
	}
	winRect, vx, vy, vw, vh, err = windowAndViewRects(right, image.Rectangle{})
	if err != nil || winRect != right || vx != 0 || vy != 0 || vw != 1920 || vh != 1080 {
		t.Fatalf("non-spanning mismatch: win=%v view=(%v,%v,%v,%v) err=%v", winRect, vx, vy, vw, vh, err)
	}
}

func TestShouldKillOnCallError(t *testing.T) {
	if shouldKillOnCallError(&ipc.Error{Code: "E_LOAD", Msg: "invalid property"}) {
		t.Fatal("ordinary validation error must not kill child host")
	}
	if !shouldKillOnCallError(&ipc.Error{Code: "E_LOAD", Msg: "fatal", Fatal: true}) {
		t.Fatal("fatal IPC error must kill child host")
	}
	if !shouldKillOnCallError(&ipc.Error{Code: "E_TIMEOUT", Msg: "timed out"}) {
		t.Fatal("IPC timeout must kill child host")
	}
	if !shouldKillOnCallError(fmt.Errorf("E_PROTO: connection closed")) {
		t.Fatal("transport disconnect must kill child host")
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
func (f *fakeView) FPS(fps int) error         { return f.Script(fmt.Sprintf("fps:%d", fps)) }
func (f *fakeView) Close()                    {}

func TestHostControlValidation(t *testing.T) {
	f := &fakeView{}
	h := hostSession{view: f, loaded: true, manifestFPS: 30, defsScript: func(s string) (string, error) { return PropertyScript(nil, s) }}
	for _, x := range []struct{ method, params string }{{"pause", "1"}, {"pause", "2"}, {"pause", "3"}, {"pause", "0"}, {"fps", "15"}, {"properties", `"{}"`}} {
		if _, e := h.handle(x.method, json.RawMessage(x.params)); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.calls) != 6 || f.calls[0] != "pause" || f.calls[3] != "resume" || f.calls[4] != "fps:15" {
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
	reply, err := h.handle("load", b)
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := reply.(render.HelperIdentity)
	if !ok || identity.PID <= 0 || identity.Build.Version == "" {
		t.Fatal(reply)
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
