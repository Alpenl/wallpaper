package shader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranslatorGoldens(t *testing.T) {
	files, e := filepath.Glob("testdata/*.glsl")
	if e != nil || len(files) < 15 {
		t.Fatal("at least 15 fixtures required", e)
	}
	for _, f := range files {
		for _, target := range []Target{HLSL, MSL} {
			t.Run(filepath.Base(f)+string(target), func(t *testing.T) {
				b, e := os.ReadFile(f)
				if e != nil {
					t.Fatal(e)
				}
				got, e := Translate(string(b), target)
				if e != nil {
					t.Fatal(e)
				}
				path := strings.TrimSuffix(f, ".glsl") + "." + string(target)
				if os.Getenv("UPDATE_SHADER_GOLDENS") == "1" {
					if e := os.WriteFile(path, []byte(got), 0600); e != nil {
						t.Fatal(e)
					}
				}
				want, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if got != string(want) {
					t.Fatalf("golden mismatch: %s", path)
				}
			})
		}
	}
}
func TestTranslationErrors(t *testing.T) {
	for _, src := range []string{"#include \"secret\"", "#define A A\nvoid mainImage(out vec4 c, vec2 p) {c=vec4(A);}", "void mainImage(out vec4 c, vec2 p) {\n c=vec4(unknown); }", "void mainImage(out vec4 c, vec2 p) {\n samplerCube x; }", "void mainImage(out vec4 c, vec2 p) { c=texture(foo,p); }", "void main() {}", "void mainImage(out vec4 c, vec2 p) {"} {
		for _, target := range []Target{HLSL, MSL} {
			if _, e := Translate(src, target); e == nil || !strings.Contains(e.Error(), "line ") {
				t.Fatalf("expected line error: %q: %v", src, e)
			}
		}
	}
}
