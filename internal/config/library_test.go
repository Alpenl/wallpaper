package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	p := Paths{Config: t.TempDir(), Library: filepath.Join(t.TempDir(), "library"),
		Cache: filepath.Join(t.TempDir(), "cache"), Logs: filepath.Join(t.TempDir(), "logs")}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	return p
}

func readLibrary(t *testing.T, p Paths) Library {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.Config, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	var l Library
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func readAssignments(t *testing.T, p Paths) Assignments {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.Config, "assignments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a Assignments
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOldLibraryDataIsDiscardedAndResetPersisted(t *testing.T) {
	p := testPaths(t)
	library := `{"schemaVersion":4,"items":[
		{"id":"video-1","type":"video","title":"Old"},
		{"id":"web-1","type":"web","title":"Old"}]}`
	assignments := `{"schemaVersion":3,"layout":"span","default":"web-1","spanItem":"video-1",
		"displays":{"d1":"web-1"},"options":{"d1":{"fit":"fit"}},"aliases":{"d1":"Desk"}}`
	if err := os.WriteFile(filepath.Join(p.Config, "library.json"), []byte(library), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Config, "assignments.json"), []byte(assignments), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Library.SchemaVersion != LibraryVersion || len(s.Library.Items) != 0 {
		t.Fatalf("old library must not be imported: %+v", s.Library)
	}
	if s.Assign.Default != "" || s.Assign.SpanItem != "" || s.Assign.Layout != "" || len(s.Assign.Displays) != 0 || len(s.Assign.Options) != 0 || len(s.Assign.Aliases) != 0 {
		t.Fatalf("old assignments must not be imported: %+v", s.Assign)
	}
	// The reset must reach disk before an import can write a v5 library while
	// leaving the old assignments file behind.
	if l := readLibrary(t, p); l.SchemaVersion != LibraryVersion || len(l.Items) != 0 {
		t.Fatalf("old library not reset on disk: %+v", l)
	}
	if a := readAssignments(t, p); a.Default != "" || a.SpanItem != "" || a.Layout != "" || len(a.Displays) != 0 || len(a.Options) != 0 || len(a.Aliases) != 0 {
		t.Fatalf("old assignments not reset on disk: %+v", a)
	}
	// A second start must not see the reset library as old or load old assignments.
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Library.Items) != 0 || len(s2.Assign.Displays) != 0 || s2.Assign.Default != "" {
		t.Fatalf("reset data reappeared on second start: %+v %+v", s2.Library, s2.Assign)
	}
}

func TestCurrentLibraryLoadsNormally(t *testing.T) {
	p := testPaths(t)
	library := `{"schemaVersion":5,"items":[{"id":"video-1","type":"video","title":"Keep"}]}`
	assignments := `{"schemaVersion":3,"default":"video-1","displays":{"d1":"video-1"}}`
	if err := os.WriteFile(filepath.Join(p.Config, "library.json"), []byte(library), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Config, "assignments.json"), []byte(assignments), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Library.Items) != 1 || s.Library.Items[0].ID != "video-1" {
		t.Fatalf("current library lost: %+v", s.Library)
	}
	if s.Assign.Default != "video-1" || s.Assign.Displays["d1"] != "video-1" {
		t.Fatalf("current assignments lost: %+v", s.Assign)
	}
}

func TestNewerLibraryIsReadOnly(t *testing.T) {
	p := testPaths(t)
	library := `{"schemaVersion":6,"items":[{"id":"future","type":"video","title":"Future"}]}`
	path := filepath.Join(p.Config, "library.json")
	if err := os.WriteFile(path, []byte(library), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LibrarySaver.ReadOnly || len(s.ReadOnlyWarn) == 0 {
		t.Fatalf("newer library must be read-only: %+v", s.ReadOnlyWarn)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != library {
		t.Fatalf("newer library was modified: %s", b)
	}
}

func TestUnreadableLibraryIsNotTreatedAsOld(t *testing.T) {
	p := testPaths(t)
	// A directory at the library path makes ReadFile fail with a non-NotExist
	// error. Open must report it instead of silently resetting the data.
	if err := os.Mkdir(filepath.Join(p.Config, "library.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("unreadable library should fail to open")
	}
	if _, err := os.Stat(filepath.Join(p.Config, "library.json")); err != nil {
		t.Fatalf("unreadable library path was removed: %v", err)
	}
}
