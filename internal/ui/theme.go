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
		// 控件是半透明“磨砂”面：窗口底下铺着模糊的壁纸（ambient），控件透出它的色调。
		// Background 保持不透明：菜单、对话框和弹出层用它做面板。
		if dark {
			t = *ui.DarkTheme()
			t.Background = ui.Hex("#111115")
			t.Surface = ui.RGBA(255, 255, 255, 0.07)
			t.SurfaceHover = ui.RGBA(255, 255, 255, 0.11)
			t.SurfacePressed = ui.RGBA(255, 255, 255, 0.15)
			t.Border = ui.RGBA(255, 255, 255, 0.10)
			t.Text = ui.Hex("#f4f4f5")
			t.TextMuted = ui.Hex("#a1a1aa")
		} else {
			t = *ui.LightTheme()
			t.Background = ui.Hex("#f6f5f2")
			t.Surface = ui.RGBA(255, 255, 255, 0.72)
			t.SurfaceHover = ui.RGBA(255, 255, 255, 0.92)
			t.SurfacePressed = ui.RGBA(0, 0, 0, 0.05)
			t.Border = ui.RGBA(0, 0, 0, 0.08)
			t.Text = ui.Hex("#18181b")
			t.TextMuted = ui.Hex("#6b6b74")
		}
		a := accentColor(u.Accent)
		t.Accent = a
		t.AccentHover = a.Mix(ui.Hex("#ffffff"), 0.12)
		t.AccentPressed = a.Mix(ui.Hex("#000000"), 0.12)
		t.Focus = a.Alpha(0.6)
		t.Selection = a.Alpha(0.25)
		t.Radius = 10
		s.theme, s.themeKey = &t, key
	}
	c.SetTheme(s.theme)
}

// ---- 设计令牌 ----
// 视觉语言：模糊壁纸做环境底色 + 磨砂玻璃面板 + 细描边 + 大标题/小字距标签。

// glass 是浮在环境底色上的面板。
func glass(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.055)
	}
	return ui.RGBA(255, 255, 255, 0.62)
}

// glassStrong 用于需要更实的面（底部 Dock、选中的标签）。
func glassStrong(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(30, 30, 36, 0.78)
	}
	return ui.RGBA(255, 255, 255, 0.86)
}

// hairline 是玻璃面板的 1px 描边。
func hairline(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.09)
	}
	return ui.RGBA(0, 0, 0, 0.07)
}

// ambientVeil 盖在模糊壁纸上，让文字保持对比度。
func ambientVeil(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(10, 10, 14, 0.64)
	}
	return ui.RGBA(246, 245, 242, 0.72)
}

func softShadow(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.45)
	}
	return ui.RGBA(30, 30, 50, 0.16)
}

// eyebrow 是标题上方的小字标签（加大字距）。
func eyebrow(c *ui.Context, label string) ui.Element {
	return ui.Text(c, label).FontSize(11).LetterSpacing(1.2).Bold().TextColor(c.Theme().TextMuted).SingleLine().Ellipsis("…")
}

// display 是页面主标题。
func display(c *ui.Context, label string, size float32) ui.Element {
	return ui.Text(c, label).FontSize(size).Bold().LetterSpacing(-0.02 * size)
}

func cardBg(t *ui.Theme) ui.Color { return glass(t) }

func sideBg(t *ui.Theme) ui.Color { return glassStrong(t) }
