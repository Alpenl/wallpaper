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
			t.Background = ui.Hex("#18181b")
			t.Surface = ui.Hex("#27272a")
			t.SurfaceHover = ui.Hex("#303034")
			t.SurfacePressed = ui.Hex("#38383d")
			t.Border = ui.Hex("#303034")
			t.TextMuted = ui.Hex("#8e8e96")
		} else {
			t = *ui.LightTheme()
			t.Background = ui.Hex("#f7f7f5")
			t.Surface = ui.Hex("#ffffff")
			t.SurfaceHover = ui.Hex("#f0f0ed")
			t.SurfacePressed = ui.Hex("#e7e7e4")
			t.Border = ui.Hex("#e8e8e5")
			t.TextMuted = ui.Hex("#71717a")
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
		return ui.Hex("#202024")
	}
	return ui.Hex("#ffffff")
}

func sideBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#141416")
	}
	return ui.Hex("#f0f0ed")
}
