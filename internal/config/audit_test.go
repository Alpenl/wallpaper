package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestH26LoadRejectsNullAndMalformedSchemaVersions(t *testing.T) {
	t.Run("null falls back to bak", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.json")
		if err := os.WriteFile(p, []byte("null"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+".bak", []byte(`{"schemaVersion":2,"ui":{"lang":"en"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got := Defaults()
		if err := Load(p, ConfigVersion, nil, &got); err != nil {
			t.Fatalf("expected fallback to .bak, got %v", err)
		}
		if got.UI.Lang != "en" {
			t.Fatalf("did not load from .bak: %+v", got.UI)
		}
	})

	for _, tc := range []struct {
		name string
		body string
	}{
		{"null without bak", "null"},
		{"array without bak", "[]"},
		{"primitive without bak", "123"},
		{"negative schemaVersion", `{"schemaVersion": -1}`},
		{"float schemaVersion", `{"schemaVersion": 1.5}`},
		{"scientific notation 1e20", `{"schemaVersion": 1e20}`},
		{"string schemaVersion", `{"schemaVersion": "1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			migrated := false
			var got Config
			err := Load(p, ConfigVersion, map[int]Migration{
				0: func(map[string]any) error { migrated = true; return nil },
			}, &got)
			if err == nil {
				t.Fatalf("Load(%s) succeeded unexpectedly", tc.body)
			}
			if errors.Is(err, ErrNewerSchema) {
				t.Fatalf("Load(%s) returned ErrNewerSchema instead of error", tc.body)
			}
			if migrated {
				t.Fatalf("Load(%s) ran migration", tc.body)
			}
			if matches, _ := filepath.Glob(p + ".v*"); len(matches) != 0 {
				t.Fatalf("Load(%s) wrote unexpected migration backup: %v", tc.body, matches)
			}
		})
	}

	t.Run("huge future integer is ErrNewerSchema without backup", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.json")
		raw := `{"schemaVersion": 999999999999999999999, "ui": {"lang": "en"}}`
		if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		got := Defaults()
		err := Load(p, ConfigVersion, nil, &got)
		if !errors.Is(err, ErrNewerSchema) {
			t.Fatalf("expected ErrNewerSchema, got %v", err)
		}
		if got.UI.Lang != "en" {
			t.Fatalf("known fields not decoded read-only: %+v", got.UI)
		}
		if matches, _ := filepath.Glob(p + ".v*"); len(matches) != 0 {
			t.Fatalf("wrote unexpected migration backup: %v", matches)
		}
		after, err := os.ReadFile(p)
		if err != nil || string(after) != raw {
			t.Fatalf("future file modified: %q %v", after, err)
		}
	})
}

func TestM23AtomicBakPreservedAndLibraryNormalizedOnOpen(t *testing.T) {
	t.Run("existing bak preserved when bak.tmp fails", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.json")
		c1 := Defaults()
		c1.Pause.LowBatteryPct = 15
		if err := WriteAtomic(p, c1); err != nil {
			t.Fatal(err)
		}
		c2 := Defaults()
		c2.Pause.LowBatteryPct = 25
		if err := WriteAtomic(p, c2); err != nil {
			t.Fatal(err)
		}
		bakBefore, err := os.ReadFile(p + ".bak")
		if err != nil {
			t.Fatal(err)
		}
		// Block atomic backup staging by placing a directory at .bak.tmp.
		if err := os.Mkdir(p+".bak.tmp", 0o700); err != nil {
			t.Fatal(err)
		}
		c3 := Defaults()
		c3.Pause.LowBatteryPct = 35
		err = WriteAtomic(p, c3)
		if err == nil || !strings.Contains(err.Error(), "save succeeded but backup failed") {
			t.Fatalf("expected backup failure warning, got %v", err)
		}
		bakAfter, err := os.ReadFile(p + ".bak")
		if err != nil || string(bakAfter) != string(bakBefore) {
			t.Fatalf("existing .bak was corrupted: got %q, want %q", bakAfter, bakBefore)
		}
	})

	t.Run("Open normalizes item Props and filters malformed PropertyDefs", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("WPAPP_HOME", home)
		p, err := DefaultPaths()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Ensure(); err != nil {
			t.Fatal(err)
		}
		libJSON := `{
  "schemaVersion": 4,
  "items": [
    {
      "id": "web1",
      "type": "web",
      "props": {
        "fit": "bogus",
        "speed": 99,
        "volume": 500,
        "audio": "loud",
        "shaderScale": 2.5,
        "shaderFPS": 45
      },
      "propertyDefs": [
        {"key": "good_bool", "type": "bool", "default": true},
        {"key": "good_bool", "type": "bool", "default": false},
        {"key": "bad_slider_no_range", "type": "slider", "default": 5},
        {"key": "bad_slider_range", "type": "slider", "default": 5, "min": 10, "max": 1},
        {"key": "bad_type", "type": "unknown", "default": 1},
        {"key": "bad_color", "type": "color", "default": "not-a-color"},
        {"key": "123invalid", "type": "bool", "default": true},
        {"key": "missing_default", "type": "text"},
        {"key": "empty_select", "type": "select", "default": "a"},
        {"key": "good_color", "type": "color", "default": "#112233"}
      ]
    }
  ]
}`
		if err := os.WriteFile(filepath.Join(p.Config, "library.json"), []byte(libJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Library.Items) != 1 {
			t.Fatalf("items = %+v", s.Library.Items)
		}
		it := s.Library.Items[0]
		if it.Props != (ItemProps{}) {
			t.Fatalf("Props not normalized: %+v", it.Props)
		}
		if len(it.PropertyDefs) != 2 || it.PropertyDefs[0].Key != "good_bool" || it.PropertyDefs[1].Key != "good_color" {
			t.Fatalf("PropertyDefs not filtered cleanly: %+v", it.PropertyDefs)
		}
	})
}

func TestL01RelativeWPAPPHomeNormalizedToAbsolute(t *testing.T) {
	t.Setenv("WPAPP_HOME", filepath.Join("relative-test-dir", "sub"))
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"Config":  p.Config,
		"Library": p.Library,
		"Cache":   p.Cache,
		"Logs":    p.Logs,
	} {
		if !filepath.IsAbs(dir) {
			t.Errorf("%s is not absolute: %q", name, dir)
		}
	}
}
