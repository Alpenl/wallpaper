package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpapp/internal/config"
)

func TestMpvPathEditorValidatesAndSubmits(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 2200)
	s.SetPage("playback")
	tt.Frame()
	must(t, tt, "mpv 路径", "保存 mpv 路径")
	s.mpvPath = filepath.Join(t.TempDir(), "missing-mpv.exe")
	tt.Frame()
	click(t, tt, "保存 mpv 路径")
	if ctl.View().Config.Render.MpvPath != "" || s.mpvPathError == "" {
		t.Fatal("missing executable must not be saved or reported as success")
	}
	must(t, tt, "找不到可执行的 mpv，请检查路径和文件权限。")
	s.mpvPath = t.TempDir()
	if s.saveMpvPath() {
		t.Fatal("a directory is not an executable")
	}
	path := filepath.Join(t.TempDir(), "mpv.exe")
	if err := os.WriteFile(path, []byte("path-validation fixture, never executed"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.mpvPath = "  " + path + "  "
	tt.Frame()
	click(t, tt, "保存 mpv 路径")
	if got := ctl.View().Config.Render.MpvPath; got != path || s.mpvPathError != "" {
		t.Fatalf("valid path was not submitted: %q, %q", got, s.mpvPathError)
	}
	s.mpvPath = " "
	tt.Frame()
	click(t, tt, "保存 mpv 路径")
	if ctl.View().Config.Render.MpvPath != "" {
		t.Fatal("empty path must restore automatic discovery")
	}
}

func TestMpvRuntimeGuidanceIsLocalizedAndPlatformScoped(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		ctl, s, tt := newUI(t, "windows")
		ctl.UpdateConfig(func(c *config.Config) { c.UI.Lang = lang })
		tt.SetLocale(lang)
		tt.SetSize(1120, 2200)
		s.SetPage("playback")
		tt.Frame()
		must(t, tt, translate(lang, "mpv 路径"), translate(lang, "本安装包不捆绑 mpv。Windows/Linux 使用 mpv 后端播放动态壁纸前，请自行安装 mpv；自动查找不会下载或安装播放器。"))
		s.SetPage("about")
		tt.Frame()
		if strings.Contains(strings.Join(tt.Texts(), " "), "shinchiro") {
			t.Fatal("UI must not claim an unshipped binary is bundled")
		}
	}
	_, s, tt := newUI(t, "darwin")
	s.SetPage("playback")
	tt.Frame()
	if tt.HasText("mpv 路径") {
		t.Fatal("macOS uses system AVFoundation, not mpv")
	}
}
