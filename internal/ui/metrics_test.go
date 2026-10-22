package ui

import (
	"testing"
	"time"

	"wpapp/internal/app"
)

func TestMetricDurationFormatting(t *testing.T) {
	for _, tc := range []struct {
		lang string
		d    time.Duration
		want string
	}{
		{"zh-CN", 0, "0 分钟"},
		{"zh-CN", 15 * time.Second, "不足 1 分钟"},
		{"zh-CN", 61 * time.Minute, "1 小时 1 分钟"},
		{"en", 0, "0 min"},
		{"en", 15 * time.Second, "Less than 1 min"},
		{"en", 125 * time.Minute, "2 h 5 min"},
	} {
		if got := metricDuration(tc.lang, tc.d); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestNativeMetricsRenderActualViewAndUnverifiedHardware(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1200, 1800)
	ctl.V.Displays[0].Metrics = app.DisplayMetricsView{WeekStart: "2026-10-19", Paused: 95 * time.Minute, Levels: [5]time.Duration{2 * time.Hour, 35 * time.Minute, 0, 30 * time.Minute, 30 * time.Minute}}
	ctl.V.Displays[0].HWDec = "unknown"
	ctl.V.Displays[0].Dropped = 17
	s.SetPage("displays")
	tt.Frame()
	must(t, tt, "本周暂停时长", "1 小时 35 分钟", "硬解状态未验证", "当前渲染会话丢帧 17", "统计周起始：2026-10-19（UTC，周一）")
	if tt.HasText("本周省电") {
		t.Fatal("duration presented as energy savings")
	}
	s.SetPage("about")
	tt.Frame()
	must(t, tt, "本机统计", "本周暂停时长", "1 小时 35 分钟", "L3 · 30 分钟", "L4 · 30 分钟")
	ctl.V.Displays[0].Metrics.Paused = 96 * time.Minute
	ctl.V.Displays[0].Metrics.ClockAdjusted = true
	tt.Frame()
	must(t, tt, "1 小时 36 分钟", "检测到系统时钟调整，已跳过不可靠的计时间隔；历史记录保留。")
	tt.SetLocale("en")
	tt.Frame()
	must(t, tt, "Local metrics", "Paused this week", "1 h 36 min", "Hardware decoding unverified")
}

func TestNativeMetricsEmptyViewIsNotFabricated(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1200, 1800)
	ctl.V.Displays[0].Metrics = app.DisplayMetricsView{}
	s.SetPage("displays")
	tt.Frame()
	must(t, tt, "尚无运行记录", "0 分钟")
	ctl.V.Displays = nil
	s.SetPage("about")
	tt.Frame()
	must(t, tt, "本机统计", "没有检测到显示器。")
}
