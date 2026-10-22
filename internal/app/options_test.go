package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func TestPerDisplayOptionsPauseCapAndReplay(t *testing.T) {
	e := setup(t, 2, true)
	eventually(t, "both started", func() bool { return e.cur("A") != nil && e.cur("B") != nil && e.cur("B").isLoaded() })
	opts := config.DefaultDisplayOptions()
	opts.Paused = true
	opts.Profile = "saver"
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "only A paused", func() bool { return e.cur("A").last() == visibility.L1 && byID(e.a.View(), "A").Options.Paused })
	if e.cur("B").last() != visibility.L0 {
		t.Fatal("B paused")
	}
	e.a.SetPausedAll(true)
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.ResumeDebounceMs = 50 })
	eventually(t, "global pause survives rebuild", func() bool { return e.cur("B").last() == visibility.L1 })
	e.a.SetPausedAll(false)
	eventually(t, "B resumed", func() bool { return e.cur("B").last() == visibility.L0 })
	if e.cur("A").last() != visibility.L1 {
		t.Fatal("global resume cleared per-display pause")
	}
	opts.Paused = false
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "A saver cap", func() bool { return e.cur("A").last() == visibility.L0 && e.cur("A").lastCap() == 15 })
	e.a.UpdateConfig(func(c *config.Config) { c.Perf.FPSCap = 10 })
	eventually(t, "global limit respected", func() bool { return e.cur("A").lastCap() == 10 && e.cur("B").lastCap() == 10 })
}

func TestForegroundRulesPauseSaverIgnoreAndSafety(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "started", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
	e.a.UpdateConfig(func(c *config.Config) {
		c.Pause.ResumeDebounceMs = 10
		c.Pause.AppRules = []config.AppRule{{Executable: "pause-app", Action: "pause"}, {Executable: "saver-app", Action: "saver"}, {Executable: "ignore-app", Action: "ignore"}}
	})
	e.h.ev <- desktop.Event{Kind: desktop.EventForegroundApp, Executable: "pause-app"}
	eventually(t, "rule pauses", func() bool { return e.cur("A").last() == visibility.L1 })
	e.h.ev <- desktop.Event{Kind: desktop.EventForegroundApp, Executable: "saver-app"}
	eventually(t, "rule caps", func() bool { return e.cur("A").last() == visibility.L0 && e.cur("A").lastCap() == 15 })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "A", Reason: visibility.Fullscreen, Active: true}
	eventually(t, "fullscreen paused", func() bool { return e.cur("A").last() == visibility.L1 })
	e.h.ev <- desktop.Event{Kind: desktop.EventForegroundApp, Executable: "ignore-app"}
	eventually(t, "ignore bypasses fullscreen", func() bool { return e.cur("A").last() == visibility.L0 && e.cur("A").lastCap() == 30 })
	e.a.SetPausedAll(true)
	eventually(t, "ignore cannot bypass manual pause", func() bool { return e.cur("A").last() == visibility.L1 })
	e.a.SetPausedAll(false)
	e.h.ev <- desktop.Event{Kind: desktop.EventForegroundApp, Executable: ""}
	eventually(t, "foreground cleared restores raw fullscreen", func() bool { return strings.Contains(byID(e.a.View(), "A").Reasons, "Fullscreen") })
}

func TestPauseToggleReplaysRawSignal(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "started", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
	e.h.ev <- desktop.Event{Kind: desktop.EventSignal, Display: "A", Reason: visibility.Fullscreen, Active: true}
	eventually(t, "paused", func() bool { return e.cur("A").last() == visibility.L1 })
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.OnFullscreen = false })
	eventually(t, "disabled resumes", func() bool { return e.cur("A").last() == visibility.L0 })
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.OnFullscreen = true })
	eventually(t, "enabled replays signal", func() bool { return e.cur("A").last() == visibility.L1 })
}

func TestFolderImportPartialFailureAppliesFirstOnce(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		os.WriteFile(filepath.Join(root, n), nil, 0600)
	}
	e := setupWith(t, 2, false, func(d *Deps) {
		d.Import = func(ctx context.Context, src string) (*importer.Result, error) {
			name := filepath.Base(src)
			if name == "b.mp4" {
				return nil, os.ErrPermission
			}
			return &importer.Result{Item: config.Item{ID: name, Title: name, Type: "video", Dir: root, Entry: name}}, nil
		}
	})
	e.a.Import(root, "A")
	eventually(t, "batch complete", func() bool { v := e.a.View(); return len(v.Library) == 2 && !v.Importing })
	v := e.a.View()
	if byID(v, "A").ItemID != "a.mp4" || byID(v, "B").ItemID != "" || !strings.Contains(v.LastImport, "失败 1") {
		t.Fatal(v)
	}
}

type optionRenderer struct {
	*fakeR
	lock    sync.Mutex
	options render.PlaybackOptions
	start   float64
	pos     float64
}

func (r *optionRenderer) Configure(o render.PlaybackOptions) error {
	r.lock.Lock()
	r.options = o
	r.lock.Unlock()
	return nil
}
func (r *optionRenderer) Capabilities() render.Capabilities {
	return render.Capabilities{Fit: true, Speed: true, Audio: true, Scale: true, Resume: true, FPS: true}
}
func (r *optionRenderer) Load(it render.Item, s desktop.Surface, p bool) error {
	r.lock.Lock()
	r.start = it.Start
	r.lock.Unlock()
	return r.fakeR.Load(it, s, p)
}
func (r *optionRenderer) Stats() render.Stats {
	r.lock.Lock()
	defer r.lock.Unlock()
	return render.Stats{Backend: "configured", Pos: r.pos}
}

func TestPlaybackConfigAndResumePosition(t *testing.T) {
	var mu sync.Mutex
	var rs []*optionRenderer
	e := setupWith(t, 1, true, func(d *Deps) {
		d.Store.State.Positions["A|x"] = 12
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			r := &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}, pos: 23}
			mu.Lock()
			rs = append(rs, r)
			mu.Unlock()
			return r
		}
	})
	current := func() *optionRenderer {
		mu.Lock()
		defer mu.Unlock()
		if len(rs) == 0 {
			return nil
		}
		return rs[len(rs)-1]
	}
	eventually(t, "started", func() bool { return current() != nil && current().isLoaded() })
	first := current()
	first.lock.Lock()
	start := first.start
	first.lock.Unlock()
	if start != 12 {
		t.Fatal(start)
	}
	opts := config.DefaultDisplayOptions()
	opts.Fit = "fit"
	opts.Speed = 2
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "reconfigured", func() bool { return current() != first && current().isLoaded() })
	current().lock.Lock()
	got, at := current().options, current().start
	current().lock.Unlock()
	if got.Fit != "fit" || got.Speed != 2 || !got.Muted || at != 23 {
		t.Fatal(got, at)
	}
	last := current()
	e.a.UpdateConfig(func(c *config.Config) {
		c.Render.ResumePosition = false
		c.Render.Muted = false
		c.Perf.RenderScale = .5
	})
	eventually(t, "resume disabled", func() bool { return current() != last && current().isLoaded() })
	current().lock.Lock()
	got, at = current().options, current().start
	current().lock.Unlock()
	if got.Muted || got.Scale != .5 || at != 0 {
		t.Fatal(got, at)
	}
}

func TestViewIsDetached(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "view", func() bool { return len(e.a.View().Library) > 0 })
	e.a.UpdateConfig(func(c *config.Config) { c.Pause.AppRules = []config.AppRule{{Executable: "x", Action: "pause"}} })
	e.sync()
	v := e.a.View()
	v.Config.Pause.AppRules[0].Action = "ignore"
	v.Library[0].Title = "changed"
	v.Transcoding["x"] = "fake"
	v2 := e.a.View()
	if v2.Config.Pause.AppRules[0].Action != "pause" || v2.Library[0].Title == "changed" || v2.Transcoding["x"] != "" {
		t.Fatal(v2)
	}
}

func TestAutoTranscodeAndLiveEncoderSelection(t *testing.T) {
	e := setupWith(t, 0, false, func(d *Deps) {
		root := d.Store.Paths.Library
		d.Store.Config.Render.AutoTranscode = true
		d.Import = func(context.Context, string) (*importer.Result, error) {
			dir := filepath.Join(root, "auto")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, "source.webm"), nil, 0600)
			return &importer.Result{Item: config.Item{ID: "auto", Dir: dir, Title: "Auto", Type: "video", Entry: "source.webm", Transcode: "recommended"}}, nil
		}
		d.ResolveEncoder = func(c config.Config) (func(context.Context, string, string) error, string) {
			if c.Render.FFmpegPath != "available" {
				return nil, ""
			}
			return func(_ context.Context, _, dst string) error { return os.WriteFile(dst, []byte("encoded"), 0600) }, ".mp4"
		}
	})
	e.a.UpdateConfig(func(c *config.Config) { c.Render.FFmpegPath = "available" })
	eventually(t, "encoder detected", func() bool { return e.a.View().HasEncoder })
	e.a.Import("source.webm", "")
	eventually(t, "auto transcoded", func() bool {
		v := e.a.View()
		return len(v.Library) == 1 && v.Library[0].Transcode == "done" && v.Library[0].SizeBytes == 7
	})
	e.a.UpdateConfig(func(c *config.Config) { c.Render.FFmpegPath = "missing" })
	eventually(t, "encoder disabled", func() bool { return !e.a.View().HasEncoder })
}

type liveRenderer struct {
	*optionRenderer
	updates int
	fail    error
}

func (r *liveRenderer) UpdatePlayback(o render.PlaybackOptions) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.updates++
	if r.fail != nil {
		return r.fail
	}
	r.options = o
	return nil
}
func TestLiveDisplayOptionsDoNotReloadOrResume(t *testing.T) {
	var mu sync.Mutex
	var rs []*liveRenderer
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			r := &liveRenderer{optionRenderer: &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}, pos: 17}}
			mu.Lock()
			rs = append(rs, r)
			mu.Unlock()
			return r
		}
	})
	current := func() *liveRenderer {
		mu.Lock()
		defer mu.Unlock()
		if len(rs) == 0 {
			return nil
		}
		return rs[len(rs)-1]
	}
	eventually(t, "started", func() bool { return current() != nil && current().isLoaded() })
	first := current()
	opts := config.DefaultDisplayOptions()
	opts.Paused = true
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "paused", func() bool { return first.last() == visibility.L1 })
	opts.Fit = "fit"
	opts.Speed = 2
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "live update", func() bool {
		first.lock.Lock()
		defer first.lock.Unlock()
		return first.updates == 1 && first.options.Fit == "fit" && first.options.Speed == 2
	})
	if current() != first || first.last() != visibility.L1 {
		t.Fatal("live adjustment reloaded or resumed")
	}
	first.lock.Lock()
	first.fail = render.ErrUnsupported
	first.lock.Unlock()
	opts.Speed = 3
	e.a.SetDisplayOptions("A", opts)
	eventually(t, "unsupported fallback", func() bool { return current() != first && current().isLoaded() })
}

// Wallpaper-group props travel with the item; a display override wins only for
// the property it sets, and clearing it falls back to the group's value.
func TestItemPropsApplyAndDisplayOverridesWin(t *testing.T) {
	var mu sync.Mutex
	var rs []*liveRenderer
	e := setupWith(t, 1, true, func(d *Deps) {
		d.NewRenderer = func(_ string, _ config.Config, id desktop.DisplayID) render.Renderer {
			r := &liveRenderer{optionRenderer: &optionRenderer{fakeR: &fakeR{display: id, done: make(chan error, 1)}}}
			mu.Lock()
			rs = append(rs, r)
			mu.Unlock()
			return r
		}
	})
	current := func() *liveRenderer {
		mu.Lock()
		defer mu.Unlock()
		if len(rs) == 0 {
			return nil
		}
		return rs[len(rs)-1]
	}
	options := func() render.PlaybackOptions {
		r := current()
		r.lock.Lock()
		defer r.lock.Unlock()
		return r.options
	}
	// Load is acknowledged on the worker before the app publishes its view.
	// Wait for the item identity as well; an empty ID makes SetItemProps a no-op.
	eventually(t, "started and item published", func() bool {
		return current() != nil && current().isLoaded() && byID(e.a.View(), "A").ItemID != ""
	})
	item := byID(e.a.View(), "A").ItemID
	e.a.SetItemProps(item, config.ItemProps{Fit: "stretch", Speed: 1.5, Volume: 40, Audio: "sound"})
	eventually(t, "group props applied", func() bool {
		o := options()
		return o.Fit == "stretch" && o.Speed == 1.5 && o.Volume == 40 && !o.Muted
	})
	if p := e.a.View().Library[0].Props; p.Volume != 40 || p.Audio != "sound" {
		t.Fatal("props not stored on the item", p)
	}
	e.a.SetDisplayOptions("A", config.DisplayOptions{Fit: "fit", Audio: "mute"})
	eventually(t, "override wins", func() bool {
		o := options()
		return o.Fit == "fit" && o.Speed == 1.5 && o.Muted
	})
	if eff := byID(e.a.View(), "A").Effective; eff.Fit != "fit" || eff.Speed != 1.5 || !eff.Muted || eff.Volume != 40 {
		t.Fatal("view effective playback", eff)
	}
	e.a.SetDisplayOptions("A", config.DisplayOptions{})
	eventually(t, "cleared override follows group", func() bool {
		o := options()
		return o.Fit == "stretch" && !o.Muted
	})
}

func TestAppearanceChangeDoesNotRebuildPauseMachines(t *testing.T) {
	e := setup(t, 1, true)
	eventually(t, "started", func() bool { return e.cur("A") != nil && e.cur("A").isLoaded() })
	before := make(chan *visibility.Controller)
	e.a.Do(func() { before <- e.a.ctl })
	old := <-before
	e.a.UpdateConfig(func(c *config.Config) { c.UI.Theme = "dark" })
	eventually(t, "theme applied", func() bool { return e.a.View().Config.UI.Theme == "dark" })
	after := make(chan *visibility.Controller)
	e.a.Do(func() { after <- e.a.ctl })
	if <-after != old {
		t.Fatal("unrelated change reset pause state")
	}
}
