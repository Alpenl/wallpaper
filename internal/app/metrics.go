package app

import (
	"time"

	"wpapp/internal/config"
	"wpapp/internal/visibility"
)

// The normal checkpoint is 30 s. Discard an entire delayed interval instead of
// treating OS sleep, a suspended process, or a stalled event loop as saved time.
const maxMetricSampleGap = 45 * time.Second

// DisplayMetricsView is detached, local runtime evidence. Paused is a duration,
// not a measurement or estimate of electrical energy saved.
type DisplayMetricsView struct {
	WeekStart     string
	ClockAdjusted bool
	Levels        [5]time.Duration
	Paused        time.Duration
}

type runtimeMetrics struct {
	last          time.Time
	clockAdjusted bool
	sources       map[string]visibility.Level
}

// sample charges the previous acknowledged state until now, then installs the
// new state. The live sampling anchor is deliberately never persisted, so an app
// restart cannot add offline time. No counter is ever decremented on clock skew.
func (r *runtimeMetrics) sample(now time.Time, metrics *config.LocalMetrics, next map[string]visibility.Level) bool {
	previous, sources := r.last, r.sources
	r.clockAdjusted = false
	r.last, r.sources = now, next
	if !previous.IsZero() && now.Round(0).Before(previous.Round(0)) {
		r.last = previous
	}
	week := config.MetricWeekStart(now)
	key := week.Format(time.DateOnly)
	changed := false
	if metrics.WeekStart == "" || metrics.WeekStart < key {
		*metrics = config.LocalMetrics{WeekStart: key, Displays: map[string]config.DisplayMetrics{}}
		changed = true
	}
	// A backward clock/week adjustment must not erase a newer period or charge
	// that period with time from an earlier week.
	if metrics.WeekStart != key {
		r.clockAdjusted = true
		return changed
	}
	if previous.IsZero() {
		return changed
	}
	elapsed := now.Sub(previous) // monotonic whenever supplied by time.Now
	wallElapsed := now.Round(0).Sub(previous.Round(0))
	if wallElapsed <= 0 {
		r.clockAdjusted = true
		r.last = previous // retain the high-water mark; never count an interval twice
		return changed
	}
	if absDuration(elapsed-wallElapsed) > time.Second {
		r.clockAdjusted = true
		return changed
	}
	if elapsed <= 0 || elapsed > maxMetricSampleGap || wallElapsed > maxMetricSampleGap {
		return changed
	}
	// If this interval crosses Monday, only its part in the new week survives.
	elapsed = min(elapsed, now.UTC().Sub(week))
	if elapsed <= 0 {
		return changed
	}
	if metrics.Displays == nil {
		metrics.Displays = map[string]config.DisplayMetrics{}
	}
	for id, level := range sources {
		if level < visibility.L0 || level > visibility.L4 {
			continue
		}
		d, exists := metrics.Displays[id]
		if !exists && len(metrics.Displays) >= config.MaxMetricDisplays {
			// Bound disconnected display history without refusing a new display.
			oldest := ""
			for candidate, v := range metrics.Displays {
				if oldest == "" || v.LastSeen < metrics.Displays[oldest].LastSeen || v.LastSeen == metrics.Displays[oldest].LastSeen && candidate < oldest {
					oldest = candidate
				}
			}
			delete(metrics.Displays, oldest)
		}
		remaining := int64(config.MetricWeek)
		for _, n := range d.LevelNS {
			remaining -= n
		}
		d.LevelNS[level] += min(int64(elapsed), max(remaining, 0))
		d.LastSeen = now.Unix()
		metrics.Displays[id] = d
		changed = true
	}
	return changed
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (a *App) sampleMetrics(now time.Time) {
	next := make(map[string]visibility.Level)
	if !a.d.SafeMode && !a.global[visibility.Suspend] {
		for id, s := range a.screens {
			m := a.ctl.Display(s.n)
			if s.item == nil || s.item.Type == "image" || !s.metricsReady || m == nil || m.State() != visibility.StateActive {
				continue
			}
			if s.metricsLevel != visibility.L4 && (!s.started || s.r == nil) {
				continue
			}
			next[config.MetricsDisplayKey(string(id))] = s.metricsLevel
		}
	}
	if a.metrics.sample(now, &a.d.Store.State.Metrics, next) {
		a.saveState()
	}
}

func (a *App) displayMetrics(id string) DisplayMetricsView {
	m := a.d.Store.State.Metrics
	d := m.Displays[config.MetricsDisplayKey(id)]
	v := DisplayMetricsView{WeekStart: m.WeekStart, ClockAdjusted: a.metrics.clockAdjusted}
	for i, n := range d.LevelNS {
		v.Levels[i] = time.Duration(n)
		if i > 0 {
			v.Paused += time.Duration(n)
		}
	}
	return v
}
