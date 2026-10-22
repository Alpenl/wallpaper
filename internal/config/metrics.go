package config

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

// LocalMetrics contains current-week wall-clock runtime, not energy estimates.
// UTC Monday boundaries make the period stable across timezone/DST changes. Only
// hashed display identifiers and bounded counters are stored, never media paths,
// wallpaper names, window titles, or executable names.
type LocalMetrics struct {
	WeekStart string                    `json:"weekStart,omitempty"`
	Displays  map[string]DisplayMetrics `json:"displays,omitempty"`
}

type DisplayMetrics struct {
	LevelNS  [5]int64 `json:"levelNs"` // acknowledged L0 through L4 runtime
	LastSeen int64    `json:"lastSeen"`
}

const MaxMetricDisplays = 64
const MetricWeek = 7 * 24 * time.Hour

func MetricsDisplayKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

func MetricWeekStart(t time.Time) time.Time {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

// Normalize rejects invalid periods/keys, bounds counters to one display-week,
// and retains at most 64 recently active displays. It never advances the period:
// that requires a fresh runtime sample, not an app restart or file load.
func (m *LocalMetrics) Normalize() {
	start, err := time.Parse(time.DateOnly, m.WeekStart)
	if err != nil || !start.Equal(MetricWeekStart(start)) {
		*m = LocalMetrics{}
		return
	}
	for key, d := range m.Displays {
		b, err := hex.DecodeString(key)
		if err != nil || len(b) != 16 || hex.EncodeToString(b) != key {
			delete(m.Displays, key)
			continue
		}
		remaining := int64(MetricWeek)
		for i, n := range d.LevelNS {
			d.LevelNS[i] = min(max(n, 0), remaining)
			remaining -= d.LevelNS[i]
		}
		d.LastSeen = min(max(d.LastSeen, start.Unix()), start.Add(MetricWeek).Unix())
		m.Displays[key] = d
	}
	if len(m.Displays) > MaxMetricDisplays {
		keys := make([]string, 0, len(m.Displays))
		for key := range m.Displays {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			x, y := m.Displays[keys[i]].LastSeen, m.Displays[keys[j]].LastSeen
			return x > y || x == y && keys[i] < keys[j]
		})
		for _, key := range keys[MaxMetricDisplays:] {
			delete(m.Displays, key)
		}
	}
}

// Clone detaches every mutable collection for concurrent persistence/snapshots.
func (s State) Clone() State {
	s.Starts = append([]time.Time(nil), s.Starts...)
	positions := make(map[string]float64, len(s.Positions))
	for key, pos := range s.Positions {
		positions[key] = pos
	}
	s.Positions = positions
	displays := make(map[string]DisplayMetrics, len(s.Metrics.Displays))
	for key, d := range s.Metrics.Displays {
		displays[key] = d
	}
	s.Metrics.Displays = displays
	return s
}
