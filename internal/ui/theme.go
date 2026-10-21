package ui

import (
	"github.com/egoist/mygo/ui"

	"wpapp/internal/config"
)

// 强调色（外观页）。
var accents = []struct{ id, name, hex string }{
	{"blue", "蓝", "#2563eb"}, {"purple", "紫", "#7c3aed"}, {"green", "绿", "#059669"}, {"orange", "橙", "#ea580c"}, {"pink", "粉", "#db2777"},
}

func accentColor(id string) ui.Color {
	for _, a := range accents {
		if a.id == id {
			return ui.Hex(a.hex)
		}
	}
	return ui.Hex(accents[0].hex)
}

// applyTheme 按 config.ui 生成主题：浅色 / 深色 / 跟随系统 + 强调色，配色比默认主题更“应用化”（浅灰底、白卡片）。
func (s *Settings) applyTheme(c *ui.Context, u config.UI) {
	dark := c.Theme().Dark
	switch u.Theme {
	case "light":
		dark = false
	case "dark":
		dark = true
	}
	key := u.Accent
	if dark {
		key += "/dark"
	}
	if s.theme == nil || s.themeKey != key {
		var t ui.Theme
		if dark {
			t = *ui.DarkTheme()
			t.Background = ui.Hex("#15171c")
			t.Surface = ui.Hex("#262a32")
			t.SurfaceHover = ui.Hex("#2e333c")
			t.SurfacePressed = ui.Hex("#363b45")
			t.Border = ui.Hex("#2f343d")
			t.TextMuted = ui.Hex("#9aa3b2")
		} else {
			t = *ui.LightTheme()
			t.Background = ui.Hex("#f4f5f7")
			t.Surface = ui.Hex("#ffffff")
			t.SurfaceHover = ui.Hex("#f1f3f6")
			t.SurfacePressed = ui.Hex("#e7eaef")
			t.Border = ui.Hex("#e3e6eb")
			t.TextMuted = ui.Hex("#6b7280")
		}
		a := accentColor(u.Accent)
		t.Accent = a
		t.AccentHover = a.Mix(ui.Hex("#ffffff"), 0.12)
		t.AccentPressed = a.Mix(ui.Hex("#000000"), 0.12)
		t.Focus = a.Alpha(0.6)
		t.Selection = a.Alpha(0.25)
		t.Radius = 8
		s.theme, s.themeKey = &t, key
	}
	c.SetTheme(s.theme)
}

func cardBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#1d2026")
	}
	return ui.Hex("#ffffff")
}

func sideBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#111317")
	}
	return ui.Hex("#eceef2")
}
