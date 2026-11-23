package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestH01MarkGoodRequiresTargetVersionAndBoot(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	inst, v2 := t.TempDir(), t.TempDir()
	write(t, inst, "wallpaperd", "v1")
	write(t, v2, "wallpaperd", "v2")
	srv, _ := feed(t, priv, v2, "0.2.0", nil, false)

	c := &Client{Feed: srv.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	m, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}

	// Still on 0.1.0: MarkGood must be rejected, and CanRollback must be false.
	if err := c.MarkGood(); err == nil {
		t.Fatal("MarkGood succeeded while still running old version 0.1.0")
	}
	if _, ok := c.CanRollback(); ok {
		t.Fatal("CanRollback returned true while still on old version 0.1.0")
	}

	// Switched to 0.2.0, but Boots == 0 (Boot() has not run yet): MarkGood must be rejected.
	c.Current = "0.2.0"
	if err := c.MarkGood(); err == nil {
		t.Fatal("MarkGood succeeded with Boots == 0")
	}
	p, err := c.ReadPending()
	if err != nil || p == nil || p.State != "awaiting" || p.Good {
		t.Fatalf("pending mutated by rejected MarkGood: %+v %v", p, err)
	}

	// After Boot() runs on 0.2.0, MarkGood succeeds.
	if rb, err := c.Boot(); rb || err != nil {
		t.Fatalf("Boot failed: rb=%v err=%v", rb, err)
	}
	if err := c.MarkGood(); err != nil {
		t.Fatalf("MarkGood after Boot failed: %v", err)
	}
	p, err = c.ReadPending()
	if err != nil || p == nil || p.State != "good" || !p.Good {
		t.Fatalf("expected good pending: %+v %v", p, err)
	}
}

func TestH01ManualUpgradeToNewerVersionDoesNotRollback(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	inst, v2 := t.TempDir(), t.TempDir()
	write(t, inst, "wallpaperd", "v1")
	write(t, v2, "wallpaperd", "v2")
	srv, _ := feed(t, priv, v2, "0.2.0", nil, false)

	c := &Client{Feed: srv.URL, Pub: pub, Platform: "linux-amd64", Current: "0.1.0", Dir: inst}
	m, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}

	// Simulate manual installer overwrite to 0.3.0.
	write(t, inst, "wallpaperd", "v3-manual")
	c3 := &Client{Dir: inst, Current: "0.3.0", Platform: "linux-amd64"}

	if _, ok := c3.CanRollback(); ok {
		t.Fatal("CanRollback must be false for manually installed 0.3.0")
	}
	if err := c3.Rollback(); err == nil {
		t.Fatal("Rollback must reject version mismatch")
	}
	if read(t, inst, "wallpaperd") != "v3-manual" {
		t.Fatal("manual 0.3.0 binary was modified by rejected Rollback")
	}

	// Boot on 0.3.0 cleans up stale pending/rollback instead of rolling back 0.3.0 to 0.1.0.
	for i := 0; i < MaxBoots+2; i++ {
		rb, err := c3.Boot()
		if rb || err != nil {
			t.Fatalf("Boot %d on 0.3.0: rb=%v err=%v", i, rb, err)
		}
	}
	if read(t, inst, "wallpaperd") != "v3-manual" {
		t.Fatalf("0.3.0 binary rolled back: got %q", read(t, inst, "wallpaperd"))
	}
	if p, err := c3.ReadPending(); err != nil || p != nil {
		t.Fatalf("stale pending.json not cleaned up: %+v %v", p, err)
	}
	if _, err := os.Stat(c3.sd("rollback")); !os.IsNotExist(err) {
		t.Fatalf("stale rollback dir not cleaned up: %v", err)
	}
}

func TestH02ApplyPreservesOldRollbackWhenWritePendingFails(t *testing.T) {
	c, m, _ := transactionFixture(t)
	// Seed a confirmed good update with an existing rollback directory.
	write(t, c.Dir, ".update/rollback/a", "v0-backup")
	if err := c.persistPending(&Pending{
		From:  "0.0.9",
		To:    "0.1.0",
		State: "good",
		Good:  true,
		Plan:  []PlanEntry{{Path: "a", Existed: true}},
	}); err != nil {
		t.Fatal(err)
	}

	c.writeState = func(*Pending) error { return errInjected }
	if _, err := c.Apply(context.Background(), m, nil); !errors.Is(err, errInjected) {
		t.Fatalf("expected injected writePending error, got %v", err)
	}
	// Old rollback and pending.json must survive intact.
	if got := read(t, c.Dir, ".update/rollback/a"); got != "v0-backup" {
		t.Fatalf("old rollback destroyed: got %q", got)
	}
	p, err := c.ReadPending()
	if err != nil || p == nil || p.State != "good" || p.From != "0.0.9" {
		t.Fatalf("old pending lost: %+v %v", p, err)
	}
	if matches, _ := filepath.Glob(c.sd("rollback.prev-*")); len(matches) != 0 {
		t.Fatalf("rollback.prev left behind after restore: %v", matches)
	}
}

func TestH02RollbackPreVerifiesBackupsBeforeModifyingInstall(t *testing.T) {
	c, m, _ := transactionFixture(t)
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	c.Current = "0.2.0"

	// Corrupt rollback by removing one required backup file.
	if err := os.Remove(c.sd("rollback", "lib", "b")); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.CanRollback(); ok {
		t.Fatal("CanRollback must return false when a backup file is missing")
	}

	beforeInstall := installSnapshot(t, c.Dir)
	if err := c.Rollback(); err == nil {
		t.Fatal("Rollback succeeded despite missing backup")
	}
	afterInstall := installSnapshot(t, c.Dir)
	for k, v := range beforeInstall {
		if afterInstall[k] != v {
			t.Fatalf("file %q modified before pre-verification failed: %q -> %q", k, v, afterInstall[k])
		}
	}
	if len(beforeInstall) != len(afterInstall) {
		t.Fatalf("install file count changed: %d -> %d", len(beforeInstall), len(afterInstall))
	}
	p, err := c.ReadPending()
	if err != nil || p == nil || p.State != "awaiting" {
		t.Fatalf("pending state mutated despite pre-check failure: %+v %v", p, err)
	}
}

func TestH03SafeRelAndCaseInsensitiveDedupAndDirTarget(t *testing.T) {
	for _, bad := range []string{
		"", ".", "..", "../x", "a/../b", "/abs", `a\b`,
		".update", ".update/pending.json", ".UPDATE/pending.json", ".Update/x",
		"a:b", "C:foo", "dir/a:stream",
		"CON", "con", "CON.txt", "dir/PRN", "AUX.dll", "NUL", "COM1", "com9.exe", "LPT1", "lpt9.dat",
		"foo.", "foo ", "dir/bar.", "a\x00b",
	} {
		if safeRel(bad) {
			t.Errorf("safeRel(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"wallpaperd", "wallpaperd.exe", "mpv/mpv", "LICENSE", "Info.plist", "sub/config.json"} {
		if !safeRel(good) {
			t.Errorf("safeRel(%q) = false, want true", good)
		}
	}

	// Case-insensitive duplicate rejection in Verify and Apply.
	pub, priv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256([]byte("x"))
	hexSum := hex.EncodeToString(sum[:])
	dupManifest := &Manifest{
		Schema:   1,
		Version:  "0.2.0",
		Platform: "linux-amd64",
		Files: []File{
			{Path: "foo.dll", Size: 1, SHA256: hexSum},
			{Path: "FOO.DLL", Size: 1, SHA256: hexSum},
		},
	}
	mb := dupManifest.Encode()
	sig := Sign(priv, mb)
	if _, err := Verify(pub, mb, sig); err == nil {
		t.Fatal("Verify accepted case-insensitive duplicate paths")
	}
	c := &Client{Dir: t.TempDir(), Current: "0.1.0", Platform: "linux-amd64", Feed: "https://updates.example"}
	if _, err := c.Apply(context.Background(), dupManifest, nil); err == nil {
		t.Fatal("Apply accepted case-insensitive duplicate paths")
	}

	// Existing directory at target path must be rejected before modifying files.
	inst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(inst, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirManifest := &Manifest{
		Schema:   1,
		Version:  "0.2.0",
		Platform: "linux-amd64",
		Files:    []File{{Path: "subdir", Size: 1, SHA256: hexSum}},
	}
	cDir := &Client{Dir: inst, Current: "0.1.0", Platform: "linux-amd64", Feed: "https://updates.example"}
	if _, err := cDir.Apply(context.Background(), dirManifest, nil); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory target error, got %v", err)
	}
}

func TestM22RedirectSSRFAndDowngradeRejected(t *testing.T) {
	targets := []string{
		"http://updates.example.com/evil",
		"https://127.0.0.1/secret",
		"http://127.0.0.1/secret",
		"https://localhost/secret",
		"https://10.0.0.1/internal",
		"https://192.168.1.1/router",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/secret",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			c := &Client{
				Feed:     "https://updates.example.com",
				Platform: "linux-amd64",
				HTTP: &http.Client{
					Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
						if r.URL.Host == "updates.example.com" && r.URL.Scheme == "https" {
							h := make(http.Header)
							h.Set("Location", target)
							return &http.Response{
								StatusCode: http.StatusFound,
								Header:     h,
								Body:       io.NopCloser(strings.NewReader("")),
								Request:    r,
							}, nil
						}
						t.Errorf("followed forbidden redirect to %s", r.URL)
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader("leaked")),
							Request:    r,
						}, nil
					}),
				},
			}
			if _, err := c.get(context.Background(), "https://updates.example.com/linux-amd64/manifest.json", 1024); err == nil {
				t.Fatalf("redirect to %s was not rejected", target)
			}
		})
	}
}

func TestM22PlatformMismatchAndPermissionsAndStreamingLimit(t *testing.T) {
	// Platform mismatch rejected by Apply.
	c, m, _ := transactionFixture(t)
	c.Platform = "windows-amd64"
	if _, err := c.Apply(context.Background(), m, nil); err == nil || !strings.Contains(err.Error(), "want windows-amd64") {
		t.Fatalf("expected platform mismatch error, got %v", err)
	}

	// File permission classification.
	for path, want := range map[string]os.FileMode{
		"wallpaperd":               0o755,
		"wallpaperd.exe":           0o755,
		"MacOS/wallpaperd":         0o755,
		"mpv/mpv":                  0o755,
		"libmpv.so.2":              0o755,
		"libmpv.dylib":             0o755,
		"LICENSE":                  0o644,
		"README":                   0o644,
		"THIRD_PARTY_LICENSES.txt": 0o644,
		"Info.plist":               0o644,
		"config.json":              0o644,
		"notes.txt":                0o644,
	} {
		if got := fileModeForPath(path); got != want {
			t.Errorf("fileModeForPath(%q) = %o, want %o", path, got, want)
		}
	}

	// Streaming download rejects oversized response without leaving staged file.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("this-payload-is-way-too-large"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "file.json")
	dlClient := &Client{Feed: srv.URL, Platform: "linux-amd64"}
	if _, err := dlClient.downloadToFile(context.Background(), srv.URL+"/file.json", dst, 4, strings.Repeat("0", 64), 0o644); err == nil {
		t.Fatal("oversized stream accepted")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("partial oversized file left behind: %v", err)
	}

	// On Unix, verify staged data file does not receive execute bits.
	if runtime.GOOS != "windows" {
		payload := []byte(`{"ok":true}`)
		sum := sha256.Sum256(payload)
		okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(payload)
		}))
		defer okSrv.Close()
		out := filepath.Join(t.TempDir(), "data.json")
		if _, err := dlClient.downloadToFile(context.Background(), okSrv.URL+"/data.json", out, int64(len(payload)), hex.EncodeToString(sum[:]), fileModeForPath("data.json")); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o111 != 0 {
			t.Fatalf("data file has execute bit: %o", st.Mode().Perm())
		}
	}
}
