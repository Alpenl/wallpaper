package importer

import (
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
