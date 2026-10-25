package ui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLibraryDensity(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	tt.SetSize(1920, 1080)
	tt.SetTitleBar(ui.TitleBar{Height: 38, Right: 138})
	tt.Frame()
	panel, found := tt.Find("壁纸属性面板")
	if !found || panel.Y > 38 || math.Abs(float64(panel.Y+panel.H-1080)) > 1 {
		t.Fatalf("panel must reach window edges: %+v", panel)
	}
	status, found := tt.Find("播放状态栏")
	if !found || status.X+status.W > panel.X+1 {
		t.Fatalf("status overlaps panel: %+v / %+v", status, panel)
	}
	first, ok := tt.Find(ctl.V.Library[0].Title)
	if !ok || first.Y > 110 {
		t.Fatalf("first tile must start within 110px: %+v", first)
	}
	columns, rows := 0, map[int]bool{}
	for _, it := range ctl.V.Library {
		r, found := tt.Find(it.Title)
		if !found {
			continue
		}
		if math.Abs(float64(r.Y-first.Y)) < 2 {
			columns++
		}
		if r.Y+r.H <= 1036 {
			rows[int(r.Y)] = true
		}
	}
	t.Logf("grid: %d columns, %d visible rows; first tile y=%.1f", columns, len(rows), first.Y)
	if columns < 6 || len(rows) < 4 {
		t.Fatalf("density: %d columns, %d visible rows", columns, len(rows))
	}
}

func TestCompactErrorDismissAndMissingEncoder(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	ctl.V.LastError = "transcode: no compatible encoder; install ffmpeg"
	ctl.V.HasEncoder = false
	s.SelectItem("ocean")
	tt.Frame()
	must(t, tt, "需要 ffmpeg 才能转码", "关闭")
	click(t, tt, "关闭")
	if tt.HasText("出现问题") {
		t.Fatal("error banner was not dismissed")
	}
}

func TestTitleBarControlInsets(t *testing.T) {
	for _, bar := range []ui.TitleBar{{}, {Height: 38, Right: 138}, {Height: 38, Left: 80}} {
		_, _, tt := newUI(t, "windows")
		tt.SetTitleBar(bar)
		tt.Frame()
		title, ok := tt.Find("显示器 1")
		if !ok || title.X < bar.Left {
			t.Fatalf("title overlaps native controls: %+v", title)
		}
		picker, ok := tt.Find("选择显示器")
		if !ok || picker.X+picker.W > 1120-bar.Right {
			t.Fatalf("picker overlaps native controls: %+v", picker)
		}
		click(t, tt, "选择显示器")
		if len(tt.Menu()) != 2 {
			t.Fatal("display picker is blocked by drag region")
		}
	}
}

func TestMacCaptionNavigationFits(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		ctl, s, tt := newUI(t, "darwin")
		ctl.V.Config.UI.Lang = lang
		tt.SetLocale(lang)
		tt.SetTitleBar(ui.TitleBar{Height: 38, Left: 80})
		for _, page := range []string{pgLibrary, pgDisplays, pgPerf} {
			s.SetPage(page)
			for _, width := range []int{900, 1120, 1470} {
				tt.SetSize(width, 740)
				tt.Frame()
				nav, ok := tt.Find(translate(lang, "壁纸库"))
				if !ok || nav.X < 12 || nav.Y < 38 || nav.Y+nav.H > 74 {
					t.Fatalf("%s/%s/%d navigation overlaps caption controls: %+v", lang, page, width, nav)
				}
				picker, ok := tt.Find(translate(lang, "选择显示器"))
				right := float32(width - 12)
				if page == pgLibrary {
					right -= 320
				}
				if !ok || picker.X+picker.W > right+0.5 || picker.Y+picker.H > 38 {
					t.Fatalf("%s/%s/%d display picker overlaps navigation or edge: %+v / %+v", lang, page, width, picker, nav)
				}
				click(t, tt, translate(lang, "选择显示器"))
				if len(tt.Menu()) != 2 {
					t.Fatal("caption drag region blocks display menu")
				}
			}
		}
	}
}

func TestPropertyPanelEmptyAndPageScope(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	ctl.V.Library = nil
	s.Refresh()
	tt.Frame()
	panel, ok := tt.Find("壁纸属性面板")
	if !ok || panel.W != 320 || panel.Y != 0 || panel.Y+panel.H != 740 {
		t.Fatalf("empty panel geometry: %+v", panel)
	}
	must(t, tt, "选择壁纸以查看属性")
	for _, page := range []string{pgDisplays, pgPerf} {
		s.SetPage(page)
		tt.Frame()
		if _, ok := tt.Find("壁纸属性面板"); ok {
			t.Fatal("panel on non-library page")
		}
		status, ok := tt.Find("播放状态栏")
		if !ok || status.W != 1120 {
			t.Fatalf("full-width status: %+v", status)
		}
	}
}

func TestPanelCaptionAndPinnedRegions(t *testing.T) {
	for _, tc := range []struct {
		name, platform string
		bar            ui.TitleBar
		previewY       float32
	}{
		{"windows", "windows", ui.TitleBar{Height: 38, Right: 138}, 38},
		{"macOS", "darwin", ui.TitleBar{Height: 38, Left: 80}, 0},
		{"fallback", "windows", ui.TitleBar{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctl, s, tt := newUI(t, tc.platform)
			s.SelectItem("ocean")
			tt.SetTitleBar(tc.bar)
			tt.Frame()
			preview, ok := tt.Find("壁纸预览")
			if !ok || preview.Y != tc.previewY {
				t.Fatalf("preview overlaps caption or has unwanted strip: %+v", preview)
			}
			apply, _ := tt.Find("应用")
			status, _ := tt.Find("播放状态栏")
			if apply.Y < status.Y || apply.Y+apply.H > status.Y+status.H {
				t.Fatalf("footer alignment: %+v / %+v", apply, status)
			}
			tile, _ := tt.Find(ctl.V.Library[0].Title)
			before, _ := tt.Find("屏幕属性")
			tt.Scroll(preview.X+100, preview.Y+300, 0, 300)
			tt.Frame()
			after, _ := tt.Find("屏幕属性")
			pinned, _ := tt.Find("壁纸预览")
			pinnedApply, _ := tt.Find("应用")
			stationaryTile, _ := tt.Find(ctl.V.Library[0].Title)
			if before == after {
				t.Fatal("properties did not scroll")
			}
			if pinned != preview || pinnedApply != apply || tile != stationaryTile {
				t.Fatal("scroll moved pinned regions or grid")
			}
		})
	}
}

// Navigation and search share one compact toolbar in either window mode.
func TestMacFullscreenHeaderAlignment(t *testing.T) {
	_, _, tt := newUI(t, "darwin")
	for _, width := range []int{900, 1470, 2048} {
		tt.SetSize(width, 900)
		for _, bar := range []ui.TitleBar{{Height: 38, Left: 80}, {}, {Height: 38, Left: 80}, {}} {
			tt.SetTitleBar(bar)
			tt.Frame()
			nav, _ := tt.Find("壁纸库")
			search, _ := tt.Find("搜索壁纸")
			picker, _ := tt.Find("选择显示器")
			if math.Abs(float64(picker.X+picker.W/2)-float64(width-320)/2) > 0.5 {
				t.Fatalf("%d display title must center on library panel: %+v", width, picker)
			}
			if math.Abs(float64(nav.Y+nav.H/2-search.Y-search.H/2)) > 1 || search.X <= nav.X+nav.W || search.Y+search.H > max(32, bar.Height)+36 {
				t.Fatalf("%d toolbar must share one compact row: %+v / %+v", width, nav, search)
			}
			if math.Abs(float64(nav.X-21)) > 1 {
				t.Fatalf("%d fullscreen navigation/search alignment: %+v / %+v", width, nav, search)
			}
			if bar.Left > 0 && nav.Y < bar.Height {
				t.Fatalf("%d windowed navigation overlaps traffic lights: %+v", width, nav)
			}
		}
	}
}
