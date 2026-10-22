package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayOptionsNormalizeAndMigration(t *testing.T) {
	a := Assignments{Options: map[string]DisplayOptions{"old": {Fit: "fit", Speed: 2, Paused: true, Profile: "saver"}}}
	if !a.Migrate("new", []string{"old"}) {
		t.Fatal("not migrated")
	}
	if got := a.OptionsFor("new"); got.Fit != "fit" || got.Speed != 2 || !got.Paused || got.Profile != "saver" {
		t.Fatal(got)
	}
	if _, ok := a.Options["old"]; ok {
		t.Fatal("legacy option retained")
	}
	bad := DisplayOptions{Fit: "bad", Speed: math.NaN(), Profile: "bad"}
	bad.Normalize()
	if bad != DefaultDisplayOptions() {
		t.Fatal(bad)
	}
	if a.OptionsFor("missing") != DefaultDisplayOptions() {
		t.Fatal("missing defaults")
	}
}

func TestOldConfigSeedsNewPrivacySafeDefaults(t *testing.T) {
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := DefaultPaths()
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	raw := `{"schemaVersion":1,"render":{"windowsBackend":"mpv"},"perf":{"fpsCap":60},"pause":{"onCovered":false}}`
	if err := os.WriteFile(filepath.Join(p.Config, "config.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Config
	if c.SchemaVersion != 2 || !c.Render.Muted || !c.Render.ResumePosition || c.Render.AutoTranscode || !c.Startup.OpenSettings || c.Perf.FPSCap != 60 || c.Pause.OnCovered {
		t.Fatal(c)
	}
	if _, err := os.Stat(filepath.Join(p.Config, "config.json.v1")); err != nil {
		t.Fatal("missing migration backup", err)
	}
}

func TestRuleMatchingDoesNotMatchTitlesOrSubstrings(t *testing.T) {
	c := Defaults()
	c.Pause.AppRules = []AppRule{{Executable: "/usr/bin/game", Action: "pause"}, {Executable: "game", Action: "saver"}, {Executable: "bad", Action: "nonsense"}}
	c.Normalize()
	if len(c.Pause.AppRules) != 1 || c.Pause.AppAction("/opt/game") != "pause" || c.Pause.AppAction("game settings") == "pause" || c.Pause.AppAction("mygame") == "pause" {
		t.Fatal(c.Pause)
	}
	copy := c.Clone()
	copy.Pause.AppRules[0].Action = "ignore"
	if c.Pause.AppRules[0].Action != "pause" {
		t.Fatal("clone aliases rules")
	}
	c.Perf.RenderScale = math.Inf(1)
	c.Render.Hwdec = "--unsafe"
	c.Normalize()
	if c.Perf.RenderScale != 1 || c.Render.Hwdec != "" {
		t.Fatal(c)
	}
}
