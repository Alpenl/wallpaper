package ui

import (
	"github.com/egoist/mygo/ui"
	"math"
	"testing"
	"wpapp/internal/config"
)

func TestDarkTextContrast(t *testing.T) {
	s := &Settings{}
	tt := ui.NewTester(func(c *ui.Context) { s.applyTheme(c, config.UI{Theme: "dark"}) }, 900, 700)
	tt.Frame()
	theme := s.theme
	luminance := func(c ui.Color) float64 {
		linear := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.04045 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
	}
	for name, bg := range map[string]ui.Color{"window": theme.Background, "panel": panelBg(theme), "raised": raisedBg(theme), "bar": barBg(theme), "hover": theme.SurfaceHover, "pressed": theme.SurfacePressed, "segment": ui.Hex("#35394f"), "hero glow": theme.Accent.Mix(panelBg(theme), 0.72)} {
		for _, text := range []struct {
			name  string
			color ui.Color
			min   float64
		}{{"muted", theme.TextMuted, 4.5}, {"faint", faint(theme), 3}} {
			ratio := (luminance(text.color) + 0.05) / (luminance(bg) + 0.05)
			t.Logf("%s on %s: %.2f:1", text.name, name, ratio)
			if ratio < text.min {
				t.Errorf("%s on %s: %.2f < %.1f", text.name, name, ratio, text.min)
			}
		}
	}
}
