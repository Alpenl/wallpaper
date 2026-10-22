package ui

import (
	"github.com/egoist/mygo/ui"

	"wpapp/internal/config"
)

// 强调色（外观页）。
var accents = []struct{ id, name, hex string }{
	{"blue", "蓝", "#3b82f6"}, {"purple", "紫", "#8b5cf6"}, {"green", "绿", "#10b981"}, {"orange", "橙", "#f97316"}, {"pink", "粉", "#ec4899"},
}

func accentColor(id string) ui.Color {
	for _, a := range accents {
		if a.id == id {
			return ui.Hex(a.hex)
		}
	}
	return ui.Hex(accents[0].hex)
}

// applyTheme 按 config.ui 生成主题：浅色 / 深色 / 跟随系统 + 强调色。
//
// 视觉系统（布局参照 Wallpaper Engine，质感参照现代桌面应用）：
//   - 底色分三层：窗口底 < 面板 < 卡片/控件，靠明度差和 1px 细描边区分，不用重阴影；
//   - 圆角 8（控件）/ 12（卡片、封面）/ 16（大面板）；
//   - 强调色只用在选中、主按钮（带渐变和光晕）和滑块；
//   - 文字三档：正文、次要、弱；标题用 600 字重、略收字距。不用图标。
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
			t.Background = ui.Hex("#0e1014")
			t.Surface = ui.Hex("#1c1f26")
			t.SurfaceHover = ui.Hex("#242832")
			t.SurfacePressed = ui.Hex("#2b303b")
			t.Border = ui.RGBA(255, 255, 255, 0.07)
			t.Text = ui.Hex("#e8eaf0")
			t.TextMuted = ui.Hex("#8a91a0")
		} else {
			t = *ui.LightTheme()
			t.Background = ui.Hex("#eef0f4")
			t.Surface = ui.Hex("#ffffff")
			t.SurfaceHover = ui.Hex("#f5f6f9")
			t.SurfacePressed = ui.Hex("#eceef3")
			t.Border = ui.RGBA(15, 23, 42, 0.10)
			t.Text = ui.Hex("#141821")
			t.TextMuted = ui.Hex("#5f6778")
		}
		a := accentColor(u.Accent)
		t.Accent = a
		t.AccentHover = a.Mix(ui.Hex("#ffffff"), 0.12)
		t.AccentPressed = a.Mix(ui.Hex("#000000"), 0.12)
		t.AccentText = ui.Hex("#ffffff")
		t.Focus = a.Alpha(0.6)
		t.Selection = a.Alpha(0.25)
		t.Radius = 6
		t.Spacing = 3
		s.theme, s.themeKey = &t, key
	}
	c.SetTheme(s.theme)
}

// ---- 设计令牌 ----

// panelBg 是面板层（右侧属性栏、设置分组、底部栏）。
func panelBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#15181e")
	}
	return ui.Hex("#f8f9fb")
}

// raisedBg 是面板里再抬高一层的分组卡片（属性组、详细信息）。
func raisedBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#1b1e25")
	}
	return ui.Hex("#ffffff")
}

// barBg 是顶部和底部栏。
func barBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#121419")
	}
	return ui.Hex("#f8f9fb")
}

// lineColor 是细分隔线。
func lineColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.06)
	}
	return ui.RGBA(15, 23, 42, 0.08)
}

// faint 是第三档文字（元数据、占位）。
func faint(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#5d6473")
	}
	return ui.Hex("#9097a6")
}

func shadowColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.5)
	}
	return ui.RGBA(20, 30, 60, 0.12)
}

// heading 是标题（“显示器 1”、页面标题）。
func heading(c *ui.Context, label string, size float32) ui.Element {
	return ui.Text(c, label).FontSize(size).FontWeight(650).LetterSpacing(-0.02 * size)
}

// overline 是小号大字距的分节标签（“属性”“详细信息”“目标显示器”）。
func overline(c *ui.Context, label string) ui.Element {
	return ui.Text(c, label).FontSize(11).FontWeight(600).LetterSpacing(1.2).TextColor(faint(c.Theme())).SingleLine()
}

// sectionHeader 是面板里的小节标题：大字距小标签，右侧可放一个操作。
func sectionHeader(c *ui.Context, key, label string, right func()) {
	ui.Row(c.Key(key)).Gap(10).AlignItems(ui.Center).Padding(10, 2, 2, 2).Children(func() {
		overline(c, label).Grow(1)
		if right != nil {
			right()
		}
	})
}

// accentButton 是主操作：强调色渐变 + 柔和光晕。
func accentButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	return primaryButton(c, label).Radius(9).Height(26).FontSize(13).Padding(4, 12).FontWeight(600).
		LinearGradient(ui.LinearGradient{From: t.Accent.Mix(ui.Hex("#ffffff"), 0.08), To: t.Accent.Mix(ui.Hex("#7c3aed"), 0.28), Angle: 120}).
		Shadow(0, 6, 18, -6, t.Accent.Alpha(0.7))
}

// ghostButton 是次要操作：无底色，悬停时出现浅底。
func ghostButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	b := button(c, label).Radius(8).Height(26).FontSize(13).Padding(4, 8).Background(ui.Transparent).BorderWidth(0).TextColor(t.TextMuted)
	if b.Hovered() {
		b.Background(t.SurfaceHover).TextColor(t.Text)
	}
	return b
}

// softButton 是面板里的普通按钮：卡片底色 + 细描边。
func softButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	return button(c, label).Radius(8).Height(26).FontSize(13).Padding(4, 8).Background(t.Surface).Border(1, t.Border)
}

// segment 是分段控件里的一项；容器见 segmentGroup。
func segment(c *ui.Context, label string, sel bool) ui.Element {
	t := c.Theme()
	b := button(c, label).Radius(7).Height(24).Padding(2, 6).BorderWidth(0).FontSize(13)
	if sel {
		bg := ui.Hex("#ffffff")
		if t.Dark {
			bg = ui.Hex("#2a2f3a")
		}
		b.Background(bg).TextColor(t.Text).FontWeight(600).Shadow(0, 1, 3, 0, shadowColor(t))
	} else {
		b.Background(ui.Transparent).TextColor(t.TextMuted)
	}
	return b
}

// segmentGroup 是分段控件的槽。
func segmentGroup(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	bg := ui.Hex("#e3e6ec")
	if t.Dark {
		bg = ui.Hex("#171a20")
	}
	return ui.Row(c).Gap(2).Padding(2).Radius(6).Background(bg).Border(1, lineColor(t)).AlignItems(ui.Center).Children(fn)
}

func cardBg(t *ui.Theme) ui.Color { return panelBg(t) }
