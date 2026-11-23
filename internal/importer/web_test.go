package importer

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/wpk"
)

func TestExampleWebImportAndConsent(t *testing.T) {
	o := opts(t, "darwin")
	called := 0
	o.ConsentNetwork = func(string, []string) bool { called++; return true }
	r, e := Import(context.Background(), "../../examples/web-orbit", o)
	if e != nil {
		t.Fatal(e)
	}
	if r.Item.Type != "web" || r.Item.NetworkGranted || len(r.Item.PropertyDefs) != 5 || called != 0 {
		t.Fatal(r)
	}
	m, e := wpk.LoadDir(r.Item.Dir)
	if e != nil {
		t.Fatal(e)
	}
	m.Web.Permissions = []string{"network"}
	m.Web.NetworkAllow = []string{"api.example.com"}
	if e = wpk.WriteManifest(r.Item.Dir, m); e != nil {
		t.Fatal(e)
	}
	// Separate library avoids updating a directory while using it as the source.
	o2 := opts(t, "windows")
	o2.ConsentNetwork = func(title string, hosts []string) bool {
		called++
		if len(hosts) != 1 || hosts[0] != "api.example.com" {
			t.Fatal(hosts)
		}
		return true
	}
	granted, e := Import(context.Background(), r.Item.Dir, o2)
	if e != nil {
		t.Fatal(e)
	}
	if !granted.Item.NetworkGranted || called != 1 {
		t.Fatal(granted)
	}
	db := filepath.Join(t.TempDir(), "library.json")
	if e = config.WriteAtomic(db, config.Library{SchemaVersion: config.LibraryVersion, Items: []config.Item{granted.Item}}); e != nil {
		t.Fatal(e)
	}
	var library config.Library
	if e = config.Load(db, config.LibraryVersion, nil, &library); e != nil {
		t.Fatal(e)
	}
	if !library.Items[0].NetworkGranted {
		t.Fatal("consent not persisted")
	}
	o2.ConsentNetwork = nil
	denied, e := Import(context.Background(), r.Item.Dir, o2)
	if e != nil {
		t.Fatal(e)
	}
	if denied.Item.NetworkGranted {
		t.Fatal("reimport silently retained consent")
	}
}
func TestWebFolderSafety(t *testing.T) {
	for _, kind := range []string{"symlink", "executable", "traversal", "audio"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0644)
			data := map[string]any{"title": "test", "type": "web", "file": "index.html"}
			switch kind {
			case "symlink":
				if e := os.Symlink("/etc/passwd", filepath.Join(dir, "escape")); e != nil {
					t.Skip(e)
				}
			case "executable":
				_ = os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("MZevil"), 0644)
			case "traversal":
				data["file"] = "../index.html"
			case "audio":
				data["general"] = map[string]any{"supportsaudioprocessing": true}
			}
			b, _ := json.Marshal(data)
			_ = os.WriteFile(filepath.Join(dir, "project.json"), b, 0644)
			if _, e := Import(context.Background(), dir, opts(t, "darwin")); e == nil {
				t.Fatal("unsafe package accepted")
			}
		})
	}
}
func TestLivelyWebProperties(t *testing.T) {
	dir := t.TempDir()
	writeLively(t, dir, `"web"`, "index.html")
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "LivelyProperties.json"), []byte(`{"caption":{"type":"textbox","value":"hi","text":"Caption"},"choice":{"type":"dropdown","value":1,"items":["A","B"]},"clock":{"type":"checkbox","value":true}}`), 0644)
	r, e := Import(context.Background(), dir, opts(t, "darwin"))
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Item.PropertyDefs) != 3 {
		t.Fatal(r)
	}
}

// M-02: Wallpaper Engine combo string labels, continuous slider step=0, HDR colors,
// and malformed/unsupported properties warn and skip instead of failing import.
func TestWEPropertiesComboStep0HDRAndMalformed(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)
	project := `{
		"title": "WE Rich Props",
		"type": "web",
		"file": "index.html",
		"general": {
			"properties": {
				"mode": {
					"type": "combo",
					"text": "Mode",
					"value": "a",
					"options": [
						{"label": "Alpha", "value": "a"},
						{"label": "Beta", "value": "b"}
					]
				},
				"speed": {
					"type": "slider",
					"text": "Speed",
					"value": 0.5,
					"min": 0,
					"max": 2,
					"step": 0
				},
				"glow": {
					"type": "color",
					"text": "HDR Glow",
					"value": "1.8 -0.2 0.5"
				},
				"bad-hyphen-key": {
					"type": "bool",
					"text": "Invalid Key",
					"value": true
				},
				"missing_default": {
					"type": "bool",
					"text": "Missing Value"
				},
				"unsupported_kind": {
					"type": "file",
					"text": "File Picker",
					"value": "x.png"
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Import(context.Background(), dir, opts(t, "windows"))
	if err != nil {
		t.Fatalf("import failed on valid WE combo/step=0/HDR/malformed props: %v", err)
	}
	if len(r.Item.PropertyDefs) != 3 {
		t.Fatalf("expected 3 valid properties, got %d: %+v", len(r.Item.PropertyDefs), r.Item.PropertyDefs)
	}
	if len(r.Warnings) < 3 {
		t.Fatalf("expected warnings for malformed/unsupported properties, got %v", r.Warnings)
	}
	byKey := map[string]wpk.Prop{}
	for _, p := range r.Item.PropertyDefs {
		byKey[p.Key] = p
	}
	if byKey["glow"].Default == nil || string(byKey["glow"].Default) != `"#ff0080"` {
		t.Fatalf("HDR color not clamped to #ff0080: %s", string(byKey["glow"].Default))
	}
	if byKey["speed"].Step != nil {
		t.Fatalf("step=0 should become nil, got %v", *byKey["speed"].Step)
	}
	var opt0 struct {
		Value string   `json:"value"`
		Label wpk.I18n `json:"label"`
	}
	if len(byKey["mode"].Options) != 2 || json.Unmarshal(byKey["mode"].Options[0], &opt0) != nil || opt0.Value != "a" || opt0.Label["en"] != "Alpha" {
		t.Fatalf("combo option label not converted to I18n: %s", string(byKey["mode"].Options[0]))
	}
}

// M-03: Backslashes in foreign JSON relative paths are normalized to '/' and
// LivelyInfo.json / project.json are located case-insensitively.
func TestForeignBackslashAndCaseInsensitiveMetadata(t *testing.T) {
	t.Run("we-web-backslash-and-upper-project-json", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, "web"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("<html></html>"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "Project.JSON"), []byte(`{"title":"Backslash","type":"web","file":"web\\index.html"}`), 0o644)
		r, err := Import(context.Background(), dir, opts(t, "linux"))
		if err != nil {
			t.Fatal(err)
		}
		if r.Item.Entry != "content/web/index.html" {
			t.Fatalf("entry not normalized: %q", r.Item.Entry)
		}
	})

	t.Run("lively-zip-lowercase-info-and-backslash", func(t *testing.T) {
		zp := filepath.Join(t.TempDir(), "lively.zip")
		f, err := os.Create(zp)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, _ := zw.Create("livelyinfo.json")
		_, _ = w.Write([]byte(`{"Title":"Lower","Type":"video","FileName":"sub\\v.mp4"}`))
		w, _ = zw.Create("sub/v.mp4")
		b, _ := os.ReadFile(fixture("h264_audio.mp4"))
		_, _ = w.Write(b)
		_ = zw.Close()
		_ = f.Close()
		r, err := Import(context.Background(), zp, opts(t, "linux"))
		if err != nil || r.Item.Title != "Lower" {
			t.Fatalf("lively lowercase/backslash zip import failed: %v %+v", err, r)
		}
	})
}

// L-06 + M-04: Web packages extract palette colors via Options.PreviewColors.
func TestWebImportExtractsPreviewColorsViaOption(t *testing.T) {
	o := opts(t, "windows")
	called := ""
	o.PreviewColors = func(p string) []string {
		called = p
		return []string{"blue", "蓝色"}
	}
	r, err := Import(context.Background(), "../../examples/web-orbit", o)
	if err != nil {
		t.Fatal(err)
	}
	if called == "" || len(r.Item.Colors) != 2 || r.Item.Colors[0] != "blue" {
		t.Fatalf("PreviewColors option not used for web package: called=%q colors=%v", called, r.Item.Colors)
	}
}
