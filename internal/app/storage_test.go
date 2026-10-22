package app

import (
	"os"
	"path/filepath"
	"testing"

	"wpapp/internal/config"
)

func TestStorageScanAndSafeCacheCleanup(t *testing.T) {
	e := setup(t, 1, true)
	paths := e.store.Paths
	for _, p := range []string{filepath.Join(paths.Library, "x", "data"), filepath.Join(paths.Cache, "x", "first.png"), filepath.Join(paths.Cache, "orphan", "old.png")} {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("12345"), 0600)
	}
	e.a.RefreshStorage()
	eventually(t, "scan", func() bool {
		v := e.a.View()
		return !v.Storage.Scanning && v.Storage.LibraryBytes == 5 && v.Storage.CacheBytes == 10
	})
	e.a.ClearCache()
	eventually(t, "orphan removed", func() bool { _, err := os.Stat(filepath.Join(paths.Cache, "orphan")); return os.IsNotExist(err) })
	if _, err := os.Stat(filepath.Join(paths.Cache, "x", "first.png")); err != nil {
		t.Fatal("live snapshot removed", err)
	}
	if managedChild(paths.Library, paths.Library) || managedChild(paths.Library, filepath.Dir(paths.Library)) || !managedChild(paths.Library, filepath.Join(paths.Library, "x")) {
		t.Fatal("unsafe root containment")
	}
}

func TestRemoveNeverDeletesExternalDirectory(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	e := setupWith(t, 0, false, func(d *Deps) {
		d.Store.Library.Upsert(config.Item{ID: "external", Dir: outside, Type: "image", Entry: "keep"})
	})
	e.a.Remove("external")
	eventually(t, "removed library record", func() bool { return len(e.a.View().Library) == 0 })
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("external file deleted", err)
	}
}

func TestTombstoneCleanupCannotDeleteReimportedIdentity(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "item")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "old"), []byte("old"), 0600)
	removed, err := tombstone(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0755)
	fresh := filepath.Join(dir, "fresh")
	os.WriteFile(fresh, []byte("new"), 0600)
	if err := os.RemoveAll(removed); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(fresh); err != nil || string(b) != "new" {
		t.Fatal("replacement lost", err)
	}
}
