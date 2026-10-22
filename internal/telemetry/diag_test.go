package telemetry

import (
	"wpapp/internal/buildinfo"

	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportDiagnosticsHasNoPaths(t *testing.T) {
	d := t.TempDir()
	logs := filepath.Join(d, "logs")
	os.MkdirAll(logs, 0o755)
	os.WriteFile(filepath.Join(logs, "wallpaperd.log"), []byte(
		`{"msg":"imported","title":"我的假期","path":"C:\\Users\\bob\\Videos\\假期.mp4"}`+"\n"+
			`{"msg":"x","err":"open /home/bob/secret/beach.mov: no such file"}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(logs, "mpv-w-1.log"), []byte(
		"[   0.001][v][cplayer] Command line options: '--' 'Z:\\tmp\\wine\\clip.mp4'\n"+
			"[   0.090][e][vo/xv] No Xvideo support found.\n"), 0o644)
	cfg := filepath.Join(d, "config.json")
	os.WriteFile(cfg, []byte(`{"render":{"mpvPath":"D:\\tools\\mpv.exe"}}`), 0o644)
	lib := filepath.Join(d, "library.json")
	os.WriteFile(lib, []byte(`{"items":[{"title":"x"}]}`), 0o644)
	out := filepath.Join(d, "diag.zip")
	identity := buildinfo.Info{Version: "1.2.3", Commit: "abcdef012345", Modified: true, CommitTime: "2026-10-22T00:00:00Z", GoVersion: "go1.27.3"}
	if err := ExportDiagnostics(logs, []string{cfg}, out, identity); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var all strings.Builder
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		all.Write(b)
	}
	s := all.String()
	if !strings.Contains(s, "wallpaperd "+identity.String()) {
		t.Fatal("missing build identity", s)
	}
	for _, bad := range []string{"bob", "假期", "beach", "clip.mp4", `D:\tools`, "Command line"} {
		if strings.Contains(s, bad) {
			t.Errorf("diagnostics leak %q:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "No Xvideo support found") || !strings.Contains(s, "<path:") {
		t.Errorf("expected kept warnings and hashed paths:\n%s", s)
	}
	if !names["logs/wallpaperd.log"] || !names["config/config.json"] || names["config/library.json"] {
		t.Error(names)
	}
}

func putDiagnostic(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func unzipDiagnostic(t *testing.T, p string) map[string]string {
	t.Helper()
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	out := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestDiagnosticsFormatsAndRecursivePrivacy(t *testing.T) {
	d := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	putDiagnostic(t, d, "mpv-display.log", "[1][w][decoder] representative warning\n[2][v][decoder] verbose omitted\n")
	putDiagnostic(t, d, "mfhost-display.log", "2026/10/22 12:00:00 MF failure\n")
	for _, name := range []string{"crash-shader-123.log", "web-stderr-display.log", "mpv-stderr-display.log.1"} {
		putDiagnostic(t, d, name, "panic: representative crash\nfailed opening \""+filepath.Join(home, "My Movies", "own-secret.mp4")+"\"\nopen /home/foreign/My Movies/foreign-secret.mp4\nopen C:\\Users\\someone\\My Movies\\windows-secret.mp4\nfetch https://private-user:private-pass@example.com/video\n")
	}
	cfg := putDiagnostic(t, d, "config.json", `{
 "nested": [{"title": "private-title", "api_key": "private-key", "password": {"value":"private-password"}, "path": "relative secret.mp4"}],
 "url": "https://private-user:private-pass@example.com/api",
 "enabled": true
}`)
	status := putDiagnostic(t, d, "logger-status.json", `{"dropped": 17, "error":"open /Users/other/Private Files/status-secret"}`)
	out := filepath.Join(d, "export.zip")
	snapshot := []any{map[string]any{"display": "display-1", "type": "video", "backend": "mpv", "options": map[string]any{"speed": 1.5, "token": "private-option", "title": "private-option-title"}}}
	if err := ExportDiagnostics(d, []string{cfg, status}, out, buildinfo.Info{Version: "dev"}, snapshot); err != nil {
		t.Fatal(err)
	}
	files := unzipDiagnostic(t, out)
	all := ""
	for _, s := range files {
		all += s
	}
	for _, want := range []string{"representative warning", "MF failure", "representative crash", `"dropped": 17`, `"backend": "mpv"`, `"speed": 1.5`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{home, "own-secret", "foreign", "windows-secret", "private-user", "private-pass", "private-title", "private-key", "private-password", "relative secret", "private-option", "status-secret", "verbose omitted"} {
		if strings.Contains(all, bad) {
			t.Errorf("leaked %q", bad)
		}
	}
	for _, name := range []string{"config/config.json", "assignments.json", "config/logger-status.json"} {
		if !json.Valid([]byte(files[name])) {
			t.Errorf("invalid JSON %s", name)
		}
	}
}

func TestDiagnosticsTailAndTotalBudget(t *testing.T) {
	d := t.TempDir()
	// More than 20 MiB of log data, generated only in the temporary fixture.
	line := strings.Repeat("x", 1000) + "\n"
	for i := 0; i < 13; i++ {
		p := putDiagnostic(t, d, fmt.Sprintf("crash-%02d.log", i), "OLD HEAD\n"+strings.Repeat(line, 2200)+fmt.Sprintf("LATEST TAIL %02d\n", i))
		stamp := time.Unix(int64(i), 0)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	cfg := putDiagnostic(t, d, "logger-status.json", `{"dropped":23}`)
	out := filepath.Join(d, "export.zip")
	if err := ExportDiagnostics(d, []string{cfg}, out, buildinfo.Info{Version: "dev"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 20<<20 {
		t.Fatalf("ZIP size %d", st.Size())
	}
	files := unzipDiagnostic(t, out)
	newest := files["logs/crash-12.log"]
	if !strings.HasPrefix(newest, truncationMarker) || !strings.Contains(newest, "LATEST TAIL 12") || strings.Contains(newest, "OLD HEAD") {
		t.Fatal("tail/truncation missing")
	}
	if _, ok := files["logs/crash-00.log"]; ok {
		t.Fatal("oldest file should lose budget priority")
	}
	if !strings.Contains(files["manifest.json"], "omitted:") || files["README.txt"] == "" || files["config/logger-status.json"] == "" {
		t.Fatal("missing summary/manifest")
	}
}

type failingDiagnosticWriter struct {
	bytes.Buffer
	failAfter          int
	writeErr, closeErr error
	closed             bool
}

func (w *failingDiagnosticWriter) Write(b []byte) (int, error) {
	if w.writeErr != nil && w.Len()+len(b) > w.failAfter {
		return 0, w.writeErr
	}
	return w.Buffer.Write(b)
}
func (w *failingDiagnosticWriter) Close() error { w.closed = true; return w.closeErr }

func TestDiagnosticsWriteAndCloseFailures(t *testing.T) {
	failure := errors.New("injected I/O failure")
	for _, tc := range []struct {
		name        string
		size, after int
		close       bool
	}{
		{"entry write", 8192, 0, false}, {"zip finalization", 1, 0, false}, {"destination close", 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &failingDiagnosticWriter{failAfter: tc.after}
			if tc.close {
				w.closeErr = failure
			} else {
				w.writeErr = failure
			}
			err := writeDiagnostics(w, []diagnosticEntry{{"test.txt", bytes.Repeat([]byte("x"), tc.size)}})
			if !errors.Is(err, failure) || !w.closed {
				t.Fatalf("err=%v closed=%v", err, w.closed)
			}
		})
	}
}

func TestRedactPathBoundaries(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		home + "/My Movies/secret.mp4",
		home + "-other/My Movies/secret.mp4",
		"open /data/My Movies/secret.mp4",
		"open ~/My Movies/secret.mp4",
		`open \\server\My Movies\secret.mp4`,
	} {
		clean := Redact(raw)
		if strings.Contains(clean, "secret") || strings.Contains(clean, "My Movies") || !strings.Contains(clean, "<path:") {
			t.Errorf("redaction failed: %q", clean)
		}
	}
}
