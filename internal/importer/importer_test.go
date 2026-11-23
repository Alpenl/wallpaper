package importer

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpapp/internal/media"
	"wpapp/internal/wpk"
)

func fixture(n string) string { return filepath.Join("..", "media", "testdata", n) }

func opts(t *testing.T, goos string) Options {
	lib, cache := t.TempDir(), t.TempDir()
	return Options{Library: lib, Cache: cache, GOOS: goos, Probe: media.ProbeFile,
		Snapshot: func(src, dst string) error { return os.WriteFile(dst, []byte("\x89PNG"), 0o644) }}
}

func TestImportVideoFile(t *testing.T) {
	o := opts(t, "windows")
	r, err := Import(context.Background(), fixture("h264_audio.mp4"), o)
	if err != nil {
		t.Fatal(err)
	}
	it := r.Item
	if it.Type != "video" || it.Codec != "h264" || it.Transcode != "recommended" || it.Snapshot == "" || it.Title != "h264_audio" {
		t.Fatalf("%+v", it)
	}
	m, err := wpk.LoadDir(it.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Video.HasAudio || m.Video.W != 64 || m.Entry != "content/h264_audio.mp4" {
		t.Fatalf("%+v", m.Video)
	}
	if _, err := os.Stat(filepath.Join(it.Dir, "preview.png")); err != nil {
		t.Fatal("应生成 preview.png")
	}
	if len(r.Warnings) == 0 {
		t.Fatal("含音轨应提示")
	}
}

func TestImportImageAndGif(t *testing.T) {
	o := opts(t, "windows")
	r, err := Import(context.Background(), fixture("still.png"), o)
	if err != nil || r.Item.Type != "image" || !strings.HasSuffix(r.Item.Snapshot, ".png") {
		t.Fatal(err, r)
	}
	r, err = Import(context.Background(), fixture("anim.gif"), o)
	if err != nil || r.Item.Type != "video" {
		t.Fatal(err, r)
	}
}

func TestNeedsTranscode(t *testing.T) {
	o := opts(t, "darwin")
	_, err := Import(context.Background(), fixture("vp9.webm"), o)
	if !errors.Is(err, ErrNeedsTranscode) {
		t.Fatal(err)
	}
	called := false
	o.Transcode = func(ctx context.Context, src, dst string) error {
		called = true
		b, _ := os.ReadFile(fixture("hevc.mov"))
		return os.WriteFile(dst, b, 0o644)
	}
	r, err := Import(context.Background(), fixture("vp9.webm"), o)
	if err != nil || !called || r.Item.Transcode != "done" || r.Item.Codec != "hevc" || !strings.HasSuffix(r.Item.Entry, ".mov") {
		t.Fatal(err, r)
	}
	// Windows 上 webm 能播（建议转码）
	r, err = Import(context.Background(), fixture("vp9.webm"), opts(t, "windows"))
	if err != nil || r.Item.Transcode != "recommended" {
		t.Fatal(err, r)
	}
	if ents, _ := os.ReadDir(o.Library); len(ents) != 1 {
		t.Fatal("失败的导入不应留下目录", ents)
	}
}

func TestImportGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.mp4")
	os.WriteFile(p, []byte("not a video at all"), 0o644)
	if _, err := Import(context.Background(), p, opts(t, "windows")); err == nil {
		t.Fatal("应拒绝")
	}
	if _, err := Import(context.Background(), "/nonexistent", opts(t, "windows")); err == nil {
		t.Fatal()
	}
}

func writeLively(t *testing.T, dir, typ, file string) {
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "LivelyInfo.json"), []byte("\ufeff"+`{"Title":"Lake","Desc":"d","Author":"a","License":"MIT","Type":`+typ+`,"FileName":"`+file+`","Tags":["x"]}`), 0o644)
}

func TestImportLively(t *testing.T) {
	dir := t.TempDir()
	writeLively(t, dir, "7", "v.mp4")
	b, _ := os.ReadFile(fixture("h264_audio.mp4"))
	os.WriteFile(filepath.Join(dir, "v.mp4"), b, 0o644)
	o := opts(t, "windows")
	r, err := Import(context.Background(), dir, o)
	if err != nil || r.Item.Origin != "lively" || r.Item.Title != "Lake" {
		t.Fatal(err, r)
	}
	m, _ := wpk.LoadDir(r.Item.Dir)
	if r.Item.Author != "a" || r.Item.Description != "d" || len(r.Item.Tags) != 1 || r.Item.Tags[0] != "x" || r.Item.Width != 64 || r.Item.SizeBytes <= 0 {
		t.Fatalf("search metadata lost: %+v", r.Item)
	}
	if m.Author.Name != "a" || m.License != "MIT" || m.Origin.Kind != "lively" {
		t.Fatalf("%+v", m)
	}
	// 名字形式 + zip
	dir2 := t.TempDir()
	writeLively(t, dir2, `"web"`, "index.html")
	if err := os.WriteFile(filepath.Join(dir2, "index.html"), []byte("<!doctype html><title>Local</title>"), 0644); err != nil {
		t.Fatal(err)
	}
	if r, err := Import(context.Background(), dir2, o); err != nil || r.Item.Type != "web" {
		t.Fatalf("web import: %v %v", r, err)
	}
	dir3 := t.TempDir()
	writeLively(t, dir3, "7", "../../etc/passwd")
	if _, err := Import(context.Background(), dir3, o); err == nil {
		t.Fatal("越界路径应拒绝")
	}
	zp := filepath.Join(t.TempDir(), "l.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("LivelyInfo.json")
	w.Write([]byte(`{"Title":"Z","Type":"video","FileName":"v.mp4"}`))
	w, _ = zw.Create("v.mp4")
	w.Write(b)
	zw.Close()
	f.Close()
	r, err = Import(context.Background(), zp, o)
	if err != nil || r.Item.Title != "Z" {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(o.Library); len(ents) != 3 {
		t.Fatal("临时解压目录应清理", ents)
	}
}

func TestImportWE(t *testing.T) {
	dir := t.TempDir()
	b, _ := os.ReadFile(fixture("hevc.mov"))
	os.WriteFile(filepath.Join(dir, "clip.mov"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "project.json"), []byte(`{"title":"WE","type":"Video","file":"clip.mov"}`), 0o644)
	r, err := Import(context.Background(), dir, opts(t, "darwin"))
	if err != nil || r.Item.Origin != "we" || r.Item.Codec != "hevc" {
		t.Fatal(err, r)
	}
	os.WriteFile(filepath.Join(dir, "project.json"), []byte(`{"title":"WE","type":"scene","file":"scene.pkg"}`), 0o644)
	if _, err := Import(context.Background(), dir, opts(t, "darwin")); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := Import(context.Background(), t.TempDir(), opts(t, "darwin")); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestImportWPK(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "p.wpk")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	add := func(n string, b []byte) { w, _ := zw.Create(n); w.Write(b) }
	add("manifest.json", []byte(`{"format":"wpk","schema":0,"id":"3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21","version":1,"type":"video","entry":"content/a.mp4","title":{"en":"P"},"video":{"codec":"h264"}}`))
	add("preview.png", []byte("\x89PNG"))
	b, _ := os.ReadFile(fixture("h264_audio.mp4"))
	add("content/a.mp4", b)
	zw.Close()
	f.Close()
	r, err := Import(context.Background(), zp, opts(t, "windows"))
	if err != nil || r.Item.ID != "3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21" || r.Item.Snapshot == "" {
		t.Fatal(err, r)
	}
}

func TestHelpers(t *testing.T) {
	id := NewID()
	if len(id) != 36 || id[14] != '4' {
		t.Fatal(id)
	}
	for in, want := range map[string]string{"My Video (1).MP4": "My_Video__1.mp4", "CON.mp4": "media.mp4", "雾湖.mov": "雾湖.mov", "...mp4": "media.mp4"} {
		if got := safeName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if (livelyInfo{Type: []byte("99")}).typeName() != "unknown" {
		t.Fatal()
	}
}

func TestUnsupportedWPKCannotReplaceExistingItem(t *testing.T) {
	o := opts(t, "linux")
	id := "3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21"
	dir := filepath.Join(o.Library, id)
	os.MkdirAll(dir, 0755)
	keep := filepath.Join(dir, "keep")
	os.WriteFile(keep, []byte("original"), 0600)
	zp := filepath.Join(t.TempDir(), "web.wpk")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"manifest.json": `{"format":"wpk","schema":1,"id":"` + id + `","version":1,"type":"web","entry":"index.html","title":{"en":"Web"},"web":{}}`, "preview.png": "PNG", "index.html": "<html/>"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	if _, err := Import(context.Background(), zp, o); err == nil {
		t.Fatal("unsupported package accepted")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "original" {
		t.Fatal("existing item changed", string(b), err)
	}
}

// H-07: Forbid IsAbsolutePath in Lively .zip archives unconditionally, and reject
// UNC / Windows device paths across all importer entrypoints.
func TestLivelyZipForbidsIsAbsolutePath(t *testing.T) {
	o := opts(t, "windows")
	target := fixture("h264_audio.mp4")
	absTarget, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(t.TempDir(), "abs.zip")
	f, err := os.Create(zp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("LivelyInfo.json")
	escaped := strings.ReplaceAll(absTarget, `\`, `\\`)
	_, _ = w.Write([]byte(`{"Title":"Exfil","Type":"video","IsAbsolutePath":true,"FileName":"` + escaped + `"}`))
	_ = zw.Close()
	_ = f.Close()
	if _, err := Import(context.Background(), zp, o); !errors.Is(err, wpk.ErrUnsafe) {
		t.Fatalf("expected wpk.ErrUnsafe for IsAbsolutePath in zip, got %v", err)
	}
}

func TestRejectsUNCAndDevicePaths(t *testing.T) {
	o := opts(t, "windows")
	badPaths := []string{
		`\\attacker\share\evil.mp4`,
		`//attacker/share/evil.mp4`,
		`\\?\C:\Windows\win.ini`,
		`\\.\pipe\evil`,
		`\/attacker\share\evil.mp4`,
		`/\attacker/share/evil.mp4`,
	}
	for _, p := range badPaths {
		if _, err := Import(context.Background(), p, o); !errors.Is(err, wpk.ErrUnsafe) {
			t.Errorf("Import(%q): expected ErrUnsafe, got %v", p, err)
		}
		if _, err := CollectSources(p); !errors.Is(err, wpk.ErrUnsafe) {
			t.Errorf("CollectSources(%q): expected ErrUnsafe, got %v", p, err)
		}
		if got := PreviewColors(p); got != nil {
			t.Errorf("PreviewColors(%q): expected nil, got %v", p, got)
		}
		// Local Lively folder with IsAbsolutePath pointing at UNC/device path must also fail.
		dir := t.TempDir()
		escaped := strings.ReplaceAll(p, `\`, `\\`)
		_ = os.WriteFile(filepath.Join(dir, "LivelyInfo.json"), []byte(`{"Title":"UNC","Type":"video","IsAbsolutePath":true,"FileName":"`+escaped+`"}`), 0o644)
		if _, err := Import(context.Background(), dir, o); !errors.Is(err, wpk.ErrUnsafe) {
			t.Errorf("Lively folder IsAbsolutePath(%q): expected ErrUnsafe, got %v", p, err)
		}
	}
}

// M-04: Options.PreviewColors overrides in-process PreviewColors for video/image imports.
func TestPreviewColorsOptionCallback(t *testing.T) {
	o := opts(t, "windows")
	var paths []string
	o.PreviewColors = func(p string) []string {
		paths = append(paths, p)
		return []string{"green", "绿色"}
	}
	r1, err := Import(context.Background(), fixture("still.png"), o)
	if err != nil || len(r1.Item.Colors) != 2 || r1.Item.Colors[0] != "green" {
		t.Fatalf("image PreviewColors callback failed: %v %+v", err, r1)
	}
	r2, err := Import(context.Background(), fixture("h264_audio.mp4"), o)
	if err != nil || len(r2.Item.Colors) != 2 || r2.Item.Colors[0] != "green" {
		t.Fatalf("video PreviewColors callback failed: %v %+v", err, r2)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 PreviewColors calls, got %d", len(paths))
	}
}

// M-05: Rotated, 10-bit H.264, odd-dimension, and >240fps videos require transcode
// and never fail manifest writing if a direct copy path is exercised.
func TestRequiresTranscodeForIncompatibleVideoAttributes(t *testing.T) {
	cases := map[string]media.Info{
		"rotated": {Container: "mp4", Kind: "video", Codec: "h264", W: 1920, H: 1080, FPS: 30, Rotation: 90},
		"10bit":   {Container: "mp4", Kind: "video", Codec: "h264", W: 1920, H: 1080, FPS: 30, BitDepth: 10},
		"oddDims": {Container: "mp4", Kind: "video", Codec: "h264", W: 1919, H: 1080, FPS: 30},
		"over240": {Container: "mp4", Kind: "video", Codec: "h264", W: 1920, H: 1080, FPS: 300},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			o := opts(t, "windows")
			inf := info
			o.Probe = func(string) (*media.Info, error) { return &inf, nil }
			_, err := Import(context.Background(), fixture("h264_audio.mp4"), o)
			if !errors.Is(err, ErrNeedsTranscode) {
				t.Fatalf("%s: expected ErrNeedsTranscode, got %v", name, err)
			}
		})
	}
}
