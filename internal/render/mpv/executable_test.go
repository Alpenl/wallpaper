package mpv

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"wpapp/internal/render"
)

func TestResolveExecutableRejectsMissingFilesAndDirectories(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "missing-mpv.exe"), t.TempDir()} {
		if _, err := ResolveExecutable(path); err == nil {
			t.Fatalf("accepted unusable executable %q", path)
		}
	}
	path := filepath.Join(t.TempDir(), "mpv.exe")
	if err := os.WriteFile(path, []byte("validation only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveExecutable("  " + path + "  "); err != nil || got != path {
		t.Fatalf("resolve existing executable: %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveExecutable(path); err == nil {
			t.Fatal("accepted a file without execute permission")
		}
	}
}

func TestMissingExecutableLoadProvidesSetupInstructions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-mpv.exe")
	r := New(Options{Path: path})
	err := r.Load(render.Item{Path: "unused.mp4"}, fakeSurface{}, false)
	if err == nil {
		t.Fatal("missing mpv must not report a successful load")
	}
	for _, want := range []string{path, "install mpv separately", "Playback & Performance > mpv path", "mpv.exe", "DLL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing actionable detail %q: %v", want, err)
		}
	}
	cause := errors.New("missing runtime dependency")
	if !errors.Is(executableError(path, cause), cause) {
		t.Fatal("setup guidance must preserve the underlying error")
	}
	if r.cmd != nil {
		t.Fatal("missing executable must not leave a running process")
	}
}
