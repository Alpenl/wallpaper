package ui

import (
	"testing"
	"wpapp/internal/config"
)

func TestMacEncoderSettingsRemainOptionalAndPersistTarget(t *testing.T) {
	ctl, s, tt := newUI(t, "darwin")
	ctl.V.Encoder.Strict = false // Exercise the limited compatibility fallback warning.
	tt.SetSize(1120, 2200)
	s.SetPage("playback")
	tt.Frame()
	must(t, tt, "ffmpeg 路径", "目标分辨率", "主屏物理分辨率", "兼容转换不保证固定帧率、去音轨或 GIF；需要这些能力时可配置 ffmpeg")
	click(t, tt, "主屏物理分辨率")
	click(t, tt, "2560 × 1440")
	if got := ctl.View().Config.Render.TranscodeResolution; got != "1440p" {
		t.Fatal(got)
	}
	s.ffmpeg = "/chosen/ffmpeg"
	tt.Frame()
	click(t, tt, "保存")
	if got := ctl.View().Config.Render.FFmpegPath; got != "/chosen/ffmpeg" {
		t.Fatal(got)
	}
	ctl.UpdateConfig(func(c *config.Config) { c.Render.FFmpegPath = "" })
}
