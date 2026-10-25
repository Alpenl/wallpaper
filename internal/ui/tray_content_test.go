package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"wpapp/internal/config"
)

func TestTrayQuickSwitchUsesLiveLibrary(t *testing.T) {
	sh, c := trayFixture()
	c.v.Library[0].Title = "First"
	c.v.Library[1].Title = "Second"
	item := sh.wallpaperMenu(c.v).Submenu[1] // First row describes the all-display scope.
	item.Click(nil, nil)
	if c.assignedID != "one" || c.assignCalls != 1 {
		t.Fatal("quick switch did not apply the selected ID")
	}
	c.v.Library = c.v.Library[1:]
	item.Click(nil, nil)
	if c.assignCalls != 1 {
		t.Fatal("stale menu applied a deleted wallpaper")
	}
	c.v.Library = append(c.v.Library, config.Item{ID: "one"})
	c.v.SafeMode = true
	item.Click(nil, nil)
	if c.assignCalls != 1 {
		t.Fatal("stale menu escaped safe mode")
	}
}

func TestTrayRecentBoundAndSelection(t *testing.T) {
	_, c := trayFixture()
	c.v.Library = nil
	for i := 0; i < 12; i++ {
		c.v.Library = append(c.v.Library, config.Item{ID: fmt.Sprint(i), Title: "Title\n" + strings.Repeat("界", 45), AddedAt: time.Unix(int64(i), 0)})
	}
	c.v.Displays[0].ItemID, c.v.Displays[1].ItemID = "11", "10"
	items := trayRecent(c.v)
	if len(items) != 8 || items[0].ID != "11" || items[0].Checked || strings.Contains(items[0].Title, "\n") || len([]rune(items[0].Title)) > 32 {
		t.Fatal("recent list is unbounded, unsorted, or misrepresents mixed assignments")
	}
	c.v.Displays[1].ItemID = "11"
	if !trayRecent(c.v)[0].Checked {
		t.Fatal("common wallpaper is not marked")
	}
}

func TestTrayStatusAndPolicyRefresh(t *testing.T) {
	sh, c := trayFixture()
	c.v.Displays = c.v.Displays[:1]
	c.v.Library[0].Title = "Current wallpaper"
	sh.updateTrayState(c.v)
	c.v.Displays[0].Level = "L1"
	c.v.Displays[0].Reasons = "Covered|Battery"
	c.v.Config.Pause.BatteryPolicy = "ignore"
	if changed, _ := sh.updateTrayState(c.v); !changed {
		t.Fatal("pause status did not refresh")
	}
	_, status := traySummary(c.v)
	if status != "已暂停 · 被窗口遮挡" {
		t.Fatalf("wrong pause explanation: %s", status)
	}
	c.v.Displays[0].Level = "L3"
	c.v.Displays[0].Dropped++
	if changed, _ := sh.updateTrayState(c.v); changed {
		t.Fatal("resource release or frame metrics rebuilt an unchanged menu")
	}
	c.v.Library[0].Title = "Renamed"
	if changed, _ := sh.updateTrayState(c.v); !changed {
		t.Fatal("wallpaper title did not refresh")
	}
	policy := trayItem(sh.menu(c.v).Items(), "自动暂停").Submenu[0]
	c.v.Config.Pause.OnCovered = false // Change after the menu was opened.
	before := c.v.Config.Pause
	policy.Click(nil, nil)
	if !c.v.Config.Pause.OnCovered || c.v.Config.Pause.OnFullscreen != before.OnFullscreen || c.v.Config.Pause.BatteryPolicy != before.BatteryPolicy {
		t.Fatal("policy toggle used a stale value or changed unrelated preferences")
	}
	if changed, _ := sh.updateTrayState(c.v); changed { // It was true in the saved menu too.
		t.Fatal("same visible policy triggered refresh")
	}
}

func TestTrayEmptyAndEnglishActions(t *testing.T) {
	sh, c := trayFixture()
	c.v.Library = nil
	c.v.Config.UI.Lang = "en"
	items := sh.menu(c.v).Items()
	if items[0].Label != "No wallpaper assigned" || trayItem(items, "Pause all wallpapers") != nil {
		t.Fatal("empty state shows ineffective playback actions")
	}
	for _, label := range []string{"Import wallpaper…", "Display settings…", "Auto-pause", "Version & diagnostics"} {
		if trayItem(items, label) == nil {
			t.Fatalf("missing English action %q", label)
		}
	}
	for i := 1; i < len(items); i++ {
		if items[i].Type == mygo.MenuItemSeparator && items[i-1].Type == mygo.MenuItemSeparator {
			t.Fatal("adjacent separators in empty menu")
		}
	}
	c.v.PausedAll = true
	if trayItem(sh.menu(c.v).Items(), "Pause all wallpapers") == nil {
		t.Fatal("empty library cannot clear persisted manual pause")
	}
}
