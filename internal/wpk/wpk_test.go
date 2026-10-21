package wpk

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodManifest = `{"format":"wpk","schema":0,"id":"3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21","version":1,"type":"video",
 "entry":"content/loop.mp4","title":{"en":"Misty Lake","zh-CN":"雾湖"},"video":{"codec":"h264"},"future":{"x":1}}`

func TestExamplesFromDocs(t *testing.T) {
	for _, f := range []string{"example.video.json", "example.web.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(b); err != nil {
			t.Fatal(f, err)
		}
	}
}

func TestManifestRules(t *testing.T) {
	m, err := Parse([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title.Pick("zh-CN") != "雾湖" || m.Title.Pick("zh-TW") != "Misty Lake" || m.Title.Pick("fr") != "Misty Lake" {
		t.Fatal("Pick")
	}
	if !bytes.Contains(m.Raw, []byte("future")) {
		t.Fatal("Raw 应保留未知字段")
	}
	cases := map[string]string{
		"format":    `"format":"zip"`,
		"schemaNew": `"schema":1`,
		"schemaOld": `"schema":-2`,
		"id":        `"id":"NOT-A-UUID"`,
		"version":   `"version":0`,
		"type":      `"type":"app"`,
		"entryOut":  `"entry":"loop.mp4"`,
		"entryDot":  `"entry":"content/../x.mp4"`,
		"entryBS":   `"entry":"content\\x.mp4"`,
		"entryExt":  `"entry":"content/x.exe"`,
		"titleLang": `"title":{"english":"x"}`,
		"noTitle":   `"title":{}`,
		"codec":     `"video":{"codec":"mpeg2"}`,
		"vw":        `"video":{"w":5}`,
	}
	repl := map[string]string{"format": `"format":"wpk"`, "schemaNew": `"schema":0`, "schemaOld": `"schema":0`,
		"id": `"id":"3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21"`, "version": `"version":1`, "type": `"type":"video"`,
		"entryOut": `"entry":"content/loop.mp4"`, "entryDot": `"entry":"content/loop.mp4"`, "entryBS": `"entry":"content/loop.mp4"`,
		"entryExt": `"entry":"content/loop.mp4"`, "titleLang": `"title":{"en":"Misty Lake","zh-CN":"雾湖"}`,
		"noTitle": `"title":{"en":"Misty Lake","zh-CN":"雾湖"}`, "codec": `"video":{"codec":"h264"}`, "vw": `"video":{"codec":"h264"}`}
	for name, bad := range cases {
		s := strings.Replace(goodManifest, repl[name], bad, 1)
		if s == goodManifest {
			t.Fatalf("%s: 替换失败", name)
		}
		_, err := Parse([]byte(s))
		if err == nil {
			t.Errorf("%s 应被拒绝", name)
		}
		if name == "schemaNew" && !errors.Is(err, ErrNewerSchema) {
			t.Errorf("schemaNew 应返回 ErrNewerSchema: %v", err)
		}
	}
	if _, err := Parse([]byte(`{"format":"wpk","schema":0,"id":"3f2b8c1e-9a7d-4e2f-8b1a-0c6d5e4f3a21","version":1,"type":"video","entry":"content/a.mp4","title":{"en":"x"}}`)); err == nil {
		t.Error("video 类型缺 video 对象应被拒绝")
	}
	if _, err := Parse(bytes.Repeat([]byte(" "), MaxManifest+1)); err == nil {
		t.Error("超过 256 KiB 应被拒绝")
	}
	props := []string{
		`{"key":"1bad","type":"bool","default":true}`,
		`{"key":"s","type":"slider","default":1}`,
		`{"key":"b","type":"bool","default":"x"}`,
		`{"key":"c","type":"color","default":"red"}`,
		`{"key":"o","type":"select","default":"a"}`,
		`{"key":"t","type":"text","default":1}`,
		`{"key":"z","type":"weird","default":1}`,
		`{"key":"n","type":"bool"}`,
	}
	for _, p := range props {
		s := strings.Replace(goodManifest, `"future"`, `"properties":[`+p+`],"future"`, 1)
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("属性 %s 应被拒绝", p)
		}
	}
	okp := `[{"key":"s","type":"slider","min":0,"max":2,"default":1},{"key":"c","type":"color","default":"#aabbcc"},{"key":"o","type":"select","default":"a","options":[{"value":"a"}]},{"key":"t","type":"text","default":"x"},{"key":"b","type":"bool","default":false}]`
	if _, err := Parse([]byte(strings.Replace(goodManifest, `"future"`, `"properties":`+okp+`,"future"`, 1))); err != nil {
		t.Error(err)
	}
}

func TestCheckPath(t *testing.T) {
	good := []string{"content/a.mp4", "content/子目录/ü.png", "preview.png", "content/"}
	badp := []string{"", "/etc/passwd", "../x", "content/../../x", `content\x`, "C:/x", "content/CON", "content/nul.txt",
		"content/a.", "content/a ", "content//a", "content/./a", "a\x00b", "content/a:b", "content/com1.mp4"}
	for _, p := range good {
		if err := CheckPath(p); err != nil {
			t.Errorf("%q 应通过: %v", p, err)
		}
	}
	for _, p := range badp {
		if CheckPath(p) == nil {
			t.Errorf("%q 应被拒绝", p)
		}
	}
}

type entry struct {
	name   string
	data   []byte
	method uint16
	mode   os.FileMode
}

func mkzip(t *testing.T, es []entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.wpk")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, e := range es {
		h := &zip.FileHeader{Name: e.name, Method: e.method}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(e.data)
	}
	zw.Close()
	f.Close()
	return p
}

func base() []entry {
	return []entry{{name: "manifest.json", data: []byte(goodManifest), method: zip.Deflate},
		{name: "preview.png", data: []byte("\x89PNG...."), method: zip.Store},
		{name: "content/", method: zip.Store},
		{name: "content/loop.mp4", data: []byte("....ftypisom"), method: zip.Store}}
}

func TestExtractOK(t *testing.T) {
	lib := t.TempDir()
	r, err := Extract(mkzip(t, base()), lib)
	if err != nil {
		t.Fatal(err)
	}
	if r.Files != 3 || r.Dir != filepath.Join(lib, r.Manifest.ID) {
		t.Fatal(r)
	}
	if _, err := LoadDir(r.Dir); err != nil {
		t.Fatal(err)
	}
	// 同 id 再导入 = 更新（替换目录）
	if _, err := Extract(mkzip(t, base()), lib); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(lib)
	if len(ents) != 1 {
		t.Fatal("临时目录应被清理", ents)
	}
}

func TestExtractRejects(t *testing.T) {
	lib := t.TempDir()
	cases := map[string][]entry{
		"zipslip":    append(base(), entry{name: "../evil.txt", data: []byte("x")}),
		"abs":        append(base(), entry{name: "/abs.txt", data: []byte("x")}),
		"dupCase":    append(base(), entry{name: "CONTENT/LOOP.MP4", data: []byte("x")}),
		"symlink":    append(base(), entry{name: "content/link", data: []byte("/etc/passwd"), mode: os.ModeSymlink | 0o777}),
		"exeExt":     append(base(), entry{name: "content/run.exe", data: []byte("x")}),
		"exeMagic":   append(base(), entry{name: "content/innocent.png", data: []byte("MZ\x90\x00")}),
		"elfMagic":   append(base(), entry{name: "content/a.bin", data: []byte("\x7fELF\x02")}),
		"machoMagic": append(base(), entry{name: "content/a.bin", data: []byte{0xcf, 0xfa, 0xed, 0xfe}}),
		"bomb":       append(base(), entry{name: "content/bomb.txt", data: bytes.Repeat([]byte{0}, 8<<20), method: zip.Deflate}),
		"noManifest": base()[1:],
		"noPreview":  append(base()[:1], base()[2:]...),
		"noEntry":    base()[:3],
		"reserved":   append(base(), entry{name: "content/aux.js", data: []byte("x")}),
	}
	for name, es := range cases {
		if _, err := Extract(mkzip(t, es), lib); err == nil {
			t.Errorf("%s 应被拒绝", name)
		}
	}
	if ents, _ := os.ReadDir(lib); len(ents) != 0 {
		t.Fatal("失败的导入不应留下任何文件", ents)
	}
}

func TestExtractNFCDuplicate(t *testing.T) {
	// "é" 的 NFC 与 NFD 形式应视为同名
	es := append(base(), entry{name: "content/caf\u00e9.png", data: []byte("x")}, entry{name: "content/cafe\u0301.png", data: []byte("y")})
	if _, err := Extract(mkzip(t, es), t.TempDir()); err == nil {
		t.Fatal("NFC 规范化后重名应被拒绝")
	}
}

func TestExecHeader(t *testing.T) {
	if IsExecutableHeader([]byte("M")) || IsExecutableHeader([]byte("abcd")) || !IsExecutableHeader([]byte{0xca, 0xfe, 0xba, 0xbe}) {
		t.Fatal()
	}
}

func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	m, _ := Parse([]byte(goodManifest))
	os.MkdirAll(filepath.Join(dir, "content"), 0o755)
	os.WriteFile(filepath.Join(dir, "content", "loop.mp4"), []byte("x"), 0o644)
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	m.Version = 0
	if WriteManifest(dir, m) == nil {
		t.Fatal("非法 manifest 不应写出")
	}
	os.Remove(filepath.Join(dir, "content", "loop.mp4"))
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("entry 缺失应报错")
	}
}

func TestSafeUnzip(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "x")
	if err := SafeUnzip(mkzip(t, []entry{{name: "LivelyInfo.json", data: []byte("{}")}, {name: "a/b.mp4", data: []byte("x")}}), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a", "b.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := SafeUnzip(mkzip(t, []entry{{name: "../../x", data: []byte("x")}}), filepath.Join(t.TempDir(), "y")); !errors.Is(err, ErrUnsafe) {
		t.Fatal(err)
	}
}
