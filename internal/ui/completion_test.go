package ui

import (
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/config"
)

func TestImportCancellationAndPickerFeedback(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		t.Run(lang, func(t *testing.T) {
			ctl, s, tt := newUI(t, "windows")
			tt.SetLocale(lang)
			s.PickFiles = func() []string { return nil }
			tt.Frame()
			click(t, tt, translate(lang, "导入壁纸"))
			must(t, tt, translate(lang, "已取消文件选择"))
			if len(ctl.Imported) != 0 {
				t.Fatal("picker cancellation imported files")
			}
			s.PickFiles = func() []string { s.pickerStatus = "文件选择失败：permission denied"; return nil }
			click(t, tt, translate(lang, "导入壁纸"))
			must(t, tt, translate(lang, "文件选择失败：permission denied"))
			s.pickerStatus = ""
			ctl.V.Importing, ctl.V.ImportQueued = true, 2
			tt.Frame()
			click(t, tt, translate(lang, "取消导入"))
			if ctl.View().Importing || ctl.View().ImportQueued != 0 {
				t.Fatal("import cancellation not forwarded")
			}
			must(t, tt, translate(lang, "导入已取消；已完成的项目保留在壁纸库"))
		})
	}
}

func TestPauseReasonsAndInheritedAssignment(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1700)
	ctl.Assign("d1", "")
	ctl.V.Displays[0].Reasons = "Fullscreen|Battery"
	s.SetPage("rules")
	tt.Frame()
	must(t, tt, "当前暂停原因", "其他程序全屏、使用电池")
	s.SetPage("displays")
	tt.Frame()
	must(t, tt, "（跟随默认壁纸）")
	click(t, tt, "（跟随默认壁纸）")
	click(t, tt, "海浪 4K")
	if !ctl.View().Displays[0].Custom {
		t.Fatal("explicit assignment not saved")
	}
}

func TestUpdateActionsSerializeAndKeepCompletedStatus(t *testing.T) {
	for _, operation := range []string{"apply", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			_, s, tt := newUI(t, "windows")
			var calls atomic.Int32
			gate := make(chan struct{})
			defer close(gate)
			hook := func() error { calls.Add(1); <-gate; return nil }
			s.ApplyUpdate = func(func(int64, int64)) error { return hook() }
			s.Rollback = hook
			start := s.applyUpdate
			if operation == "rollback" {
				start = s.rollback
			}
			start()
			for range 20 {
				start()
				s.checkUpdate()
			}
			deadline := time.Now().Add(time.Second)
			for calls.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if calls.Load() != 1 {
				t.Fatal("duplicate update operations", calls.Load())
			}
			gate <- struct{}{}
			for time.Now().Before(deadline) {
				s.mu.Lock()
				done := s.upd.installed
				s.mu.Unlock()
				if done {
					break
				}
				time.Sleep(time.Millisecond)
			}
			s.mu.Lock()
			message, done := s.upd.msg, s.upd.installed
			s.mu.Unlock()
			if !done {
				t.Fatal("update never completed")
			}
			start()
			s.SetPage("versions")
			tt.Frame()
			must(t, tt, message)
			if calls.Load() != 1 {
				t.Fatal("completed operation repeated")
			}
		})
	}
}

func TestResetCancelKeepsSettings(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	ctl.UpdateConfig(func(c *config.Config) { c.Perf.Profile = "saver" })
	s.SetPage("about")
	tt.SetSize(1120, 3000)
	tt.Frame()
	click(t, tt, "恢复默认")
	click(t, tt, "取消")
	if ctl.View().Config.Perf.Profile != "saver" {
		t.Fatal("cancel reset changed configuration")
	}
}
