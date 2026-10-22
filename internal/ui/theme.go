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
		// 参照 Wallpaper Engine：平面、近直角、深色为主；层次靠底色深浅区分，不用阴影和图标。
		if dark {
			t = *ui.DarkTheme()
			t.Background = ui.Hex("#1b1c20")
			t.Surface = ui.Hex("#2d2e34")
			t.SurfaceHover = ui.Hex("#37383f")
			t.SurfacePressed = ui.Hex("#414249")
			t.Border = ui.Hex("#3a3b42")
			t.Text = ui.Hex("#e9e9ec")
			t.TextMuted = ui.Hex("#9a9ba4")
		} else {
			t = *ui.LightTheme()
			t.Background = ui.Hex("#e9ebee")
			t.Surface = ui.Hex("#ffffff")
			t.SurfaceHover = ui.Hex("#f2f3f6")
			t.SurfacePressed = ui.Hex("#e3e5ea")
			t.Border = ui.Hex("#d3d6dc")
			t.Text = ui.Hex("#1c1e23")
			t.TextMuted = ui.Hex("#686d77")
		}
		a := accentColor(u.Accent)
		t.Accent = a
		t.AccentHover = a.Mix(ui.Hex("#ffffff"), 0.12)
		t.AccentPressed = a.Mix(ui.Hex("#000000"), 0.12)
		t.Focus = a.Alpha(0.6)
		t.Selection = a.Alpha(0.25)
		t.Radius = 3
		s.theme, s.themeKey = &t, key
	}
	c.SetTheme(s.theme)
}

// ---- 设计令牌 ----
// 视觉语言参照 Wallpaper Engine：平面色块、3px 以内圆角、强调色只用于选中与主操作，全程不用图标。

// panelBg 是内容面板（右侧属性栏、设置分组）。
func panelBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#232429")
	}
	return ui.Hex("#f7f8fa")
}

// barBg 是顶部和底部栏。
func barBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#16171a")
	}
	return ui.Hex("#dfe2e7")
}

// lineColor 是面板内的分隔线。
func lineColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#303137")
	}
	return ui.Hex("#dcdfe4")
}

// heading 是大标题（“显示器 1”、页面标题）。
func heading(c *ui.Context, label string, size float32) ui.Element {
	return ui.Text(c, label).FontSize(size).FontWeight(500)
}

// sectionHeader 是“属性”这类小节标题：文字 + 一条强调色细线（Wallpaper Engine 的分节样式）。
func sectionHeader(c *ui.Context, key, label string, right func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).Gap(10).AlignItems(ui.Center).Padding(6, 0, 2, 0).Children(func() {
		text(c, label).FontSize(14).SingleLine()
		ui.Box(c).Grow(1).Height(1).Background(t.Accent.Alpha(0.55)).Children(func() {})
		if right != nil {
			right()
		}
	})
}

func cardBg(t *ui.Theme) ui.Color { return panelBg(t) }
