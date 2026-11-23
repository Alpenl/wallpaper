package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wpapp/internal/config"
	webwall "wpapp/internal/render/web"
	"wpapp/internal/wpk"
)

func TestHelperDispatchShaderValidateAndPalette(t *testing.T) {
	root := t.TempDir()

	t.Run("shader-validate-valid", func(t *testing.T) {
		src := filepath.Join(root, "ok.frag")
		if err := os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) { c = vec4(1.0); }"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "shader-validate", in: src}, &out, &errBuf)
		if !handled || code != 0 {
			t.Fatalf("code=%d handled=%v stderr=%q", code, handled, errBuf.String())
		}
		var defs []wpk.PropertyDef
		if err := json.Unmarshal(out.Bytes(), &defs); err != nil {
			t.Fatalf("invalid json %q: %v", out.String(), err)
		}
	})

	t.Run("shader-validate-invalid", func(t *testing.T) {
		src := filepath.Join(root, "bad.frag")
		if err := os.WriteFile(src, []byte("void mainImage(out vec4 c, vec2 p) { c = nope(p); }"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "shader-validate", in: src}, &out, &errBuf)
		if !handled || code == 0 {
			t.Fatalf("expected failure for invalid shader, code=%d", code)
		}
	})

	t.Run("shader-validate-oversized", func(t *testing.T) {
		src := filepath.Join(root, "huge.frag")
		huge := append([]byte("void mainImage(out vec4 c, vec2 p) { c = vec4(1.0); }//"), bytes.Repeat([]byte("a"), maxShaderHelperBytes+16)...)
		if err := os.WriteFile(src, huge, 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "shader-validate", in: src}, &out, &errBuf)
		if !handled || code == 0 || !strings.Contains(errBuf.String(), "1 MiB") {
			t.Fatalf("expected 1 MiB rejection, code=%d stderr=%q", code, errBuf.String())
		}
	})

	t.Run("shader-validate-staged-wpk-properties", func(t *testing.T) {
		pkg := filepath.Join(root, "pkg")
		if err := os.MkdirAll(filepath.Join(pkg, "content"), 0o700); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(pkg, "content", "main.frag")
		if err := os.WriteFile(entry, []byte("void mainImage(out vec4 c, vec2 p) { c = vec4(tint, 1.0); }"), 0o600); err != nil {
			t.Fatal(err)
		}
		schema := 0
		m := &wpk.Manifest{
			Format:  "wpk",
			Schema:  &schema,
			ID:      "01234567-89ab-4def-8123-456789abcdef",
			Version: 1,
			Type:    "shader",
			Entry:   "content/main.frag",
			Title:   wpk.I18n{"en": "Tinted"},
			Shader:  &wpk.Shader{Dialect: "shadertoy-glsl"},
			Properties: []wpk.PropertyDef{{
				Key:     "tint",
				Type:    "color",
				Default: json.RawMessage(`"#ff0000"`),
				Label:   wpk.I18n{"en": "Tint"},
			}},
		}
		if err := wpk.WriteManifest(pkg, m); err != nil {
			t.Fatal(err)
		}
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "shader-validate", in: entry}, &out, &errBuf)
		if !handled || code != 0 {
			t.Fatalf("staged wpk validation failed: code=%d stderr=%q", code, errBuf.String())
		}
		var defs []wpk.PropertyDef
		if err := json.Unmarshal(out.Bytes(), &defs); err != nil || len(defs) != 1 || defs[0].Key != "tint" {
			t.Fatalf("unexpected defs: %+v err=%v", defs, err)
		}
	})

	t.Run("palette-red-png", func(t *testing.T) {
		imgPath := filepath.Join(root, "red.png")
		im := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				im.SetRGBA(x, y, color.RGBA{220, 20, 20, 255})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, im); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imgPath, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "palette", in: imgPath}, &out, &errBuf)
		if !handled || code != 0 {
			t.Fatalf("palette helper failed: code=%d stderr=%q", code, errBuf.String())
		}
		var colors []string
		if err := json.Unmarshal(out.Bytes(), &colors); err != nil || len(colors) == 0 || colors[0] != "red" {
			t.Fatalf("expected red palette, got %+v err=%v", colors, err)
		}
	})

	t.Run("unknown-helper", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		code, handled := dispatchHelper(&flags{helper: "bogus"}, &out, &errBuf)
		if !handled || code != 2 {
			t.Fatalf("expected code 2, got code=%d handled=%v", code, handled)
		}
	})
}

func TestWebRendererSetsCacheDir(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	e := &env{exe: "wallpaperd", paths: config.Paths{Cache: cacheDir, Logs: t.TempDir()}}
	r, ok := e.newRenderer("web", config.Defaults(), "display-1").(*webwall.Renderer)
	if !ok {
		t.Fatal("expected *webwall.Renderer")
	}
	if r.CacheDir != cacheDir {
		t.Fatalf("web renderer CacheDir = %q, want %q", r.CacheDir, cacheDir)
	}
}

func TestExportDiagnosticsReadOnlyDoesNotMutateState(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		Config:  filepath.Join(root, "config"),
		Library: filepath.Join(root, "library"),
		Cache:   filepath.Join(root, "cache"),
		Logs:    filepath.Join(root, "logs"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}

	secretTitle := "SuperSecretWallpaperTitle999"
	secretDir := filepath.Join(paths.Library, "item-secret")
	statePath := filepath.Join(paths.Config, "state.json")
	stateData := []byte(`{"schemaVersion":1,"cleanShutdown":false,"safeMode":true,"starts":[1700000000,1700000010,1700000020]}`)
	if err := os.WriteFile(statePath, stateData, 0o600); err != nil {
		t.Fatal(err)
	}
	lib := config.Library{
		SchemaVersion: config.LibraryVersion,
		Items: []config.Item{{
			ID:    "item-secret",
			Title: secretTitle,
			Type:  "video",
			Dir:   secretDir,
			Entry: "content/secret.mp4",
		}},
	}
	if err := config.WriteAtomic(filepath.Join(paths.Config, "library.json"), lib); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteAtomic(filepath.Join(paths.Config, "config.json"), config.Defaults()); err != nil {
		t.Fatal(err)
	}
	logLine := "2026-11-23 INFO loaded title=" + secretTitle + " path=" + filepath.Join(secretDir, "content/secret.mp4") + "\n"
	if err := os.WriteFile(filepath.Join(paths.Logs, "wallpaperd.log"), []byte(logLine), 0o600); err != nil {
		t.Fatal(err)
	}

	beforeInfo, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}

	zipPath, err := exportDiagnosticsReadOnly(paths)
	if err != nil {
		t.Fatalf("exportDiagnosticsReadOnly failed: %v", err)
	}

	afterBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, stateData) {
		t.Fatalf("state.json was mutated by -export-diagnostics:\ngot:  %s\nwant: %s", afterBytes, stateData)
	}
	afterInfo, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("state.json modtime changed: %v -> %v", beforeInfo.ModTime().Format(time.RFC3339Nano), afterInfo.ModTime().Format(time.RFC3339Nano))
	}
	if _, err := os.Stat(filepath.Join(paths.Logs, "logger-status.json")); !os.IsNotExist(err) {
		t.Fatal("read-only export must not write logger-status.json")
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	sawState, sawLog := false, false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.HasSuffix(f.Name, "state.json") {
			sawState = true
		}
		if strings.HasSuffix(f.Name, "wallpaperd.log") {
			sawLog = true
			if strings.Contains(string(data), secretTitle) || strings.Contains(string(data), secretDir) {
				t.Fatalf("secret library title/path leaked into diagnostics log: %s", string(data))
			}
		}
	}
	if !sawState || !sawLog {
		t.Fatalf("expected state.json and wallpaperd.log in zip: sawState=%v sawLog=%v", sawState, sawLog)
	}
}
