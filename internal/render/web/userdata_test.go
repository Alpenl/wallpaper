package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserDataDirSweepAndClose(t *testing.T) {
	cacheDir := t.TempDir()
	base := filepath.Join(cacheDir, "webview")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	// Stale directory with dead PID should be swept on startup.
	staleDir := filepath.Join(base, userDataPrefix+"stale")
	if err := os.MkdirAll(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, userDataLockFile), []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Active directory belonging to the current process must NOT be swept.
	active, err := prepareUserDataDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale user-data dir was not swept: %v", err)
	}
	second, err := prepareUserDataDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active.path); err != nil {
		t.Fatalf("active user-data dir was mistakenly swept: %v", err)
	}
	activePath := active.path
	secondPath := second.path
	active.Close()
	second.Close()
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("active dir not removed on Close: %v", err)
	}
	if _, err := os.Stat(secondPath); !os.IsNotExist(err) {
		t.Fatalf("second dir not removed on Close: %v", err)
	}
}
