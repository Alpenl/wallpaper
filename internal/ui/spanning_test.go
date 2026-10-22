package ui

import "testing"

func TestSpanningControlsPreserveIndependentAssignments(t *testing.T) {
	ctl, _, tt := newUI(t, "windows")
	tt.SetSize(1400, 1800)
	click(t, tt, "显示器")
	must(t, tt, "独立显示器", "跨屏拼接")
	click(t, tt, "跨屏拼接")
	must(t, tt, "跨屏壁纸", "修改后应用到整个跨屏画布")
	v := ctl.View()
	if v.Layout != "span" || v.Displays[0].ItemID != v.Displays[1].ItemID {
		t.Fatal("span selection not routed")
	}
	if tt.HasText("复制到所有显示器") {
		t.Fatal("span must not be presented as duplicate")
	}
	click(t, tt, "独立显示器")
	v = ctl.View()
	if v.Layout != "independent" || v.Displays[1].ItemID != "city" {
		t.Fatal("independent assignment lost")
	}
	must(t, tt, "复制到所有显示器")
}
func TestSpanningControlsExplainUnsupportedHost(t *testing.T) {
	ctl, _, tt := newUI(t, "linux")
	ctl.V.Capabilities.Spanning = false
	tt.SetSize(1400, 1800)
	click(t, tt, "显示器")
	must(t, tt, "当前桌面宿主不支持跨屏画布；复制壁纸仍可使用。")
	click(t, tt, "跨屏拼接")
	if ctl.View().Layout == "span" {
		t.Fatal("unsupported host entered span")
	}
}
