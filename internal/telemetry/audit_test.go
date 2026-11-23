package telemetry

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"wpapp/internal/buildinfo"
)

func TestH27TitleNamedConfigDoesNotCorruptArchiveOrJSONKeys(t *testing.T) {
	d := t.TempDir()
	logs := filepath.Join(d, "logs")
	cfgDirA := filepath.Join(d, "a")
	cfgDirB := filepath.Join(d, "b")
	for _, dir := range []string{logs, cfgDirA, cfgDirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg1 := putDiagnostic(t, cfgDirA, "config.json", `{"config":{"render":"mpv","note":"using config wallpaper"},"state":"active"}`)
	cfg2 := putDiagnostic(t, cfgDirB, "config.json", `{"config":{"render":"mf"}}`)
	stateFile := putDiagnostic(t, cfgDirA, "state.json", `{"state":{"clean":true}}`)
	putDiagnostic(t, logs, "wallpaperd.log", `{"msg":"started","config":"playing config"}`+"\n")

	out := filepath.Join(d, "diag.zip")
	private := PrivateValues{Titles: []string{"config", "logs", "state", "render"}}
	if err := ExportDiagnostics(logs, []string{cfg1, cfg2, stateFile}, out, buildinfo.Info{Version: "1.0.0"}, private); err != nil {
		t.Fatal(err)
	}

	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	seen := map[string]bool{}
	for _, f := range z.File {
		if strings.ContainsAny(f.Name, `<>:"\|?*`) {
			t.Errorf("archive entry %q contains illegal Windows filename chars", f.Name)
		}
		if seen[strings.ToLower(f.Name)] {
			t.Errorf("duplicate archive entry %q", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
	}

	files := unzipDiagnostic(t, out)
	for _, want := range []string{"config/config.json", "config/config-2.json", "config/state.json", "logs/wallpaperd.log", "manifest.json", "README.txt"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing expected archive entry %q (got keys: %v)", want, seen)
		}
	}

	// Verify JSON schema keys survived intact while private title values were redacted.
	var cfgObj map[string]any
	if err := json.Unmarshal([]byte(files["config/config.json"]), &cfgObj); err != nil {
		t.Fatalf("config/config.json invalid JSON: %v\n%s", err, files["config/config.json"])
	}
	nested, ok := cfgObj["config"].(map[string]any)
	if !ok {
		t.Fatalf("JSON schema key 'config' was corrupted: %s", files["config/config.json"])
	}
	if _, ok := nested["render"]; !ok {
		t.Fatalf("JSON schema key 'render' was corrupted: %s", files["config/config.json"])
	}
	if note, _ := nested["note"].(string); strings.Contains(note, "config") {
		t.Fatalf("private title value not redacted in string value: %q", note)
	}

	// Sanitizer must remove illegal chars from dynamic filenames.
	if got := sanitizeArchiveBase(`bad<name>:with*chars?.log`); strings.ContainsAny(got, `<>:"/\|?*`) {
		t.Errorf("sanitizeArchiveBase failed: %q", got)
	}
}

func TestH27URLSchemesQueryParamsBearerAndWindowsHomes(t *testing.T) {
	t.Run("URL schemes not misparsed as Windows drive letters and secrets redacted", func(t *testing.T) {
		redactors := map[string]func(string) string{
			"Redact": Redact,
			"newRedactor": newRedactor(PrivateValues{
				Paths:  []string{`C:\Users\Alice\Videos\clip.mp4`},
				Titles: []string{"My Wallpaper"},
			}),
		}
		for name, fn := range redactors {
			got := fn("GET https://example.com/a?token=SECRET&page=2")
			if !strings.Contains(got, "https://example.com/a?token=<redacted>&page=2") || strings.Contains(got, "SECRET") || strings.Contains(got, "<path:") {
				t.Errorf("%s corrupted or leaked https URL: %q", name, got)
			}

			got = fn("proxy socks5://user:pass@host/?token=abc&sig=deadbeef&access_token=xyz")
			if !strings.Contains(got, "socks5://<redacted>@host/?token=<redacted>&sig=<redacted>&access_token=<redacted>") {
				t.Errorf("%s failed socks5 URL redaction: %q", name, got)
			}
			for _, bad := range []string{"user:pass", "abc", "deadbeef", "xyz"} {
				if strings.Contains(got, bad) {
					t.Errorf("%s leaked %q in %q", name, bad, got)
				}
			}

			got = fn("Authorization: Bearer super_secret_jwt_123")
			if !strings.Contains(got, "Authorization: Bearer <redacted>") || strings.Contains(got, "super_secret_jwt_123") {
				t.Errorf("%s leaked Bearer token: %q", name, got)
			}
		}
	})

	t.Run("Windows backslash home and WPAPP_HOME redacted across slash and case variants", func(t *testing.T) {
		winHome := `D:\CustomHome\Jane Doe`
		re := buildDirPattern(winHome, true)
		if re == nil {
			t.Fatal("buildDirPattern returned nil")
		}
		for _, sample := range []string{
			`open D:\CustomHome\Jane Doe\Videos\clip.mp4`,
			`open d:\customhome\jane doe\Videos\clip.mp4`,
			`open D:/CustomHome/Jane Doe/Videos/clip.mp4`,
			`open D:\\CustomHome\\Jane Doe\\Videos\\clip.mp4`,
			`dir="D:\CustomHome\Jane Doe"`,
		} {
			got := replaceSubmatchPath(re, sample, 0, 1, 2)
			if strings.Contains(strings.ToLower(got), "jane doe") || strings.Contains(got, "clip.mp4") || !strings.Contains(got, "<path:") {
				t.Errorf("buildDirPattern failed for %q -> %q", sample, got)
			}
		}

		// Dynamic WPAPP_HOME redaction via Redact.
		wpHome := filepath.Join(t.TempDir(), "PortableHome")
		t.Setenv("WPAPP_HOME", wpHome)
		for _, sample := range []string{
			"failed opening " + filepath.Join(wpHome, "library", "secret.mp4"),
			"failed opening " + strings.ReplaceAll(filepath.Join(wpHome, "library", "secret.mp4"), `\`, "/"),
			"failed opening " + strings.ReplaceAll(filepath.Join(wpHome, "library", "secret.mp4"), `\`, `\\`),
		} {
			got := Redact(sample)
			if strings.Contains(got, "PortableHome") || strings.Contains(got, "secret.mp4") || !strings.Contains(got, "<path:") {
				t.Errorf("WPAPP_HOME leaked for %q -> %q", sample, got)
			}
		}
	})
}

func TestM18InstallCrashOutputPreservesLiveSiblingCrashFile(t *testing.T) {
	if os.Getenv("WP_CRASH_LIVE_HELPER") == "1" {
		// Exit immediately; used to obtain a guaranteed dead PID.
		os.Exit(0)
	}
	dir := t.TempDir()
	t.Cleanup(func() {
		if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil {
			t.Error(err)
		}
	})

	// Verify PID parser.
	if pid, ok := crashLogPID("crash-main-12345-20261123T150405.000000000.log"); !ok || pid != 12345 {
		t.Fatalf("crashLogPID failed: %d %v", pid, ok)
	}
	if pid, ok := crashLogPID("crash-web-host-42-20261123T150405.000000000.log"); !ok || pid != 42 {
		t.Fatalf("crashLogPID hyphen role failed: %d %v", pid, ok)
	}
	if _, ok := crashLogPID("crash-old-00.log"); ok {
		t.Fatal("crashLogPID misparsed legacy test name")
	}

	// Obtain a definitely-dead PID by running a short-lived child to completion.
	deadCmd := exec.Command(os.Args[0], "-test.run=^TestM18InstallCrashOutputPreservesLiveSiblingCrashFile$")
	deadCmd.Env = append(os.Environ(), "WP_CRASH_LIVE_HELPER=1")
	if err := deadCmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := deadCmd.Process.Pid

	// Parent PID is a live process distinct from os.Getpid().
	livePID := os.Getppid()
	if livePID <= 0 || livePID == os.Getpid() || !processAlive(livePID) {
		t.Skipf("parent pid %d not usable as live sibling", livePID)
	}

	liveFile := filepath.Join(dir, fmt.Sprintf("crash-main-%d-20261123T150405.000000000.log", livePID))
	deadFile := filepath.Join(dir, fmt.Sprintf("crash-main-%d-20261123T150405.000000000.log", deadPID))
	if err := os.WriteFile(liveFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deadFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := InstallCrashOutput(dir, "webhost"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(liveFile); err != nil {
		t.Fatalf("live sibling 0-byte crash file was unlinked: %v", err)
	}
	if _, err := os.Stat(deadFile); !os.IsNotExist(err) {
		t.Fatalf("dead process 0-byte crash file was not cleaned up: %v", err)
	}
}
