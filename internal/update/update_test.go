package update

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func write(t *testing.T, dir, rel, s string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// 发布端：v2 目录 → 清单 + 签名，用 httptest 当更新源。
func feed(t *testing.T, priv ed25519.PrivateKey, v2 string, version string, remove []string, tamper bool) (*httptest.Server, *atomic.Int64) {
	m, err := BuildManifest(v2, version, "linux-amd64", "notes")
	if err != nil {
		t.Fatal(err)
	}
	m.Remove = remove
	mb := m.Encode()
	sig := Sign(priv, mb)
	if tamper {
		mb = append(mb, ' ')
	}
	var bytes atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/linux-amd64/manifest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(mb) })
	mux.HandleFunc("/linux-amd64/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sig)) })
	mux.HandleFunc("/linux-amd64/", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Path[len("/linux-amd64/"):]
		b, err := os.ReadFile(filepath.Join(v2, filepath.FromSlash(rel)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		bytes.Add(int64(len(b)))
		w.Write(b)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s, &bytes
}

func TestIncrementalUpdateBootRollback(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	inst, v2 := t.TempDir(), t.TempDir()
	write(t, inst, "wallpaperd", "v1-binary")
	write(t, inst, "mpv/mpv", "same-big-mpv")
	write(t, inst, "old.txt", "obsolete")
	write(t, v2, "wallpaperd", "v2-binary!")
	write(t, v2, "mpv/mpv", "same-big-mpv")
	write(t, v2, "new.dat", "added")
	srv, got := feed(t, priv, v2, "0.2.0", []string{"old.txt"}, false)
	c := &Client{Feed: srv.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0-mvp", Dir: inst}
	m, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan := Plan(inst, m); len(plan) != 2 {
		t.Fatalf("增量计划应只有 2 个文件: %+v", plan)
	}
	n, err := c.Apply(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len("v2-binary!")+len("added")) || got.Load() != n {
		t.Fatalf("只应下载变化的文件: %d / %d", n, got.Load())
	}
	if read(t, inst, "wallpaperd") != "v2-binary!" || read(t, inst, "new.dat") != "added" || read(t, inst, "old.txt") != "<missing>" {
		t.Fatal("替换结果不对")
	}
	if v, ok := c.CanRollback(); !ok || v != "0.1.0-mvp" {
		t.Fatal("应可回滚")
	}
	// 新版本连续起不来：第 3 次启动自动回滚
	for i := 1; i <= MaxBoots; i++ {
		if rb, err := c.Boot(); rb || err != nil {
			t.Fatal(i, rb, err)
		}
	}
	rb, err := c.Boot()
	if !rb || err != nil {
		t.Fatal("应自动回滚", rb, err)
	}
	if read(t, inst, "wallpaperd") != "v1-binary" || read(t, inst, "new.dat") != "<missing>" || read(t, inst, "old.txt") != "obsolete" || read(t, inst, "mpv/mpv") != "same-big-mpv" {
		t.Fatal("回滚结果不对")
	}
	if _, ok := c.CanRollback(); ok {
		t.Fatal("回滚后不应再可回滚")
	}
}

func TestMarkGoodAndManualRollback(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	inst, v2 := t.TempDir(), t.TempDir()
	write(t, inst, "a", "1")
	write(t, v2, "a", "2")
	srv, _ := feed(t, priv, v2, "0.2.0", nil, false)
	c := &Client{Feed: srv.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	m, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	c.MarkGood()
	for i := 0; i < 5; i++ {
		if rb, _ := c.Boot(); rb {
			t.Fatal("确认后不应自动回滚")
		}
	}
	if err := c.Rollback(); err != nil || read(t, inst, "a") != "1" {
		t.Fatal("手动回滚失败", err)
	}
	if err := c.Rollback(); err != ErrNothing {
		t.Fatal(err)
	}
	// 已是最新
	c.Current = "0.2.0"
	if _, err := c.Check(context.Background()); err != ErrNoUpdate {
		t.Fatal(err)
	}
}

func TestSignatureAndHashRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	inst, v2 := t.TempDir(), t.TempDir()
	write(t, inst, "a", "1")
	write(t, v2, "a", "2")
	srv, _ := feed(t, priv, v2, "0.2.0", nil, true)
	c := &Client{Feed: srv.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	if _, err := c.Check(context.Background()); err != ErrSignature {
		t.Fatal("篡改的清单应被拒绝", err)
	}
	// 别的公钥
	other, _, _ := ed25519.GenerateKey(nil)
	srv2, _ := feed(t, priv, v2, "0.2.0", nil, false)
	c2 := &Client{Feed: srv2.URL, Pub: other, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	if _, err := c2.Check(context.Background()); err != ErrSignature {
		t.Fatal(err)
	}
	// 清单之后文件被替换 → 哈希不符，不应改动安装目录
	c3 := &Client{Feed: srv2.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	m, err := c3.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	write(t, v2, "a", "X")
	if _, err := c3.Apply(context.Background(), m, nil); err == nil {
		t.Fatal("哈希不符应失败")
	}
	if read(t, inst, "a") != "1" {
		t.Fatal("失败时不应改动")
	}
	// 非 https 远程地址拒绝
	c4 := &Client{Feed: "http://example.com", Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	if _, err := c4.Check(context.Background()); err == nil {
		t.Fatal("应拒绝 http")
	}
}

func TestNewerAndSafeRel(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.2.0", "0.1.0-mvp", true}, {"0.1.0", "0.1.0-mvp", true}, {"0.1.0-mvp", "0.1.0", false}, {"1.10.0", "1.9.9", true}, {"v1.0.0", "1.0.0", false}, {"0.1.0-rc2", "0.1.0-rc1", true}} {
		if Newer(c.a, c.b) != c.want {
			t.Error(c)
		}
	}
	for p, ok := range map[string]bool{"a/b": true, "../x": false, "/abs": false, "a/../b": false, `a\b`: false, ".update/x": false, "": false} {
		if safeRel(p) != ok {
			t.Error(p)
		}
	}
}
