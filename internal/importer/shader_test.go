package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"wpapp/internal/wpk"
)

func TestPlainShaderImport(t *testing.T) {
	for _, ext := range []string{".glsl", ".frag"} {
		t.Run(ext, func(t *testing.T) {
			root := t.TempDir()
			library := filepath.Join(root, "library")
			os.MkdirAll(library, 0700)
			src := filepath.Join(root, "waves"+ext)
			os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) { c=vec4(p/iResolution.xy,0.5,1.0); }"), 0600)
			r, e := Import(context.Background(), src, Options{Library: library})
			if e != nil {
				t.Fatal(e)
			}
			if r.Item.Type != "shader" || r.Item.Snapshot == "" {
				t.Fatal(r)
			}
			if _, e = wpk.LoadDir(r.Item.Dir); e != nil {
				t.Fatal(e)
			}
			os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) { c=unknown(p); }"), 0600)
			if _, e = Import(context.Background(), src, Options{Library: library}); e == nil || !strings.Contains(e.Error(), "line") {
				t.Fatal("invalid shader accepted", e)
			}
			entries, _ := os.ReadDir(library)
			if len(entries) != 1 {
				t.Fatal("failed import published package", entries)
			}
		})
	}
}

func TestInvalidShaderReplacementKeepsOriginal(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "library")
	os.MkdirAll(library, 0700)
	src := filepath.Join(root, "source.frag")
	os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) {c=vec4(1.0);}"), 0600)
	r, e := Import(context.Background(), src, Options{Library: library})
	if e != nil {
		t.Fatal(e)
	}
	staged := filepath.Join(root, "replacement")
	os.MkdirAll(filepath.Join(staged, "content"), 0700)
	for _, name := range []string{"manifest.json", "preview.png"} {
		b, e := os.ReadFile(filepath.Join(r.Item.Dir, name))
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(staged, name), b, 0600)
	}
	os.WriteFile(filepath.Join(staged, "content/main.frag"), []byte("#include \"secret\""), 0600)
	called := false
	_, e = Import(context.Background(), staged, Options{Library: library, BeforeReplace: func(string) error { called = true; return nil }})
	if e == nil || called {
		t.Fatal("bad shader reached replacement", e, called)
	}
	b, e := os.ReadFile(filepath.Join(r.Item.Dir, r.Item.Entry))
	if e != nil || !strings.Contains(string(b), "mainImage") {
		t.Fatal("original destroyed", e)
	}
}

// C-04 + L-06: Reject standalone shader > 1 MiB explicitly and route untrusted
// shader validation through Options.ValidateShader (standalone and WPK).
func TestValidateShaderCallbackAndSizeLimit(t *testing.T) {
	t.Run("reject-over-1MiB", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "huge.frag")
		huge := append([]byte("void mainImage(out vec4 c, vec2 p) { c=vec4(1.0); }//"), bytes.Repeat([]byte("a"), (1<<20)+16)...)
		if err := os.WriteFile(src, huge, 0o600); err != nil {
			t.Fatal(err)
		}
		called := false
		_, err := Import(context.Background(), src, Options{
			Library: filepath.Join(root, "lib"),
			ValidateShader: func(string) ([]wpk.PropertyDef, error) {
				called = true
				return nil, nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), "1 MiB") {
			t.Fatalf("expected >1 MiB error, got %v", err)
		}
		if called {
			t.Fatal("ValidateShader should not be invoked for oversized file")
		}
	})

	t.Run("standalone-uses-ValidateShader", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "custom.glsl")
		// Intentionally non-standard GLSL that in-process translator would reject:
		// proves ValidateShader callback is used instead of in-process translator.
		if err := os.WriteFile(src, []byte("/* validated by isolated helper */"), 0o600); err != nil {
			t.Fatal(err)
		}
		var calls int
		var gotPath string
		prop := wpk.PropertyDef{Key: "speed", Type: "bool", Default: json.RawMessage(`true`), Label: wpk.I18n{"en": "Speed"}}
		res, err := Import(context.Background(), src, Options{
			Library: filepath.Join(root, "lib"),
			ValidateShader: func(p string) ([]wpk.PropertyDef, error) {
				calls++
				gotPath = p
				return []wpk.PropertyDef{prop}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || gotPath != src {
			t.Fatalf("ValidateShader calls=%d path=%q want 1, %q", calls, gotPath, src)
		}
		if len(res.Item.PropertyDefs) != 1 || res.Item.PropertyDefs[0].Key != "speed" {
			t.Fatalf("expected extracted PropertyDefs, got %+v", res.Item.PropertyDefs)
		}
	})

	t.Run("wpk-validates-staged-before-BeforeReplace", func(t *testing.T) {
		root := t.TempDir()
		lib := filepath.Join(root, "lib")
		src := filepath.Join(root, "good.frag")
		_ = os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) { c=vec4(1.0); }"), 0o600)
		first, err := Import(context.Background(), src, Options{Library: lib})
		if err != nil {
			t.Fatal(err)
		}
		staged := filepath.Join(root, "update")
		_ = os.MkdirAll(filepath.Join(staged, "content"), 0o700)
		for _, name := range []string{"manifest.json", "preview.png", "content/main.frag"} {
			b, _ := os.ReadFile(filepath.Join(first.Item.Dir, name))
			_ = os.WriteFile(filepath.Join(staged, name), b, 0o600)
		}
		replaceCalled := false
		var validatedPath string
		_, err = Import(context.Background(), staged, Options{
			Library: lib,
			ValidateShader: func(p string) ([]wpk.PropertyDef, error) {
				validatedPath = p
				return nil, errors.New("helper rejected shader")
			},
			BeforeReplace: func(string) error {
				replaceCalled = true
				return nil
			},
		})
		if err == nil || replaceCalled {
			t.Fatalf("expected helper rejection before BeforeReplace: err=%v replaceCalled=%v", err, replaceCalled)
		}
		if validatedPath == "" || !strings.HasSuffix(filepath.ToSlash(validatedPath), "/content/main.frag") {
			t.Fatalf("unexpected validated path: %q", validatedPath)
		}
	})
}

// L-06 + M-04: Importing a shader WPK with author-provided preview.png must NOT
// overwrite preview.png, and must extract palette colors via Options.PreviewColors.
func TestShaderWPKPreservesAuthorPreviewAndExtractsColors(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	pkg := filepath.Join(root, "pkg")
	_ = os.MkdirAll(filepath.Join(pkg, "content"), 0o700)
	_ = os.WriteFile(filepath.Join(pkg, "content/main.frag"), []byte("void mainImage(out vec4 c, vec2 p) { c=vec4(1.0); }"), 0o600)
	schema := 0
	m := &wpk.Manifest{
		Format:  "wpk",
		Schema:  &schema,
		ID:      NewID(),
		Version: 1,
		Type:    "shader",
		Entry:   "content/main.frag",
		Title:   wpk.I18n{"en": "Author Preview"},
		Shader:  &wpk.Shader{Dialect: "shadertoy-glsl"},
	}
	if err := wpk.WriteManifest(pkg, m); err != nil {
		t.Fatal(err)
	}
	// Create a distinct solid red PNG as the author-provided preview.png.
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			im.SetRGBA(x, y, color.RGBA{220, 20, 20, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, im); err != nil {
		t.Fatal(err)
	}
	authorPNG := pngBuf.Bytes()
	if err := os.WriteFile(filepath.Join(pkg, "preview.png"), authorPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotCalled := false
	colorCalled := false
	res, err := Import(context.Background(), pkg, Options{
		Library: lib,
		ShaderSnapshot: func(string, string) error {
			snapshotCalled = true
			return nil
		},
		PreviewColors: func(p string) []string {
			colorCalled = true
			return PreviewColors(p)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshotCalled {
		t.Fatal("ShaderSnapshot should not overwrite author-provided preview.png")
	}
	gotPreview, err := os.ReadFile(filepath.Join(res.Item.Dir, "preview.png"))
	if err != nil || !bytes.Equal(gotPreview, authorPNG) {
		t.Fatal("author-provided preview.png was modified or missing")
	}
	if !colorCalled || len(res.Item.Colors) == 0 || res.Item.Colors[0] != "red" {
		t.Fatalf("expected palette colors via PreviewColors callback, called=%v colors=%v", colorCalled, res.Item.Colors)
	}
}
