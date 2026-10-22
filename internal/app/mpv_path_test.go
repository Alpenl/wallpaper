package app

import (
	"path/filepath"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/visibility"
)

// Model the terminal state reached after missing-mpv load failures without
// spending seconds waiting for each supervisor backoff in every portable run.
func exhaustMpvRetries(t *testing.T, e *env) {
	t.Helper()
	e.a.Do(func() {
		s := e.a.screens["A"]
		m := e.a.ctl.Display(s.n)
		for i := 0; i < VisConfig(e.store.Config).CrashLimit; i++ {
			m.RendererDone(e.a.now())
		}
		m.Drain()
		e.a.closeScreen(s, false)
	})
	eventually(t, "failed renderer with exhausted retries", func() bool {
		return byID(e.a.View(), "A").State == "failed"
	})
}

func TestMpvPathChangeRecoversFailedRendererAndKeepsPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "playing"
		if paused {
			name = "paused"
		}
		t.Run(name, func(t *testing.T) {
			e := setup(t, 1, true)
			eventually(t, "initial load", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
			if paused {
				e.a.SetPausedAll(true)
				eventually(t, "manual pause", func() bool { return e.cur("A").last() == visibility.L1 })
			}
			old := e.cur("A")
			exhaustMpvRetries(t, e)
			path := filepath.Join(t.TempDir(), "mpv.exe")
			e.a.UpdateConfig(func(c *config.Config) { c.Render.MpvPath = path })
			eventually(t, "corrected path retries the failed renderer", func() bool {
				r := e.cur("A")
				return r != old && r.isLoaded() && byID(e.a.View(), "A").State == "active"
			})
			r := e.cur("A")
			r.mu.Lock()
			startedPaused := r.paused
			r.mu.Unlock()
			if startedPaused != paused || byID(e.a.View(), "A").ItemID != "x" || e.a.View().Config.Render.MpvPath != path {
				t.Fatal("runtime correction lost pause, assignment, or configured path")
			}
		})
	}
}

func TestMpvPathChangeRespectsSnapshotAndSafeMode(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "initial load", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
	exhaustMpvRetries(t, e)
	// Signals received while failed must be reevaluated before retrying.
	e.a.Do(func() { _ = e.a.ctl.Set(e.a.now(), visibility.LowBattery, -1, true) })
	n := len(e.renderers("A"))
	e.a.UpdateConfig(func(c *config.Config) { c.Render.MpvPath = "/new/mpv" })
	eventually(t, "failed state cleared under snapshot policy", func() bool { return byID(e.a.View(), "A").State == "active" })
	if len(e.renderers("A")) != n || byID(e.a.View(), "A").Level != "L4" {
		t.Fatal("path correction must not start playback under L4")
	}
	e.a.Do(func() { _ = e.a.ctl.Set(e.a.now(), visibility.LowBattery, -1, false) })
	eventually(t, "normal policy resume uses the corrected path", func() bool { return len(e.renderers("A")) > n && e.cur("A").isLoaded() })

	safe := setupWith(t, 1, true, func(d *Deps) { d.SafeMode = true })
	eventually(t, "safe mode ready", func() bool { return safe.a.View().SafeMode })
	safe.a.UpdateConfig(func(c *config.Config) { c.Render.MpvPath = "/new/mpv" })
	safe.sync()
	if len(safe.renderers("A")) != 0 || !safe.a.View().SafeMode {
		t.Fatal("path correction must not exit safe mode")
	}
}
