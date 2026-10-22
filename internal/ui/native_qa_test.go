package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	mui "github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

func TestEnglishDisplayNamesReasonsAndAliases(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1120, 1700)
	tt.SetLocale("en")
	ctl.V.Displays[1].Reasons = "Fullscreen|Battery"
	s.SetPage("displays")
	s.SelectDisplay("d2")
	tt.Frame()
	must(t, tt, "2  Display 2", "Pause reason: Fullscreen application, On battery")
	click(t, tt, "Rename")
	if s.renameBuf != "Display 2" {
		t.Fatalf("generated name was not localized: %q", s.renameBuf)
	}
	click(t, tt, "Save")
	if ctl.View().Displays[1].Alias != "" {
		t.Fatal("saving unchanged localized fallback must not create an alias")
	}
	ctl.SetAlias("d2", "设置")
	tt.Frame()
	must(t, tt, "2  设置")
	if tt.HasText("2  Settings") {
		t.Fatal("translated user-authored display alias")
	}
	s.SelectDisplay("d1")
	tt.Frame()
	must(t, tt, "1  DELL U2723QE (Primary)")
}

func TestDisplayPhysicalPixelsDoNotChangeTopology(t *testing.T) {
	ctl, s, tt := newUI(t, "darwin")
	ctl.V.Displays[0].W, ctl.V.Displays[0].H = 1920, 1080
	ctl.V.Displays[0].PixelW, ctl.V.Displays[0].PixelH = 3840, 2160
	s.SetPage("displays")
	tt.Frame()
	must(t, tt, "3840 × 2160")
	if ctl.V.Displays[0].W != 1920 || ctl.V.Displays[0].H != 1080 {
		t.Fatal("displaying physical pixels changed layout geometry")
	}
	ctl.V.Displays[0].PixelW, ctl.V.Displays[0].PixelH = 0, 0
	tt.Frame()
	must(t, tt, "1920 × 1080")
}

func TestLibraryOptimizationCopyMatchesEncoderCapabilities(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		t.Run(lang, func(t *testing.T) {
			ctl, s, tt := newUI(t, "darwin")
			tt.SetSize(1120, 1100)
			tt.SetLocale(lang)
			s.SelectItem("ocean")
			tt.Frame()
			must(t, tt, translate(lang, "标准转换将验证 30 fps、无音轨和目标尺寸；原文件保留。"))
			ctl.V.Encoder.Strict = false
			tt.Frame()
			must(t, tt, translate(lang, "兼容转换能力取决于编码器，不保证固定帧率或去音轨；原文件保留。"))
			if tt.HasText(translate(lang, "标准转换将验证 30 fps、无音轨和目标尺寸；原文件保留。")) {
				t.Fatal("fallback UI still promises standard conversion")
			}
			if lang == "en" {
				must(t, tt, "Resolution 5120×2880 exceeds 4K", "Frame rate 60 exceeds the 30 fps target")
			}
		})
	}
	if got := mediaNote("en", "自定义备注：通用"); got != "自定义备注：通用" {
		t.Fatalf("unknown source note changed: %q", got)
	}
}

// These tests exercise controls through real scrolling at the minimum window
// size rather than only finding offscreen text in a tall synthetic layout.
// The bottom ~90 px belong to the floating display dock on library/display pages.
func scrollToControl(t *testing.T, tt *mui.Tester, label string) {
	t.Helper()
	for range 15 {
		if r, ok := tt.Find(label); ok && r.Y >= 130 && r.Y+r.H <= 590 && r.X >= 0 && r.X+r.W <= 900 {
			return
		}
		tt.Scroll(850, 420, 0, 130)
		tt.Frame()
	}
	r, _ := tt.Find(label)
	t.Fatalf("%q is not reachable inside 900×680 viewport: %+v", label, r)
}

func TestSmallWindowNativeControlsAndPreviewStates(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(lang+"/"+mode, func(t *testing.T) {
				ctl, s, tt := newUI(t, "darwin")
				ctl.UpdateConfig(func(c *config.Config) { c.UI.Theme = mode; c.UI.Lang = lang })
				s.Info.Version = "native-qa-fixture"
				for i := range ctl.V.Displays {
					ctl.V.Displays[i].Backend, ctl.V.Displays[i].HWDec = "avfoundation", "unknown"
				}
				if thumbs := os.Getenv("WALLPAPER_QA_THUMBS"); thumbs != "" {
					for i := range ctl.V.Library {
						ctl.V.Library[i].Snapshot = filepath.Join(thumbs, ctl.V.Library[i].ID+".png")
					}
				}
				tt.SetSize(900, 680)
				tt.SetLocale(lang)
				tt.SetDark(mode == "dark")
				s.SetPage("displays")
				tt.Frame()
				span := translate(lang, "跨屏拼接")
				scrollToControl(t, tt, span)
				click(t, tt, span)
				if ctl.View().Layout != "span" {
					t.Fatal("scrolled span control did not activate")
				}
				must(t, tt, translate(lang, "各屏保留独立解码与暂停，不保证视频逐帧同步。混合缩放按系统桌面坐标拼接。"))
				saveNativeQAPreview(t, tt, fmt.Sprintf("%s-%s-01-span-scrolled", lang, mode))
				scrollToControl(t, tt, translate(lang, "独立显示器"))
				click(t, tt, translate(lang, "独立显示器"))
				if ctl.View().Layout != "independent" || ctl.View().Displays[1].ItemID != "city" {
					t.Fatal("returning from span lost independent assignment")
				}
				s.SetPage("playback")
				tt.Frame()
				resolution := translate(lang, "主屏物理分辨率")
				scrollToControl(t, tt, resolution)
				click(t, tt, resolution)
				click(t, tt, "2560 × 1440")
				if ctl.View().Config.Render.TranscodeResolution != "1440p" {
					t.Fatal("small-window target resolution was not saved")
				}
				saveNativeQAPreview(t, tt, fmt.Sprintf("%s-%s-02-mac-encoder-scrolled", lang, mode))
				tt.SetSize(1120, 1700)
				ctl.V.Encoder.Strict = false
				tt.Scroll(850, 600, 0, -10000)
				tt.Frame()
				saveNativeQAPreview(t, tt, fmt.Sprintf("%s-%s-03-mac-fallback-full", lang, mode))
				ctl.V.Displays[0].HWDec = "unknown"
				ctl.V.Displays[0].Metrics = app.DisplayMetricsView{WeekStart: "2026-10-19", Paused: 95 * time.Minute, Levels: [5]time.Duration{2 * time.Hour, 35 * time.Minute, 0, 30 * time.Minute, 30 * time.Minute}}
				s.SetPage("about")
				tt.Frame()
				must(t, tt, translate(lang, "硬解状态未验证"), metricDuration(lang, 95*time.Minute))
				saveNativeQAPreview(t, tt, fmt.Sprintf("%s-%s-04-demo-metrics-full", lang, mode))
			})
		}
	}
}

// Optional native-render evidence. This is fixture data, not a hardware result.
func saveNativeQAPreview(t *testing.T, tt *mui.Tester, name string) {
	t.Helper()
	out := os.Getenv("WALLPAPER_QA_SCREENSHOTS")
	if out == "" {
		return
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(out, strings.ReplaceAll(name, "/", "-")+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeNumberControlsStayInsideCards(t *testing.T) {
	_, s, tt := newUI(t, "windows")
	tt.SetSize(900, 680)
	for _, page := range []string{"playback", "rules"} {
		s.SetPage(page)
		tt.Frame()
		r, ok := tt.Find("Increase")
		if !ok || r.X+r.W > 850 {
			t.Fatalf("%s number stepper extends past card content: %+v", page, r)
		}
	}
}

func TestEnglishApplicationLabelsAcrossPages(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			ctl, s, tt := newUI(t, platform)
			ctl.V.LastImport = ""
			ctl.UpdateConfig(func(c *config.Config) { c.UI.Lang = "en" })
			for i := range ctl.V.Library {
				ctl.V.Library[i].Title = "Fixture " + ctl.V.Library[i].ID
			}
			tt.SetLocale("en")
			tt.SetSize(1120, 2200)
			s.SelectItem("ocean")
			for _, page := range []string{"library", "displays", "playback", "rules", "general", "versions", "about"} {
				s.SetPage(page)
				tt.Frame()
				for _, label := range tt.Texts() {
					if strings.ContainsFunc(label, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
						t.Errorf("%s has untranslated application text: %q", page, label)
					}
				}
			}
		})
	}
}

func TestEnglishActionFeedbackKeepsSourceTitles(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetLocale("en")
	tt.SetSize(1120, 1200)
	ctl.V.Library[0].Title = "通用"
	s.SelectItem("aurora")
	tt.Frame()
	click(t, tt, "Apply")
	must(t, tt, "Applied to 1  DELL U2723QE")
	tt.Advance(5 * time.Second)
	click(t, tt, "Remove from library")
	must(t, tt, "Remove “通用”?", "Cancel")
	click(t, tt, "Cancel")
	if len(ctl.Removed) != 0 {
		t.Fatal("cancelled removal still submitted")
	}
	if got := translate("en", "已导入「通用」"); got != "Imported: 通用" {
		t.Fatal(got)
	}
	s.SetPage("playback")
	tt.Frame()
	must(t, tt, "Restore playback position after restarting with mpv, Media Foundation, or AVFoundation.")
}

func TestMediaNotesTranslateGeneratedClausesOnly(t *testing.T) {
	for _, note := range []string{
		"HEVC 需要系统安装 HEVC 视频扩展才能用 Media Foundation 硬解；mpv 后端可自带解码；含音轨：建议去掉音轨以利无缝循环",
		"分辨率 5120x2880 超过 4K；帧率 60 超过优化目标 30；含音轨：建议去掉音轨以利无缝循环",
		"AVFoundation 不能直接播放 webm/vp9；GIF 将转换为视频；未提供编码器时支持的平台仍可直接播放 GIF",
	} {
		if got := mediaNote("en", note); strings.ContainsFunc(got, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Fatalf("generated media note is partially translated: %q", got)
		}
	}
	unknown := "用户备注；后端诊断内容"
	if got := mediaNote("en", unknown); got != unknown {
		t.Fatalf("unknown source note changed: %q", got)
	}
}
