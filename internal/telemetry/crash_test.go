package telemetry

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestCrashCapture(t *testing.T) {
	if dir := os.Getenv("WP_CRASH_FIXTURE"); dir != "" {
		if err := InstallCrashOutput(dir, "fixture"); err != nil {
			panic(err)
		}
		panic("captured crash fixture")
	}
	dir := t.TempDir()
	for range 6 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashCapture$")
		cmd.Env = append(os.Environ(), "WP_CRASH_FIXTURE="+dir)
		if cmd.Run() == nil {
			t.Fatal("expected crash")
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "crash-fixture-*.log"))
	// The latest process crashes after pruning five previous crash reports.
	if len(files) != 6 {
		t.Fatal(files)
	}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil || !strings.Contains(string(b), "captured crash fixture") {
			t.Fatal(p, e, string(b))
		}
	}
}

func TestCrashRetention(t *testing.T) {
	for _, tc := range []struct {
		name            string
		empty, nonempty int
	}{
		{"empty runs preserve real crash", 10, 1},
		{"newest five real crashes", 0, 7},
		{"current file survives", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Close the runtime's duplicate before TempDir cleanup on Windows.
			t.Cleanup(func() {
				if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil {
					t.Error(err)
				}
			})
			for i := range tc.nonempty + tc.empty {
				var data []byte
				if i < tc.nonempty {
					data = []byte("real crash")
				}
				path := filepath.Join(dir, fmt.Sprintf("crash-old-%02d.log", i))
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				mtime := time.Unix(int64(i), 0)
				if err := os.Chtimes(path, mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}
			if err := InstallCrashOutput(dir, "current"); err != nil {
				t.Fatal(err)
			}
			for i := range tc.nonempty + tc.empty {
				path := filepath.Join(dir, fmt.Sprintf("crash-old-%02d.log", i))
				_, err := os.Stat(path)
				keep := i < tc.nonempty && i >= tc.nonempty-5
				if keep && err != nil {
					t.Errorf("retained crash %s: %v", path, err)
				}
				if !keep && !os.IsNotExist(err) {
					t.Errorf("expected removal of %s, stat: %v", path, err)
				}
			}
			current, err := filepath.Glob(filepath.Join(dir, "crash-current-*.log"))
			if err != nil || len(current) != 1 {
				t.Fatalf("current file: %v, %v", current, err)
			}
		})
	}
}
