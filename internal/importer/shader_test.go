package importer

import (
	"context"
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
