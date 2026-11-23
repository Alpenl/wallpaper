package ui

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

type trayController struct {
	Controller
	mu             sync.Mutex
	v              app.View
	optionsCalls   int
	nextCalls      int
	assignedID     string
	assignCalls    int
	networkAnswers []bool
	signalAnswers  []bool
}

func (c *trayController) View() app.View {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.v
	v.Displays = append([]app.DisplayView(nil), c.v.Displays...)
	v.Library = append([]config.Item(nil), c.v.Library...)
	return v
}
func (c *trayController) SetPausedAll(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v.PausedAll = v
}
func (c *trayController) ExitSafeMode() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v.SafeMode = false
}
func (c *trayController) AssignAll(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assignedID = id
	c.assignCalls++
}
func (c *trayController) Next() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextCalls++
}
func (c *trayController) UpdateConfig(fn func(*config.Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.v.Config)
}
func (c *trayController) ResolveNetworkConsent(allow bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.networkAnswers = append(c.networkAnswers, allow)
	c.v.NetworkConsent = nil
}
func (c *trayController) ResolveSignalConsent(allow bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.signalAnswers = append(c.signalAnswers, allow)
	c.v.SignalConsent = nil
}
func (c *trayController) SetDisplayOptions(id string, options config.DisplayOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.optionsCalls++
	for i := range c.v.Displays {
		if c.v.Displays[i].ID == id {
			c.v.Displays[i].Options = options
		}
	}
}

func trayFixture() (*Shell, *trayController) {
	c := &trayController{v: app.View{
		Config:  config.Defaults(),
		Library: []config.Item{{ID: "one"}, {ID: "two"}},
		Displays: []app.DisplayView{
			{ID: "a", ItemID: "one", Level: "L0", State: "active", Index: 1, Name: "工作屏", Alias: "工作屏"},
			{ID: "b", ItemID: "two", Level: "L1", State: "active", Index: 2, Name: "副屏", Friendly: "副屏", Options: config.DisplayOptions{Paused: true}},
		},
	}}
	return &Shell{S: NewSettings(c, "windows")}, c
}

func trayItem(items []*mygo.MenuItem, label string) *mygo.MenuItem {
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	return nil
}

func TestTrayMenu(t *testing.T) {
	for _, tc := range []struct {
		name              string
		paused, safe      bool
		displays, library int
		layout            string
	}{
		{"normal", false, false, 2, 2, "independent"},
		{"paused at startup", true, false, 2, 2, "independent"},
		{"safe mode", true, true, 2, 2, "span"},
		{"single display", false, false, 1, 2, "independent"},
		{"span", false, false, 2, 2, "span"},
		{"one wallpaper", false, false, 2, 1, "independent"},
		{"empty library", false, false, 2, 0, "independent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh, c := trayFixture()
			c.v.PausedAll, c.v.SafeMode, c.v.Layout = tc.paused, tc.safe, tc.layout
			c.v.Displays, c.v.Library = c.v.Displays[:tc.displays], c.v.Library[:tc.library]
			sh.S.Refresh()
			v := sh.S.view0()
			items := sh.menu(v).Items()
			sh.updateTrayState(v) // Start initializes comparison from this same snapshot.
			if menu, tip := sh.updateTrayState(v); menu || tip {
				t.Fatal("unchanged startup snapshot refreshed")
			}
			for _, label := range []string{"打开壁纸库", "性能档位", "设置…", "退出"} {
				if trayItem(items, label) == nil {
					t.Fatalf("missing %s", label)
				}
			}
			pause, next, displays := trayItem(items, "暂停全部壁纸"), trayItem(items, "切换到下一张壁纸"), trayItem(items, "显示器")
			if tc.safe {
				if pause != nil || next != nil || displays != nil {
					t.Fatal("safe mode exposes playback controls")
				}
				if items[0].Label != "安全模式：壁纸未加载" || !items[0].Disabled || items[1].Type != mygo.MenuItemSeparator {
					t.Fatal("missing safe mode heading")
				}
				trayItem(items, "退出安全模式").Click(nil, nil)
				if c.v.SafeMode || !c.v.PausedAll || !c.v.Displays[1].Options.Paused {
					t.Fatal("safe mode exit changed manual pauses")
				}
				return
			}
			if tc.library == 0 && !tc.paused {
				if pause != nil || next != nil {
					t.Fatal("empty library exposes ineffective playback controls")
				}
				return
			}
			if pause.Type != mygo.MenuItemCheckbox || pause.Checked != tc.paused {
				t.Fatal("wrong global pause")
			}
			if next.Disabled != (tc.library < 2) {
				t.Fatal("wrong next availability")
			}
			if (displays != nil) != (tc.displays >= 2) {
				t.Fatal("wrong display submenu")
			}
			if displays != nil {
				for i, item := range displays.Submenu {
					if item.Type != mygo.MenuItemCheckbox || item.Checked != c.v.Displays[i].Options.Paused {
						t.Fatal("wrong display check")
					}
				}
			}
			pause.Click(nil, nil)
			if c.v.PausedAll == tc.paused {
				t.Fatal("pause callback failed")
			}
			if tc.displays >= 2 && !c.v.Displays[1].Options.Paused {
				t.Fatal("global resume cleared display pause")
			}
			trayItem(items, "性能档位").Submenu[0].Click(nil, nil)
			if c.v.Config.Perf.Profile != "smooth" {
				t.Fatal("profile callback failed")
			}
			if !next.Disabled {
				next.Click(nil, nil)
				if c.nextCalls != 1 {
					t.Fatal("next callback failed")
				}
			}
		})
	}
}

func TestTrayDisplayCallbackUsesLatestOptions(t *testing.T) {
	sh, c := trayFixture()
	item := trayItem(sh.menu(c.v).Items(), "显示器").Submenu[0]
	want := config.DisplayOptions{Fit: "fit", Speed: 1.5, Audio: "sound", Profile: "saver", Paused: true}
	c.v.Displays[0].Options = want
	c.v.Displays[0], c.v.Displays[1] = c.v.Displays[1], c.v.Displays[0]
	item.Click(nil, nil)
	want.Paused = false
	if c.v.Displays[1].Options != want || c.optionsCalls != 1 {
		t.Fatal("callback did not preserve latest options by ID")
	}
	c.v.Displays = c.v.Displays[:1]
	item.Click(nil, nil)
	if c.optionsCalls != 1 {
		t.Fatal("disconnected display was modified")
	}
}

func TestTrayRefreshState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*app.View)
		rebuild bool
	}{
		{"alias", func(v *app.View) { v.Displays[0].Name = "新别名" }, true},
		{"fallback label", func(v *app.View) { v.Displays[0].Alias = "" }, true},
		{"order", func(v *app.View) { v.Displays[0], v.Displays[1] = v.Displays[1], v.Displays[0] }, true},
		{"index", func(v *app.View) { v.Displays[0].Index = 3 }, true},
		{"disconnect", func(v *app.View) { v.Displays = v.Displays[:1] }, true},
		{"connect", func(v *app.View) { v.Displays = append(v.Displays, app.DisplayView{ID: "c"}) }, true},
		{"replacement", func(v *app.View) { v.Displays[0].ID = "c" }, true},
		{"display pause", func(v *app.View) { v.Displays[0].Options.Paused = true }, true},
		{"global pause", func(v *app.View) { v.PausedAll = true }, true},
		{"safe mode", func(v *app.View) { v.SafeMode = true }, true},
		{"profile", func(v *app.View) { v.Config.Perf.Profile = "smooth" }, true},
		{"language", func(v *app.View) { v.Config.UI.Lang = "en" }, true},
		{"few items", func(v *app.View) { v.Library = nil }, true},
		{"FPS", func(v *app.View) { v.Displays[0].FPSCap = 15 }, false},
		{"reasons", func(v *app.View) { v.Displays[0].Reasons = "User" }, false},
		{"layout", func(v *app.View) { v.Layout = "span" }, false},
		{"more items", func(v *app.View) { v.Library = append(v.Library, config.Item{ID: "three"}) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh, c := trayFixture()
			sh.updateTrayState(c.v)
			tc.change(&c.v)
			rebuild, _ := sh.updateTrayState(c.v)
			if rebuild != tc.rebuild {
				t.Fatalf("rebuild = %v", rebuild)
			}
			if rebuild, tip := sh.updateTrayState(c.v); rebuild || tip {
				t.Fatal("repeated refresh")
			}
		})
	}
	sh, c := trayFixture()
	sh.updateTrayState(c.v)
	sh.lastToolTip = "old"
	if menu, tip := sh.updateTrayState(c.v); menu || !tip {
		t.Fatal("tooltip-only update rebuilt menu")
	}
}

func TestTrayTranslationsAndToolTip(t *testing.T) {
	sh, c := trayFixture()
	for _, lang := range []string{"zh-CN", "en"} {
		c.v.Config.UI.Lang = lang
		for _, tc := range []struct {
			safe, paused bool
			label        string
		}{
			{false, false, "动态壁纸"}, {false, true, "动态壁纸 · 已手动暂停全部"},
			{true, false, "动态壁纸 · 安全模式"}, {true, true, "动态壁纸 · 安全模式"},
		} {
			c.v.SafeMode, c.v.PausedAll = tc.safe, tc.paused
			if got := trayToolTip(c.v); got != translate(lang, tc.label) {
				t.Fatalf("tooltip = %q", got)
			}
		}
	}
	c.v.SafeMode = false
	c.v.Displays[0].Name = "设置"
	items := sh.menu(c.v).Items()
	if got := trayItem(items, "Displays").Submenu[0].Label; got != "Pause Display 1 · 设置" {
		t.Fatalf("translated user name: %q", got)
	}
	c.v.SafeMode = true
	items = sh.menu(c.v).Items()
	if items[0].Label != "Safe mode: wallpapers not loaded" || trayItem(items, "Exit safe mode") == nil {
		t.Fatal("safe mode translation missing")
	}
}

func TestTrayDisplayLabels(t *testing.T) {
	for _, tc := range []struct {
		name, alias, friendly, zh, en string
	}{
		{"fallback", "", "", "暂停 显示器 1", "Pause Display 1"},
		{"alias", "显示器 1", "", "暂停 显示器 1 · 显示器 1", "Pause Display 1 · 显示器 1"},
		{"friendly", "", "显示器 1", "暂停 显示器 1 · 显示器 1", "Pause Display 1 · 显示器 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh, c := trayFixture()
			d := &c.v.Displays[0]
			d.Name, d.Alias, d.Friendly = "显示器 1", tc.alias, tc.friendly
			for _, lang := range []string{"zh-CN", "en"} {
				c.v.Config.UI.Lang = lang
				want := tc.zh
				if lang == "en" {
					want = tc.en
				}
				item := trayItem(sh.menu(c.v).Items(), translate(lang, "显示器")).Submenu[0]
				if item.Label != want {
					t.Fatalf("%s label = %q, want %q", lang, item.Label, want)
				}
			}
		})
	}
}

func TestTrayChangedWithoutTray(t *testing.T) {
	sh, _ := trayFixture()
	sh.Changed()
	if !sh.lastMenu.equal(trayMenuState{}) || sh.lastToolTip != "" {
		t.Fatal("Changed initialized comparison state before tray creation")
	}
}

func TestTrayChangedIgnoresMetrics(t *testing.T) {
	sh, c := trayFixture()
	sh.tray = &mygo.Tray{}
	sh.updateTrayState(c.v)
	c.v.Displays[0].FPSCap = 15
	c.v.Displays[0].Reasons = "User"
	sh.Changed() // No native tray or main loop is needed for unrelated changes.
	if !sh.lastMenu.equal(trayState(c.v)) {
		t.Fatal("menu state changed")
	}
}

func TestShellChangedOpensWindowForNetworkAndSignalConsent(t *testing.T) {
	for _, kind := range []string{"network", "signal"} {
		t.Run(kind, func(t *testing.T) {
			sh, c := trayFixture()
			var mainCalls, openCalls int
			sh.runOnMain = func(fn func()) { mainCalls++; fn() }
			sh.openWin = func() { openCalls++ }
			if kind == "network" {
				c.v.NetworkConsent = &app.NetworkConsent{Title: "Web", Hosts: []string{"example.com"}}
			} else {
				c.v.SignalConsent = &app.SignalConsent{Title: "Audio", Permission: "audio"}
			}
			sh.Changed()
			if mainCalls != 1 || openCalls != 1 {
				t.Fatalf("mainCalls=%d openCalls=%d, want 1, 1", mainCalls, openCalls)
			}
		})
	}
}

func TestShellWindowCloseDeniesPendingConsents(t *testing.T) {
	sh, c := trayFixture()
	w := &mygo.Window{}
	sh.win = w
	c.v.NetworkConsent = &app.NetworkConsent{Title: "Web"}
	c.v.SignalConsent = &app.SignalConsent{Title: "Input", Permission: "input"}

	sh.onWindowClosed(w)

	if sh.win != nil {
		t.Fatal("closed window reference was not cleared")
	}
	if len(c.networkAnswers) != 1 || c.networkAnswers[0] {
		t.Fatalf("networkAnswers = %v, want [false]", c.networkAnswers)
	}
	if len(c.signalAnswers) != 1 || c.signalAnswers[0] {
		t.Fatalf("signalAnswers = %v, want [false]", c.signalAnswers)
	}

	// Closing again with no pending consents must not send extra resolutions.
	sh.onWindowClosed(w)
	if len(c.networkAnswers) != 1 || len(c.signalAnswers) != 1 {
		t.Fatalf("unexpected extra consent resolutions: net=%v sig=%v", c.networkAnswers, c.signalAnswers)
	}
}

func TestShellChangedSerializesTrayUpdatesOnMain(t *testing.T) {
	sh, c := trayFixture()
	sh.tray = &mygo.Tray{}
	sh.updateTrayState(c.View())

	// Simulated single-threaded main loop with explicit gating (no fixed sleeps).
	mainCh := make(chan func(), 32)
	posted := make(chan struct{}, 32)
	gate := make(chan struct{})
	doneMain := make(chan struct{})
	var onMainThread atomic.Bool
	go func() {
		defer close(doneMain)
		<-gate
		for fn := range mainCh {
			onMainThread.Store(true)
			fn()
			onMainThread.Store(false)
		}
	}()
	sh.runOnMain = func(fn func()) {
		done := make(chan struct{})
		mainCh <- func() {
			defer close(done)
			fn()
		}
		posted <- struct{}{}
		<-done
	}

	var applied []app.View
	sh.applyTray = func(_ *mygo.Tray, rebuild, tipChanged bool, v app.View) {
		if !onMainThread.Load() {
			t.Error("applyTray ran off the main thread")
		}
		if rebuild || tipChanged {
			applied = append(applied, v)
		}
	}

	// Trigger state transition S0 -> S1 while main is blocked; lastMenu must NOT
	// be updated before the main thread runs.
	c.SetPausedAll(true)
	g1Done := make(chan struct{})
	go func() {
		defer close(g1Done)
		sh.Changed()
	}()
	<-posted
	sh.mu.Lock()
	premature := sh.lastMenu.PausedAll
	sh.mu.Unlock()
	if premature {
		t.Fatal("lastMenu was mutated before main thread executed")
	}

	// Concurrently transition S1 -> S2 (different profile and unpaused) before main runs.
	c.SetPausedAll(false)
	c.UpdateConfig(func(cfg *config.Config) { cfg.Perf.Profile = "saver" })
	g2Done := make(chan struct{})
	go func() {
		defer close(g2Done)
		sh.Changed()
	}()

	close(gate)
	<-g1Done
	<-g2Done

	// Burst concurrent notifications from multiple goroutines under -race.
	var wg sync.WaitGroup
	profiles := []string{"smooth", "balanced", "saver"}
	for i := 0; i < 12; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.UpdateConfig(func(cfg *config.Config) { cfg.Perf.Profile = profiles[i%len(profiles)] })
			sh.Changed()
		}()
	}
	wg.Wait()
	c.UpdateConfig(func(cfg *config.Config) { cfg.Perf.Profile = "smooth" })
	c.SetPausedAll(true)
	sh.Changed()

	close(mainCh)
	<-doneMain

	if len(applied) == 0 {
		t.Fatal("expected at least one tray update on main")
	}
	last := applied[len(applied)-1]
	if last.Config.Perf.Profile != "smooth" || !last.PausedAll {
		t.Fatalf("final applied tray state = (profile=%q, paused=%v), want (smooth, true)", last.Config.Perf.Profile, last.PausedAll)
	}
	if !sh.lastMenu.equal(trayState(c.View())) {
		t.Fatal("shell lastMenu diverged from final controller state")
	}
}
