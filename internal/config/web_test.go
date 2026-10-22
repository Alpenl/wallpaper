package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryWebMigration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "library.json")
	old := []byte(`{"schemaVersion":3,"items":[{"id":"a","networkGranted":true,"props":{"fit":"fit","speed":1.5}}]}`)
	if e := os.WriteFile(p, old, 0644); e != nil {
		t.Fatal(e)
	}
	var l Library
	if e := Load(p, LibraryVersion, libraryMigrations, &l); e != nil {
		t.Fatal(e)
	}
	if l.SchemaVersion != 4 || l.Items[0].NetworkGranted || l.Items[0].Props.Speed != 1.5 {
		t.Fatal(l)
	}
	if b, e := os.ReadFile(p + ".v3"); e != nil || string(b) != string(old) {
		t.Fatal("missing migration backup", e)
	}
	l.Items[0].NetworkGranted = true
	l.Items[0].Props.WebValues = `{"caption":"hello"}`
	if e := WriteAtomic(p, l); e != nil {
		t.Fatal(e)
	}
	var next Library
	if e := Load(p, LibraryVersion, libraryMigrations, &next); e != nil {
		t.Fatal(e)
	}
	if !next.Items[0].NetworkGranted || next.Items[0].Props.WebValues != l.Items[0].Props.WebValues {
		t.Fatal("lost consent/properties")
	}
}
