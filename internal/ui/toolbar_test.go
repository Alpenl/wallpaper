package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLibraryToolbarResponsive(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(fmt.Sprintf("empty=%v/%s", empty, mode), func(t *testing.T) {
				ctl, _, tt := newUI(t, "windows")
				ctl.V.Config.UI.Theme = mode
				if empty {
					ctl.V.Library = nil
				}
				tt.SetTitleBar(ui.TitleBar{Height: 38, Right: 138})
				// Repeat wide after shrinking to exercise retained layout state.
				for _, width := range []int{1440, 1280, 1033, 900, 820, int(libraryToolbarWide) + 344 - 1, int(libraryToolbarWide) + 344 + 1, int(libraryToolbarNav) + 344 - 1, int(libraryToolbarNav) + 344 + 1, 820, 1440} {
					tt.SetSize(width, 900)
					tt.Frame()
					labels := []string{"壁纸库", "显示器", "设置", "搜索壁纸", "更多导入", "导入壁纸"}
					if float32(width)-344 < libraryToolbarWide {
						labels = append(labels, "筛选与显示")
					} else {
						labels = append(labels, "筛选结果", "排序", "网格", "列表")
					}
					rects := make([]ui.Rect, 0, len(labels))
					for _, label := range labels {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.X < 12-0.5 || r.X+r.W > float32(width)-320-12+0.5 || r.Y < 38 || r.Y+r.H > 106 {
							t.Fatalf("width %d: %s outside toolbar: %+v", width, label, r)
						}
						if label == "搜索壁纸" && r.W < 140-0.5 {
							t.Fatalf("width %d search too small: %+v", width, r)
						}
						for i, other := range rects {
							if r.X < other.X+other.W-0.5 && other.X < r.X+r.W-0.5 && r.Y < other.Y+other.H-0.5 && other.Y < r.Y+r.H-0.5 {
								t.Fatalf("width %d: %s overlaps %s: %+v / %+v", width, label, labels[i], r, other)
							}
						}
						rects = append(rects, r)
					}
				}
			})
		}
	}
}

func TestStaleImportNotice(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	title := ctl.V.Library[0].Title
	notice := "已导入「" + title + "」（测试警告）"
	ctl.V.LastImport = notice
	tt.Frame()
	must(t, tt, notice)
	ctl.V.Library = ctl.V.Library[1:]
	tt.Frame()
	if tt.HasText(notice) {
		t.Fatal("removed item success notice remains")
	}
	ctl.V.Library = nil
	tt.Frame()
	if tt.HasText(notice) {
		t.Fatal("empty library success notice remains")
	}
	ctl.V.LastImport = "导入已取消；已完成的项目保留在壁纸库"
	tt.Frame()
	must(t, tt, ctl.V.LastImport)
}
