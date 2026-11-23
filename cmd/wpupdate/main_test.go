package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyPlatformEnforcement(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	dist := filepath.Join(dir, "dist")
	feed := filepath.Join(dir, "feed")

	if err := keygen([]string{"-o", keys}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "wallpaperd.exe"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := manifest([]string{
		"-dir", dist,
		"-version", "0.2.0",
		"-platform", "windows-amd64",
		"-key", filepath.Join(keys, "update.key"),
		"-out", feed,
	}); err != nil {
		t.Fatal(err)
	}

	// Missing -platform must fail.
	if err := verify([]string{"-feed", feed, "-pub", filepath.Join(keys, "update.pub")}); err == nil {
		t.Fatal("verify accepted empty -platform")
	}

	// Matching platform succeeds.
	if err := verify([]string{"-feed", feed, "-platform", "windows-amd64", "-pub", filepath.Join(keys, "update.pub")}); err != nil {
		t.Fatalf("verify failed for valid platform: %v", err)
	}

	// Copy signed windows-amd64 manifest under linux-amd64 directory: verify must reject mismatch.
	wrongDir := filepath.Join(feed, "linux-amd64")
	if err := os.MkdirAll(wrongDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "manifest.json.sig", "wallpaperd.exe"} {
		if err := copyFile(filepath.Join(feed, "windows-amd64", name), filepath.Join(wrongDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	err := verify([]string{"-feed", feed, "-platform", "linux-amd64", "-pub", filepath.Join(keys, "update.pub")})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected platform mismatch error, got %v", err)
	}
}
