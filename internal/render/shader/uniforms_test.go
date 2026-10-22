package shader

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
	"time"
	"wpapp/internal/wpk"
)

func TestUniformPacking(t *testing.T) {
	var u Uniforms
	defs := []wpk.Prop{{Key: "tint", Type: "color", Default: json.RawMessage(`"#ff8000"`)}, {Key: "enabled", Type: "bool", Default: json.RawMessage(`true`)}}
	if e := u.Properties(defs, ""); e != nil {
		t.Fatal(e)
	}
	u.Frame(800, 600, 2, .03, 7, time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC))
	get := func(offset int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(u.Bytes[offset:])) }
	if u.Size != BaseUniformBytes+32 || u.Size%16 != 0 || get(0) != 800 || get(4) != 600 || get(12) != 2 || binary.LittleEndian.Uint32(u.Bytes[20:]) != 7 || get(48) != 2026 || get(52) != 9 || get(BaseUniformBytes) != 1 || get(BaseUniformBytes+16) != 1 {
		t.Fatal("uniform layout mismatch", u)
	}
	for i := 32; i < 48; i++ {
		if u.Bytes[i] != 0 {
			t.Fatal("mouse must remain zero")
		}
	}
	for _, target := range []Target{MSL, HLSL} {
		src, e := TranslateProperties("void mainImage(out vec4 c, vec2 p) {c=vec4(tint,enabled ? 1.0 : 0.0);}", target, defs)
		if e != nil || src == "" {
			t.Fatal(e)
		}
	}
}
