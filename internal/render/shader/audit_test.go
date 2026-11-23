package shader

import (
	"encoding/binary"
	"encoding/json"
	"image"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
	"wpapp/internal/wpk"
)

func TestPreprocessQuadraticMacroBomb(t *testing.T) {
	// Classic exponential/quadratic macro expansion payload.
	src := strings.Join([]string{
		"#define A(x) x+x+x+x+x+x+x+x+x+x",
		"#define B(x) A(A(A(x)))",
		"#define C(x) B(B(B(x)))",
		"void mainImage(out vec4 c, vec2 p) { float v = C(1.0); c = vec4(v); }",
	}, "\n")
	start := time.Now()
	_, err := Translate(src, HLSL)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "macro expansion limit") {
		t.Fatalf("expected macro expansion limit error, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("macro bomb rejection took too long: %v", elapsed)
	}
}

func TestHLSLPropertyRewritingAndShadowing(t *testing.T) {
	defs := []wpk.Prop{
		{Key: "color", Type: "color", Default: json.RawMessage(`"#ff0000"`)},
		{Key: "pos", Type: "slider", Default: json.RawMessage(`0.5`), Min: ptrFloat(0), Max: ptrFloat(1)},
		{Key: "id", Type: "select", Default: json.RawMessage(`1`), Options: []json.RawMessage{
			json.RawMessage(`{"value":0}`),
			json.RawMessage(`{"value":1.0}`),
		}},
	}
	src := `
vec3 helper(vec3 color) {
	return color * 0.5;
}
void mainImage(out vec4 outCol, vec2 p) {
	vec3 a = color * pos;
	vec3 b = helper(vec3(1.0));
	{
		vec3 color = vec3(0.2);
		a += color;
	}
	float k = float(id);
	outCol = vec4(a + b + vec3(k), 1.0);
}
`
	hlsl, err := TranslateProperties(src, HLSL, defs)
	if err != nil {
		t.Fatalf("HLSL translation failed: %v", err)
	}
	if strings.Contains(hlsl, "#define color") || strings.Contains(hlsl, "#define pos") || strings.Contains(hlsl, "#define id") {
		t.Fatalf("HLSL must not emit #define for user properties:\n%s", hlsl)
	}
	if !strings.Contains(hlsl, "wp_prop0.xyz") || !strings.Contains(hlsl, "wp_prop1.x") || !strings.Contains(hlsl, "int(wp_prop2.x)") {
		t.Fatalf("HLSL property references not rewritten:\n%s", hlsl)
	}
	// Shadowed parameter and block local must remain "color", not "wp_prop0.xyz".
	if !strings.Contains(hlsl, "return (color * 0.5);") || !strings.Contains(hlsl, "(a += color);") {
		t.Fatalf("HLSL local shadowing broken:\n%s", hlsl)
	}
	if !strings.Contains(hlsl, "wp_color") || !strings.Contains(hlsl, "wp_pos") || !strings.Contains(hlsl, "wp_id") {
		t.Fatalf("hlslEntry internals must be wp_-prefixed:\n%s", hlsl)
	}

	msl, err := TranslateProperties(src, MSL, defs)
	if err != nil {
		t.Fatalf("MSL translation failed: %v", err)
	}
	if !strings.Contains(msl, "wp_s.color=wp_u.wp_prop0.xyz;") {
		t.Fatalf("MSL property init missing:\n%s", msl)
	}
}

func TestUnaryParenthesizationAndReservedKeywords(t *testing.T) {
	src := `void mainImage(out vec4 c, vec2 p) {
		float v = 1.0;
		float a = -(-v);
		float b = +(+v);
		bool ok = !(!true);
		c = vec4(a, b, ok ? 1.0 : 0.0, 1.0);
	}`
	for _, target := range []Target{HLSL, MSL} {
		out, err := Translate(src, target)
		if err != nil {
			t.Fatalf("%s failed: %v", target, err)
		}
		if strings.Contains(out, "--") || strings.Contains(out, "++") {
			t.Fatalf("%s glued unary operators:\n%s", target, out)
		}
		if !strings.Contains(out, "-((-(v)))") {
			t.Fatalf("%s missing parenthesized unary minus:\n%s", target, out)
		}
	}
	for _, kw := range []string{"half", "device", "sample", "texture2d", "cbuffer", "threadgroup"} {
		badSrc := "void mainImage(out vec4 c, vec2 p) { float " + kw + " = 1.0; c = vec4(" + kw + "); }"
		if _, err := Translate(badSrc, HLSL); err == nil {
			t.Errorf("accepted reserved keyword %q as identifier", kw)
		}
		badProp := []wpk.Prop{{Key: kw, Type: "slider", Default: json.RawMessage(`0`), Min: ptrFloat(0), Max: ptrFloat(1)}}
		if _, err := TranslateProperties("void mainImage(out vec4 c, vec2 p) { c = vec4(1.0); }", HLSL, badProp); err == nil {
			t.Errorf("accepted reserved keyword %q as property", kw)
		}
	}
}

func TestM08TranslatorSemantics(t *testing.T) {
	src := `
float gUninit;
float gInit = iTime * 0.5;
float pick(float x) { return x; }
vec2 pick(vec2 x) { return x; }
int tag(int x) { return x; }
uint tag(uint x) { return x; }
void mainImage(out vec4 c, vec2 p) {
	vec2 uv = p / iResolution.xy;
	vec2 s1 = step(0.5, uv);
	vec2 s2 = smoothstep(0.1, 0.9, uv);
	float f1 = pick(1.0);
	vec2 v2 = pick(uv);
	ivec2 iv = ivec2(1, 2);
	uvec2 uv2 = uvec2(1u, 2u);
	int a = tag(iv[0]);
	uint b = tag(uv2[1]);
	bool eq = (s1 == s2);
	bool neq = (s1 != v2);
	float z = (eq && !neq) ? (s1.x + s2.y + f1 + float(a) + float(b) + gUninit + gInit) : 0.0;
	c = vec4(vec3(z), 1.0);
}
`
	hlsl, err := Translate(src, HLSL)
	if err != nil {
		t.Fatalf("HLSL failed: %v", err)
	}
	if !strings.Contains(hlsl, "all((s1) == (s2))") || !strings.Contains(hlsl, "any((s1) != (v2))") {
		t.Fatalf("HLSL vector equality translation missing:\n%s", hlsl)
	}
	msl, err := Translate(src, MSL)
	if err != nil {
		t.Fatalf("MSL failed: %v", err)
	}
	if !strings.Contains(msl, "float gUninit = float(0);") || !strings.Contains(msl, "float gInit = float(0);") {
		t.Fatalf("MSL globals not zero-initialized:\n%s", msl)
	}
}

func TestL02SelectNormalizedJSONValue(t *testing.T) {
	defs := []wpk.Prop{{
		Key:     "mode",
		Type:    "select",
		Default: json.RawMessage(`1`),
		Options: []json.RawMessage{
			json.RawMessage(`{"value":0}`),
			json.RawMessage(`{"value":1.0}`),
			json.RawMessage(`{"value":2.0}`),
		},
	}}
	var u Uniforms
	if err := u.Properties(defs, `{"mode":1}`); err != nil {
		t.Fatal(err)
	}
	got := math.Float32frombits(binary.LittleEndian.Uint32(u.Bytes[BaseUniformBytes:]))
	if got != 1 {
		t.Fatalf("select index=%v want 1", got)
	}
}

func TestMetalLayoutSpanningAndClamping(t *testing.T) {
	canvas := image.Rect(0, 0, 10000, 3000)
	viewport := image.Rect(5000, 0, 10000, 3000)
	winRect, vx, vy, vw, vh, pw, ph, err := metalLayout(canvas, viewport, 2.0)
	if err != nil {
		t.Fatal(err)
	}
	if winRect != viewport || vx != -5000 || vy != 0 || vw != 10000 || vh != 3000 {
		t.Fatalf("unexpected spanning frame: win=%v view=(%v,%v,%v,%v)", winRect, vx, vy, vw, vh)
	}
	if pw > maxMetalTextureDim || ph > maxMetalTextureDim || pw != 16384 {
		t.Fatalf("physical Metal dimensions not clamped <= 16384: %dx%d", pw, ph)
	}
}

func TestUpdatePlaybackSendsNormalizedScaleAndPreservesHostOnValidationError(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	var receivedScale float64
	srv := ipc.New(b, func(method string, p json.RawMessage) (any, *ipc.Error) {
		if method == "scale" {
			_ = json.Unmarshal(p, &receivedScale)
			return struct{}{}, nil
		}
		if method == "properties" {
			return nil, &ipc.Error{Code: "E_GPU", Msg: "validation failed"}
		}
		return struct{}{}, nil
	}, nil)
	defer srv.Close()

	r := New("dummy")
	r.conn = ipc.New(a, nil, nil)
	defer r.conn.Close()

	o := render.DefaultPlaybackOptions()
	o.Scale = 0 // should normalize to 0.75 before sending to host
	if r.Capabilities().Scale {
		if err := r.UpdatePlayback(o); err != nil {
			t.Fatal(err)
		}
		if receivedScale != 0.75 {
			t.Fatalf("sent scale=%v want 0.75", receivedScale)
		}
	} else {
		if err := r.UpdatePlayback(o); err == nil {
			t.Fatal("expected unsupported error when Scale capability is disabled")
		}
	}
	if err := r.UpdateProperties(`{"bad":1}`); err == nil {
		t.Fatal("expected validation error")
	}
	if shouldKillOnCallError(&ipc.Error{Code: "E_GPU", Msg: "validation failed"}) {
		t.Fatal("ordinary validation error must not trigger kill")
	}
	if !shouldKillOnCallError(&ipc.Error{Code: "E_TIMEOUT", Msg: "timeout", Fatal: true}) {
		t.Fatal("fatal timeout error must trigger kill")
	}
}

func ptrFloat(v float64) *float64 { return &v }
