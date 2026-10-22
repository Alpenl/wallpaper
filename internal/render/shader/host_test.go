package shader

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"wpapp/internal/wpk"
)

type fakeGPU struct {
	frames int
	fail   error
}

func (g *fakeGPU) Load(loadRequest, string) error            { return nil }
func (g *fakeGPU) Frame(*Uniforms, float64) (float64, error) { g.frames++; return 5, g.fail }
func (g *fakeGPU) Pump()                                     {}
func (g *fakeGPU) Close()                                    {}
func TestHostPauseAndFailure(t *testing.T) {
	g := &fakeGPU{}
	h := hostSession{gpu: g, loaded: true, adaptive: NewAdaptive(.75), width: 100, height: 100, fps: 30}
	h.uniforms.Properties(nil, "")
	if e := h.frame(time.Unix(0, 0)); e != nil {
		t.Fatal(e)
	}
	for _, level := range []string{"1", "2", "3"} {
		if e := h.control("pause", json.RawMessage(level)); e != nil {
			t.Fatal(e)
		}
		h.frame(time.Unix(100, 0))
	}
	if g.frames != 1 {
		t.Fatal("presented while paused")
	}
	h.control("pause", json.RawMessage(`0`))
	g.fail = errors.New("device removed")
	if e := h.frame(time.Unix(200, 0)); e == nil {
		t.Fatal("GPU failure swallowed")
	}
}
func TestFramePolicyAllocations(t *testing.T) {
	h := hostSession{gpu: &fakeGPU{}, loaded: true, adaptive: NewAdaptive(.75), width: 100, height: 100}
	h.uniforms.Properties([]wpk.Prop{}, "")
	n := time.Now()
	if got := testing.AllocsPerRun(100, func() { h.frame(n) }); got != 0 {
		t.Fatal("frame policy allocated", got)
	}
}
func TestMalformedShaderNeverPanics(t *testing.T) {
	for _, src := range []string{"/* unclosed", "#define M(\n", "#define M() 1\nvoid mainImage(out vec4 c, vec2 p){c=vec4(M());}", "void mainImage(out vec4 c, vec2 p){ c = ; }"} {
		for _, target := range []Target{MSL, HLSL} {
			_, _ = Translate(src, target)
		}
	}
}
