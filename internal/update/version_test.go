package update

import (
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictSemVerOrdering(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.2", "1.0.0-rc.10", "1.0.0", "1.0.1", "999999999999999999999.0.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			if Newer(a, b) != (i > j) {
				t.Errorf("Newer(%q, %q)", a, b)
			}
		}
	}
	for _, bad := range []string{"dev", "", "v1.0.0", "1", "1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-rc.01", "1.2.3-", "1.2.3+", "1.2.3-a..b", "1.2.3+a..b", "1.2.3\n", " 1.2.3", "1.2.3-💥"} {
		if _, err := parseVersion(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
		if Newer("2.0.0", bad) || Newer(bad, "0.0.0") {
			t.Errorf("ordered invalid %q", bad)
		}
		if _, err := BuildManifest(t.TempDir(), bad, "linux-amd64", ""); err == nil {
			t.Errorf("published %q", bad)
		}
	}
	for _, a := range []string{"1.2.3+001", "1.2.3+build.2", "1.2.3"} {
		if _, err := parseVersion(a); err != nil {
			t.Fatal(err)
		}
		if Newer(a, "1.2.3") || Newer("1.2.3", a) {
			t.Fatal("build metadata affects precedence")
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestManifestRejectionBeforeSideEffects(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, current, version string
		schema                 int
	}{
		{"unknown-schema", "1.0.0", "2.0.0", 2},
		{"missing-schema", "1.0.0", "2.0.0", 0},
		{"malformed-target", "1.0.0", "2.0", 1},
		{"malformed-current", "broken", "2.0.0", 1},
		{"dev-current", "dev", "2.0.0", 1},
		{"dev-target", "1.0.0", "dev", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "app", "original")
			write(t, dir, ".update/staging/keep", "staging")
			write(t, dir, ".update/rollback/keep", "rollback")
			m := &Manifest{Schema: tc.schema, Version: tc.version, Platform: "linux-amd64", Files: []File{{Path: "app", Size: 3}}, Remove: []string{"app"}}
			data := m.Encode()
			sig := Sign(priv, data)
			downloads := 0
			c := &Client{Current: tc.current, Dir: dir, Platform: m.Platform, Feed: "https://updates.example", Pub: pub,
				HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					var body string
					switch filepath.Base(r.URL.Path) {
					case "manifest.json":
						body = string(data)
					case "manifest.json.sig":
						body = sig
					default:
						downloads++
						body = "new"
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})}}
			if tc.schema != 1 {
				if _, err := Verify(pub, data, sig); err == nil || !strings.Contains(err.Error(), "unsupported manifest schema") {
					t.Fatal("expected schema rejection for valid signature", err)
				}
			}
			if _, err := c.Check(context.Background()); err == nil {
				t.Fatal("Check accepted invalid update")
			}
			if _, err := c.Apply(context.Background(), m, nil); err == nil {
				t.Fatal("Apply accepted invalid update")
			}
			if downloads != 0 {
				t.Fatal("download side effect")
			}
			for path, want := range map[string]string{"app": "original", ".update/staging/keep": "staging", ".update/rollback/keep": "rollback"} {
				if got := read(t, dir, path); got != want {
					t.Fatalf("%s changed to %s", path, got)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, ".update/pending.json")); !os.IsNotExist(err) {
				t.Fatal("pending side effect", err)
			}
		})
	}
}
