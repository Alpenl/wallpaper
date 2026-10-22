// Package shader contains native shader rendering policy and translation.
// It has no dependency on the WebView renderer.
package shader

import (
	"math"
	"time"
)

// Adaptive uses consecutive samples and asymmetric thresholds to avoid scale
// oscillation. Invalid/disjoint/unavailable GPU measurements must be ignored.
type Adaptive struct {
	Scale, Ceiling float64
	hot, cool      int
}

func NewAdaptive(ceiling float64) Adaptive {
	if math.IsNaN(ceiling) || ceiling < .5 || ceiling > 1 {
		ceiling = .75
	}
	return Adaptive{Scale: ceiling, Ceiling: ceiling}
}
func (a *Adaptive) Sample(ms float64) float64 {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms <= 0 {
		return a.Scale
	}
	if ms > 4 {
		a.hot++
		a.cool = 0
	} else if ms < 2.8 {
		a.cool++
		a.hot = 0
	} else {
		a.hot = 0
		a.cool = 0
	}
	if a.hot >= 3 {
		a.Scale = math.Max(.5, math.Round((a.Scale-.05)*100)/100)
		a.hot = 0
	}
	if a.cool >= 120 {
		a.Scale = math.Min(a.Ceiling, math.Round((a.Scale+.05)*100)/100)
		a.cool = 0
	}
	return a.Scale
}

// Timeline advances only while presenting. Changing pause state resets the
// sampling anchor, so the first resumed frame has zero delta.
type Timeline struct {
	elapsed time.Duration
	last    time.Time
	paused  bool
	Frame   int32
}

func (t *Timeline) Pause(paused bool) {
	if t.paused != paused {
		t.last = time.Time{}
	}
	t.paused = paused
}
func (t *Timeline) Tick(now time.Time) (seconds, delta float32) {
	if t.paused {
		return float32(t.elapsed.Seconds()), 0
	}
	if !t.last.IsZero() && now.After(t.last) {
		d := now.Sub(t.last)
		t.elapsed += d
		delta = float32(d.Seconds())
	}
	t.last = now
	t.Frame++
	return float32(t.elapsed.Seconds()), delta
}
