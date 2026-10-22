//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainsAtom(t *testing.T) {
	if !containsAtom(u32le(1, 7, 20), 7) || containsAtom(u32le(1, 7, 20), 8) || containsAtom([]byte{1, 2, 3}, 1) {
		t.Fatal("invalid supported-atom lookup")
	}
}

func TestForegroundSameMachine(t *testing.T) {
	for _, tc := range []struct {
		client, local string
		want          bool
	}{
		{"desktop", "desktop", true}, {"Desktop.\x00", "desktop", true},
		{"remote", "desktop", false}, {"", "desktop", false},
		{"localhost", "desktop", false}, {"desktop.example", "desktop", false},
	} {
		if got := sameMachine(tc.client, tc.local); got != tc.want {
			t.Errorf("sameMachine(%q, %q) = %v", tc.client, tc.local, got)
		}
	}
}

func TestForegroundProcessExecutable(t *testing.T) {
	dir := t.TempDir()
	if got := processExecutable(dir, 0); got != "" {
		t.Fatalf("PID 0 = %q", got)
	}
	if got := processExecutable(dir, 123); got != "" {
		t.Fatalf("missing process = %q", got)
	}
	proc := filepath.Join(dir, "123")
	if err := os.Mkdir(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/private/home/user/apps/My Player (deleted)", filepath.Join(proc, "exe")); err != nil {
		t.Fatal(err)
	}
	if got := processExecutable(dir, 123); got != "My Player" {
		t.Fatalf("identity must contain only executable basename, got %q", got)
	}
}

func TestForegroundDisabledOnWayland(t *testing.T) {
	for _, tc := range []struct{ session, display string }{
		{"wayland", ""}, {"x11", "wayland-0"}, {"", "wayland-1"},
	} {
		t.Run(tc.session+tc.display, func(t *testing.T) {
			t.Setenv("XDG_SESSION_TYPE", tc.session)
			t.Setenv("WAYLAND_DISPLAY", tc.display)
			h := New(Options{})
			h.initForeground()
			if h.Caps().ForegroundApp {
				t.Fatal("XWayland cannot identify native Wayland foreground apps")
			}
		})
	}
}

func TestForegroundCurrentProcess(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := processExecutable("/proc", uint32(os.Getpid())); got != filepath.Base(path) {
		t.Fatalf("current process basename = %q, want %q", got, filepath.Base(path))
	}
}
