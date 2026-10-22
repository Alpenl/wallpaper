// uishots 用演示数据渲染设置窗口的每一页（浅色 + 深色），保存 PNG。无需窗口和显示服务器。
//
//	go run ./cmd/uishots -out /workspace/ui_screens
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	mui "github.com/egoist/mygo/ui"

	"wpapp/internal/config"
	"wpapp/internal/ui"
	"wpapp/internal/ui/uidemo"
)

func main() {
	out := flag.String("out", "ui_screens", "输出目录")
	lang := flag.String("lang", "zh-CN", "界面语言（zh-CN / en）")
	width := flag.Int("width", 1120, "窗口宽度")
	height := flag.Int("height", 740, "窗口高度")
	goos := flag.String("goos", "windows", "模拟的平台（影响平台相关设置）")
	scale := flag.Float64("scale", 1.5, "像素缩放")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		panic(err)
	}
	thumbs := map[string]string{}
	for i, id := range []string{"aurora", "ocean", "city", "forest", "pixel", "mount", "web-orbit", "shader-waves"} {
		p := filepath.Join(*out, ".thumbs", id+".png")
		os.MkdirAll(filepath.Dir(p), 0o755)
		save(p, scene(i, 640, 360))
		thumbs[id] = p
	}
	shots := []struct{ name, page, sel string }{
		{"01-library", "library", "ocean"}, {"01b-web", "library", "web-orbit"}, {"01c-shader", "library", "shader-waves"}, {"02-displays", "displays", ""}, {"02b-displays-rename", "displays", ""}, {"02c-displays-alias", "displays", ""},
		{"03-playback", "playback", ""}, {"04-rules", "rules", ""}, {"05-general", "general", ""}, {"06-versions", "versions", ""}, {"07-about", "about", ""},
	}
	for _, mode := range []string{"light", "dark"} {
		for _, sh := range shots {
			ctl := uidemo.New(thumbs)
			m := mode
			ctl.UpdateConfig(func(c *config.Config) { c.UI.Theme = m; c.UI.Lang = *lang })
			s := ui.NewSettings(ctl, *goos)
			s.Info = ui.Info{Version: "ui-development", Platform: *goos + "/amd64", EncoderName: "ffmpeg（用户安装）", MpvPath: `C:\Users\me\AppData\Local\Programs\Wallpaper\mpv\mpv.exe`,
				DataDir: `C:\Users\me\AppData\Roaming\Wallpaper`, LogsDir: `C:\Users\me\AppData\Roaming\Wallpaper\logs`, UpdateKey: true}
			s.PickFiles = func() []string { return nil }
			s.ExportDiag = func() (string, error) { return "diag.zip", nil }
			s.CheckUpdate = func() (*ui.UpdateInfo, error) { return nil, nil }
			s.SetLaunchAtLogin = func(bool) error { return nil }
			s.SetPage(sh.page)
			if sh.sel != "" {
				s.SelectItem(sh.sel)
			}
			switch sh.name {
			case "02b-displays-rename":
				s.SelectDisplay("d2")
				s.StartRename("d2", "书桌左屏")
			case "02c-displays-alias":
				ctl.SetAlias("d2", "书桌左屏")
				s.SelectDisplay("d2")
			}
			s.Refresh()
			tt := mui.NewTester(s.View, *width, *height)
			tt.SetScale(float32(*scale))
			tt.SetDark(mode == "dark")
			tt.SetLocale(*lang)
			tt.Frame()
			p := filepath.Join(*out, fmt.Sprintf("%s-%s.png", sh.name, mode))
			save(p, tt.Image())
			fmt.Println(p)
		}
	}
	// 空库（首次运行）
	ctl := uidemo.New(nil)
	ctl.V.Library, ctl.V.LastImport = nil, ""
	for i := range ctl.V.Displays {
		ctl.V.Displays[i].ItemID = ""
	}
	ctl.UpdateConfig(func(c *config.Config) { c.UI.Lang = *lang })
	s := ui.NewSettings(ctl, *goos)
	s.Info = ui.Info{Version: "ui-development"}
	s.PickFiles = func() []string { return nil }
	tt := mui.NewTester(s.View, *width, *height)
	tt.SetScale(float32(*scale))
	tt.SetLocale(*lang)
	tt.Frame()
	p := filepath.Join(*out, "00-empty-library-light.png")
	save(p, tt.Image())
	fmt.Println(p)
}

func save(p string, img image.Image) {
	f, err := os.Create(p)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// scene 画一张程序生成的演示缩略图（不使用任何外部素材）。
func scene(kind, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	lerp := func(a, b color.NRGBA, t float64) color.NRGBA {
		t = math.Max(0, math.Min(1, t))
		return color.NRGBA{uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t), uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t), uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t), 255}
	}
	pals := [][2]color.NRGBA{
		{{8, 12, 40, 255}, {20, 60, 90, 255}}, {{10, 70, 130, 255}, {120, 200, 230, 255}}, {{25, 10, 50, 255}, {120, 30, 110, 255}},
		{{20, 60, 40, 255}, {170, 200, 170, 255}}, {{250, 200, 120, 255}, {120, 170, 240, 255}}, {{40, 80, 140, 255}, {220, 235, 250, 255}},
	}
	p := pals[kind%len(pals)]
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fy, fx := float64(y)/float64(h), float64(x)/float64(w)
			c := lerp(p[0], p[1], fy)
			switch kind {
			case 0: // 极光
				band := math.Exp(-math.Pow((fy-0.35-0.08*math.Sin(fx*7))*9, 2))
				c = lerp(c, color.NRGBA{60, 255, 170, 255}, band*0.8)
				if (x*7919+y*104729)%997 == 0 {
					c = color.NRGBA{255, 255, 255, 255}
				}
			case 1: // 海浪
				if fy > 0.55+0.04*math.Sin(fx*18+fy*30) {
					c = lerp(color.NRGBA{20, 90, 150, 255}, color.NRGBA{5, 30, 70, 255}, (fy-0.55)*2)
				}
			case 2: // 城市
				bw := 40
				bh := 0.35 + 0.4*math.Abs(math.Sin(float64(x/bw)*2.3))
				if fy > 1-bh {
					c = color.NRGBA{15, 10, 30, 255}
					if (x%bw) > 8 && (x%bw) < bw-8 && (y/12)%2 == 0 && (x/6)%2 == 0 {
						c = color.NRGBA{255, 80, 200, 255}
					}
				}
			case 3: // 雨林
				tree := 0.55 + 0.15*math.Sin(fx*25) + 0.05*math.Sin(fx*90)
				if fy > tree {
					c = lerp(color.NRGBA{30, 90, 50, 255}, color.NRGBA{10, 40, 20, 255}, fy)
				}
				c = lerp(c, color.NRGBA{230, 240, 230, 255}, math.Exp(-math.Pow((fy-0.6)*6, 2))*0.35)
			case 4: // 像素
				if (x/20+y/20)%2 == 0 && fy > 0.6 {
					c = color.NRGBA{90, 160, 80, 255}
				} else if fy > 0.6 {
					c = color.NRGBA{110, 180, 90, 255}
				}
				if fy > 0.35 && fy < 0.6 && (x/40)%3 == 1 {
					c = color.NRGBA{200, 90, 70, 255}
				}
			case 5: // 雪山
				peak := 0.75 - 0.5*math.Max(0, 1-math.Abs(fx-0.5)*2.4)
				if fy > peak {
					c = color.NRGBA{70, 90, 120, 255}
					if fy < peak+0.08 {
						c = color.NRGBA{245, 248, 252, 255}
					}
				}
			}
			img.Set(x, y, c)
		}
	}
	return img
}
