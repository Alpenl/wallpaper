package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func metricTime() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) }
func metricSource(level visibility.Level) map[string]visibility.Level {
	return map[string]visibility.Level{config.MetricsDisplayKey("test-display"): level}
}
func metricLevel(m config.LocalMetrics, level visibility.Level) time.Duration {
	return time.Duration(m.Displays[config.MetricsDisplayKey("test-display")].LevelNS[level])
}

func TestMetricsChargesPreviousAcknowledgedLevel(t *testing.T) {
	var r runtimeMetrics
	var m config.LocalMetrics
	t0 := metricTime()
	r.sample(t0, &m, metricSource(visibility.L0))
	r.sample(t0.Add(10*time.Second), &m, metricSource(visibility.L1))
	r.sample(t0.Add(25*time.Second), &m, metricSource(visibility.L3))
	r.sample(t0.Add(30*time.Second), &m, metricSource(visibility.L4))
	r.sample(t0.Add(40*time.Second), &m, nil)
	r.sample(t0.Add(50*time.Second), &m, nil)
	want := [5]time.Duration{10 * time.Second, 15 * time.Second, 0, 5 * time.Second, 10 * time.Second}
	for i, n := range want {
		if got := metricLevel(m, visibility.Level(i)); got != n {
			t.Fatalf("level %d = %v, want %v", i, got, n)
		}
	}
}

func TestMetricsWeekRollover(t *testing.T) {
	var r runtimeMetrics
	var m config.LocalMetrics
	sunday := time.Date(2026, 10, 25, 23, 59, 40, 0, time.UTC)
	r.sample(sunday, &m, metricSource(visibility.L1))
	r.sample(sunday.Add(10*time.Second), &m, metricSource(visibility.L1))
	r.sample(sunday.Add(30*time.Second), &m, metricSource(visibility.L1))
	if m.WeekStart != "2026-10-26" || metricLevel(m, visibility.L1) != 10*time.Second {
		t.Fatalf("old week was not discarded/interval was not split: %+v", m)
	}
	// A backward adjustment across the week boundary retains the newer period
	// and does not recharge the already counted first 10 seconds of Monday.
	r.sample(sunday.Add(15*time.Second), &m, metricSource(visibility.L1))
	if !r.clockAdjusted {
		t.Fatal("backward week adjustment was not exposed to the UI")
	}
	r.sample(sunday.Add(35*time.Second), &m, metricSource(visibility.L1))
	if r.clockAdjusted {
		t.Fatal("clock warning did not clear after a reliable interval")
	}
	if m.WeekStart != "2026-10-26" || metricLevel(m, visibility.L1) != 15*time.Second {
		t.Fatalf("backward week change double-counted or erased history: %+v", m)
	}
}

func TestMetricsRestartAndLongGapDoNotCountOfflineTime(t *testing.T) {
	var r runtimeMetrics
	var m config.LocalMetrics
	t0 := metricTime()
	r.sample(t0, &m, metricSource(visibility.L1))
	r.sample(t0.Add(10*time.Second), &m, metricSource(visibility.L1))
	// Serialization contains no live timer anchor, so restart adds no downtime.
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored config.LocalMetrics
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	r = runtimeMetrics{}
	r.sample(t0.Add(time.Hour), &restored, metricSource(visibility.L1))
	r.sample(t0.Add(time.Hour+10*time.Second), &restored, metricSource(visibility.L1))
	r.sample(t0.Add(2*time.Hour), &restored, metricSource(visibility.L1))
	if got := metricLevel(restored, visibility.L1); got != 20*time.Second {
		t.Fatalf("offline/suspended-process time was counted: %v", got)
	}
	r.sample(t0.Add(2*time.Hour+10*time.Second), &restored, metricSource(visibility.L1))
	if got := metricLevel(restored, visibility.L1); got != 30*time.Second {
		t.Fatal(got)
	}
}

func TestMetricsBackwardClockNeverDoubleCounts(t *testing.T) {
	var r runtimeMetrics
	var m config.LocalMetrics
	t0 := metricTime()
	r.sample(t0, &m, metricSource(visibility.L1))
	r.sample(t0.Add(20*time.Second), &m, metricSource(visibility.L1))
	for _, offset := range []time.Duration{10, 15, 20, 25} {
		r.sample(t0.Add(offset*time.Second), &m, metricSource(visibility.L1))
	}
	if got := metricLevel(m, visibility.L1); got != 25*time.Second {
		t.Fatal(got)
	}
}

func TestMetricsMonotonicClockJumpRejected(t *testing.T) {
	// This checks the wall-only test-clock path. Real time.Now anchors use the
	// same guard plus a monotonic/wall-delta disagreement check.
	var r runtimeMetrics
	var m config.LocalMetrics
	t0 := metricTime()
	r.sample(t0, &m, metricSource(visibility.L1))
	r.sample(t0.Add(24*time.Hour), &m, metricSource(visibility.L1))
	if got := metricLevel(m, visibility.L1); got != 0 {
		t.Fatal(got)
	}
}

func TestMetricsCountersAndDisplayHistoryBounded(t *testing.T) {
	var r runtimeMetrics
	var m config.LocalMetrics
	t0 := metricTime()
	for i := 0; i < config.MaxMetricDisplays+20; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		r.sample(now, &m, map[string]visibility.Level{config.MetricsDisplayKey(fmt.Sprint(i)): visibility.L1})
	}
	if len(m.Displays) != config.MaxMetricDisplays {
		t.Fatal(len(m.Displays))
	}
	key := config.MetricsDisplayKey("test-display")
	m.Displays[key] = config.DisplayMetrics{LevelNS: [5]int64{int64(config.MetricWeek) - 1, 0, 0, 0, 0}}
	r.sources = metricSource(visibility.L1)
	r.sample(r.last.Add(time.Second), &m, metricSource(visibility.L1))
	if m.Displays[key].LevelNS[1] != 1 {
		t.Fatal(m.Displays[key])
	}
}

func newMetricApp(t *testing.T) (*App, *screen) {
	t.Helper()
	saver := &config.Saver{ReadOnly: true}
	store := &config.Store{State: config.State{SchemaVersion: config.StateVersion}, StateSaver: saver, Config: config.Defaults()}
	a := New(Deps{Store: store, Host: newHost(1)})
	a.ctl = visibility.NewController(VisConfig(store.Config))
	a.ctl.AddDisplay(0, 0)
	s := &screen{worker: &screenWorker{}, n: 0, d: desktop.Display{ID: "test-display"}, item: &config.Item{ID: "private-wallpaper-title", Type: "video", Dir: "/private/media/path"}, r: &fakeR{}, started: true, metricsReady: true, metricsLevel: visibility.L1}
	a.screens[s.d.ID] = s
	return a, s
}

func TestAppMetricsOnlyRelevantWallpaperRuntime(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable func(*App, *screen)
	}{
		{"empty-library", func(a *App, s *screen) { s.item = nil }},
		{"static-image", func(a *App, s *screen) { s.item.Type = "image" }},
		{"not-started", func(a *App, s *screen) { s.started = false }},
		{"unacknowledged", func(a *App, s *screen) { s.metricsReady = false }},
		{"missing-renderer", func(a *App, s *screen) { s.r = nil }},
		{"safe-mode", func(a *App, s *screen) { a.d.SafeMode = true }},
		{"system-suspend", func(a *App, s *screen) { a.global[visibility.Suspend] = true }},
		{"disconnected", func(a *App, s *screen) { delete(a.screens, s.d.ID) }},
		{"recovering", func(a *App, s *screen) { a.ctl.Display(0).RendererDone(0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := newMetricApp(t)
			tc.disable(a, s)
			a.sampleMetrics(metricTime())
			a.sampleMetrics(metricTime().Add(30 * time.Second))
			if got := metricLevel(a.d.Store.State.Metrics, visibility.L1); got != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestAppMetricsSuspendDisconnectAndL4(t *testing.T) {
	a, s := newMetricApp(t)
	t0 := metricTime()
	a.sampleMetrics(t0)
	a.global[visibility.Suspend] = true
	a.sampleMetrics(t0.Add(10 * time.Second))
	a.global[visibility.Suspend] = false
	a.sampleMetrics(t0.Add(30 * time.Second))
	a.sampleMetrics(t0.Add(40 * time.Second))
	delete(a.screens, s.d.ID)
	a.sampleMetrics(t0.Add(45 * time.Second))
	a.sampleMetrics(t0.Add(60 * time.Second))
	s.metricsLevel, s.r, s.started = visibility.L4, nil, false
	a.screens[s.d.ID] = s
	a.sampleMetrics(t0.Add(65 * time.Second))
	a.sampleMetrics(t0.Add(75 * time.Second))
	v := a.displayMetrics(string(s.d.ID))
	if v.Levels[1] != 25*time.Second || v.Levels[4] != 10*time.Second || v.Paused != 35*time.Second {
		t.Fatal(v)
	}
	// Editing a published scalar/array snapshot cannot change persistent state.
	v.Levels[1] = 0
	if a.displayMetrics(string(s.d.ID)).Levels[1] != 25*time.Second {
		t.Fatal("aliased view")
	}
	data, _ := json.Marshal(a.d.Store.State.Metrics)
	for _, forbidden := range []string{"test-display", "private", "wallpaper", "path", "executable", "window"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("private identifier in metrics", string(data))
		}
	}
}

func TestMetricPauseRequiresRendererAcknowledgment(t *testing.T) {
	// The orchestration still renders live backend stats, never metric fixtures.
	e := setup(t, 1, true)
	eventually(t, "renderer started", func() bool { v := e.a.View(); return len(v.Displays) > 0 && v.Displays[0].Backend == "fake" })
	e.a.SetPausedAll(true)
	eventually(t, "pause acknowledged", func() bool {
		ack := make(chan bool, 1)
		e.a.Do(func() { s := e.a.screens["A"]; ack <- s.metricsReady && s.metricsLevel == visibility.L1 })
		return <-ack
	})
	e.a.Do(func() {
		a := e.a
		a.metrics.last = time.Now().Add(-5 * time.Second)
	})
	eventually(t, "paused duration published", func() bool { return e.a.View().Displays[0].Metrics.Paused >= 5*time.Second })
	e.cancel()
	e.a.Wait()
	reopened, err := config.Open(e.store.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.State.Metrics.Displays[config.MetricsDisplayKey("A")].LevelNS[1]; got < int64(5*time.Second) {
		t.Fatalf("shutdown did not persist %d", got)
	}
}

type metricFailureRenderer struct {
	fakeR
	pauseAttempt chan struct{}
	stopAttempt  chan struct{}
	failPause    bool
	failStop     bool
}

func (r *metricFailureRenderer) SetLevel(level visibility.Level) error {
	if level > visibility.L0 && r.failPause {
		select {
		case r.pauseAttempt <- struct{}{}:
		default:
		}
		return errors.New("pause rejected")
	}
	return r.fakeR.SetLevel(level)
}
func (r *metricFailureRenderer) Close() error {
	if r.failStop {
		select {
		case r.stopAttempt <- struct{}{}:
		default:
		}
		return errors.New("stop rejected")
	}
	return r.fakeR.Close()
}

func TestMetricRejectedPauseIsNotSavedDuration(t *testing.T) {
	r := &metricFailureRenderer{fakeR: fakeR{done: make(chan error, 1)}, pauseAttempt: make(chan struct{}, 1), failPause: true}
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer { return r }
	})
	eventually(t, "renderer active", func() bool { v := e.a.View(); return len(v.Displays) > 0 && v.Displays[0].Backend == "fake" })
	e.a.SetPausedAll(true)
	select {
	case <-r.pauseAttempt:
	case <-time.After(time.Second):
		t.Fatal("no pause attempt")
	}
	eventually(t, "bounded recovery exhausted", func() bool { return e.a.View().Displays[0].State == "failed" })
	e.a.Do(func() { e.a.metrics.last = time.Now().Add(-5 * time.Second) })
	e.sync()
	before := e.a.View().Displays[0].Metrics.Paused
	e.a.Do(func() { e.a.metrics.last = time.Now().Add(-5 * time.Second) })
	e.sync()
	if got := e.a.View().Displays[0].Metrics.Paused; got != before {
		t.Fatalf("unconfirmed recovery counted pause: %v -> %v", before, got)
	}

}

func TestMetricRejectedL4StopIsNotSavedDuration(t *testing.T) {
	r := &metricFailureRenderer{fakeR: fakeR{done: make(chan error, 1)}, stopAttempt: make(chan struct{}, 1), failStop: true}
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(string, config.Config, desktop.DisplayID) render.Renderer { return r }
	})
	eventually(t, "renderer active", func() bool { v := e.a.View(); return len(v.Displays) > 0 && v.Displays[0].Backend == "fake" })
	e.a.Do(func() { e.a.signal(visibility.LowBattery, "", true, "test") })
	select {
	case <-r.stopAttempt:
	case <-time.After(time.Second):
		t.Fatal("no stop attempt")
	}
	done := make(chan bool, 1)
	e.a.Do(func() { done <- e.a.screens["A"].metricsReady })
	if <-done {
		t.Fatal("rejected close counted as successful L4")
	}
	e.a.Do(func() { e.a.metrics.last = time.Now().Add(-5 * time.Second) })
	eventually(t, "unconfirmed L4 view", func() bool { return e.a.View().Displays[0].Level == "" })
	if got := e.a.View().Displays[0].Metrics.Paused; got != 0 {
		t.Fatalf("failed L4 counted %v", got)
	}
}
