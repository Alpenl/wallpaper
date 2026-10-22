package ui

import (
	"errors"
	"strings"
	"testing"

	"wpapp/internal/config"
)

func TestMpvPathEditorValidatesAndSubmits(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	const path = "valid-mpv"
	const validationError = "mpv executable unavailable: invalid-mpv"
	var validated []string
	s.ValidateMpvPath = func(p string) error {
		validated = append(validated, p)
		if p != path {
			return errors.New(validationError)
		}
		return nil
	}
	tt.SetSize(1120, 2200)
	s.SetPage("playback")
	tt.Frame()
	must(t, tt, "mpv 路径", "保存 mpv 路径")
	s.mpvPath = "invalid-mpv"
	tt.Frame()
	click(t, tt, "保存 mpv 路径")
	if ctl.View().Config.Render.MpvPath != "" || s.mpvPathError == "" {
		t.Fatal("missing executable must not be saved or reported as success")
	}
	must(t, tt, "找不到可执行的 mpv，请检查路径和文件权限。", validationError)
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
	if got := strings.Join(validated, ","); got != "invalid-mpv,valid-mpv" {
		t.Fatalf("validation calls = %q; want only nonempty, trimmed paths", got)
	}
}

func TestMpvPathEditorInitialValidation(t *testing.T) {
	for _, path := range []string{"", "configured-mpv"} {
		t.Run(path, func(t *testing.T) {
			ctl, s, tt := newUI(t, "windows")
			ctl.UpdateConfig(func(c *config.Config) { c.Render.MpvPath = path })
			calls := 0
			const message = "configured executable unavailable"
			s.ValidateMpvPath = func(got string) error {
				calls++
				if got != path {
					t.Fatalf("validated %q, want %q", got, path)
				}
				return errors.New(message)
			}
			tt.SetSize(1120, 2200)
			s.SetPage("playback")
			tt.Frame()
			tt.Frame()
			if path == "" {
				if calls != 0 || s.mpvPathError != "" {
					t.Fatal("empty initial path must not be validated")
				}
			} else {
				if calls != 1 {
					t.Fatalf("initial validation calls = %d, want 1", calls)
				}
				must(t, tt, message)
			}
		})
	}
}

func TestMpvPathEditorWithoutValidator(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	ctl.UpdateConfig(func(c *config.Config) { c.Render.MpvPath = "configured-mpv" })
	s.SetPage("playback")
	tt.Frame()
	if s.mpvPathError != "" {
		t.Fatalf("nil validator rejected initial path: %s", s.mpvPathError)
	}
	s.mpvPath = "  another-mpv  "
	if !s.saveMpvPath() || ctl.View().Config.Render.MpvPath != "another-mpv" {
		t.Fatal("nil validator must allow saving a trimmed path")
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
