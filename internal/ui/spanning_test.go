package ui

import (
	"strings"
	"testing"
	"unicode"
)

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

func TestSpanningDisablesAudioOnSecondaryDisplay(t *testing.T) {
	ctl, s, tt := newUI(t, "windows")
	tt.SetSize(1400, 2000)
	s.SetPage("displays")
	tt.Frame()
	click(t, tt, "跨屏拼接")

	// Primary display keeps audio control active.
	s.SelectDisplay("d1")
	tt.Frame()
	if tt.HasText("跨屏模式下由主屏统一控制声音，副屏保持静音。") {
		t.Fatal("primary spanning display showed secondary-muted notice")
	}

	// Secondary display shows accurate helper text and disables audio dropdown.
	s.SelectDisplay("d2")
	tt.Frame()
	must(t, tt, "跨屏模式下由主屏统一控制声音，副屏保持静音。")
	before := ctl.View().Displays[1].Options.Audio
	_ = tt.Click("这块屏的声音")
	tt.Frame()
	if ctl.View().Displays[1].Options.Audio != before {
		t.Fatalf("disabled secondary audio control mutated options: %q -> %q", before, ctl.View().Displays[1].Options.Audio)
	}

	// English locale renders translated helper text with no Chinese leak.
	ctl.V.Config.UI.Lang = "en"
	tt.SetLocale("en")
	tt.Frame()
	wantEn := "In spanning mode, audio is controlled by the primary spanning display; secondary displays stay muted."
	must(t, tt, wantEn)
	if strings.ContainsFunc(wantEn, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		t.Fatal("expected English notice")
	}
}
