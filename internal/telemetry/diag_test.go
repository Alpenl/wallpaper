package telemetry

import (
	"wpapp/internal/buildinfo"

	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
