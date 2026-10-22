package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateMetricsMigrationAndClone(t *testing.T) {
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := DefaultPaths()
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.Config, "state.json")
	old := []byte(`{"schemaVersion":1,"cleanShutdown":true,"starts":["2026-10-21T01:00:00Z"],"positions":{"screen|item":12.5}}`)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.State.SchemaVersion != StateVersion || !s.State.CleanShutdown || s.State.Positions["screen|item"] != 12.5 || len(s.State.Starts) != 1 || len(s.State.Metrics.Displays) != 0 {
		t.Fatal(s.State)
	}
	if b, err := os.ReadFile(path + ".v1"); err != nil || string(b) != string(old) {
		t.Fatal("migration backup missing", err)
	}
	key := MetricsDisplayKey("display")
	s.State.Metrics = LocalMetrics{WeekStart: "2026-10-19", Displays: map[string]DisplayMetrics{key: {LevelNS: [5]int64{1, 2, 3, 4, 5}}}}
	cp := s.State.Clone()
	cp.Starts[0] = time.Time{}
	cp.Positions["screen|item"] = 0
	delete(cp.Metrics.Displays, key)
	if s.State.Starts[0].IsZero() || s.State.Positions["screen|item"] != 12.5 || len(s.State.Metrics.Displays) != 1 {
		t.Fatal("State.Clone aliases mutable state")
	}
	if err := WriteAtomic(path, s.State); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(p)
	if err != nil || reloaded.State.Metrics.Displays[key].LevelNS[4] != 5 {
		t.Fatal("metrics did not persist", err)
	}
}

func TestStateNewerSchemaRemainsReadOnlyWithMetrics(t *testing.T) {
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := DefaultPaths()
	_ = p.Ensure()
	path := filepath.Join(p.Config, "state.json")
	data := []byte(`{"schemaVersion":99,"future":"must-survive","metrics":{"weekStart":"2026-10-19"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil || !s.StateSaver.ReadOnly {
		t.Fatal(err)
	}
	s.StateSaver.Schedule(func() any { return s.State.Clone() })
	s.FlushAll()
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("newer state overwritten")
	}
}

func TestLocalMetricsNormalizeBoundsAndPrivacy(t *testing.T) {
	m := LocalMetrics{WeekStart: "2026-10-19", Displays: map[string]DisplayMetrics{"raw path /private": {}}}
	for i := 0; i < MaxMetricDisplays+5; i++ {
		m.Displays[MetricsDisplayKey(fmt.Sprint(i))] = DisplayMetrics{LevelNS: [5]int64{-1, int64(MetricWeek), 9999}, LastSeen: int64(i)}
	}
	m.Normalize()
	if len(m.Displays) != MaxMetricDisplays {
		t.Fatal(len(m.Displays))
	}
	if _, ok := m.Displays["raw path /private"]; ok {
		t.Fatal("invalid identifier retained")
	}
	for _, d := range m.Displays {
		if d.LevelNS[0] != 0 || d.LevelNS[1] != int64(MetricWeek) || d.LevelNS[2] != 0 {
			t.Fatal(d)
		}
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > 15000 {
		t.Fatal("unbounded serialization", len(data), err)
	}
	for _, period := range []string{"", "2026-10-20", "bogus"} {
		bad := LocalMetrics{WeekStart: period, Displays: m.Displays}
		bad.Normalize()
		if bad.WeekStart != "" || len(bad.Displays) != 0 {
			t.Fatal(bad)
		}
	}
}

func TestMetricWeekIsUTCAndMonday(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-10-26T01:30:00+02:00", "2026-10-19"},
		{"2026-10-25T20:30:00-04:00", "2026-10-26"},
		{"2027-01-01T10:00:00Z", "2026-12-28"},
	} {
		now, err := time.Parse(time.RFC3339, tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := MetricWeekStart(now).Format(time.DateOnly); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}

func TestTranscodeResolutionDefaultsAndNormalize(t *testing.T) {
	if Defaults().Render.TranscodeResolution != "display" {
		t.Fatal("wrong default")
	}
	for _, value := range []string{"display", "1080p", "1440p", "2160p", "", "garbage"} {
		c := Defaults()
		c.Render.TranscodeResolution = value
		c.Normalize()
		want := value
		if value == "" || value == "garbage" {
			want = "display"
		}
		if c.Render.TranscodeResolution != want {
			t.Fatal(c.Render.TranscodeResolution, want)
		}
	}
}
