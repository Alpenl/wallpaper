package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"slices"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

// Shell 是托盘 + 设置窗口。必须在 mygo.App.WhenReady 里创建。
//
// MyGo 绕行（README「MyGo workaround」一节）：
//  1. 关闭最后一个窗口默认会退出应用：用 App.OnWindowAllClosed(空函数) 保持托盘常驻；设置窗口关闭后按需重建。
//  2. 托盘菜单项的 Checked 需要整份菜单重建（SetMenu）才会刷新：只在菜单相关状态变化时重建。
//  3. 托盘图标需要 PNG 字节：运行时用 image/png 画一个 32×32 图标，不提交二进制资源。
type Shell struct {
	S      *Settings
	OnQuit func()

	mu          sync.Mutex
	win         *mygo.Window
	tray        *mygo.Tray
	lastMenu    trayMenuState
	lastToolTip string
}

// Icon 生成托盘图标：与应用图标一致的简化层叠壁纸卡片。
// 托盘图标运行时绘制 32×32 PNG，不额外提交托盘专用二进制资源；应用/安装器/
// macOS 图标资产见 assets/icon。8× 超采样让通知区域边缘平滑。
func Icon() []byte {
	const size, samples = 32, 8
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	bg := iconColor{11, 31, 56}
	mid := iconColor{74, 159, 232}
	front := iconColor{247, 247, 242}
	ink := iconColor{18, 58, 140}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sumR, sumG, sumB, sumA float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/samples
					py := float64(y) + (float64(sy)+0.5)/samples
					c, a := iconSample(px, py, bg, mid, front, ink)
					sumR += c.r * a
					sumG += c.g * a
					sumB += c.b * a
					sumA += a
				}
			}
			n := float64(samples * samples)
			if sumA == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(sumR/sumA + 0.5),
				G: uint8(sumG/sumA + 0.5),
				B: uint8(sumB/sumA + 0.5),
				A: uint8(sumA/n*255 + 0.5),
			})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

type iconColor struct{ r, g, b float64 }

// iconSample 返回子像素的颜色与覆盖；图层从后到前：圆角底、浅蓝卡片、白卡、山与播放。
func iconSample(x, y float64, bg, mid, front, ink iconColor) (iconColor, float64) {
	mountain := inTriangle(x, y, 8, 18.5, 12, 11.8, 16, 18.5) ||
		inTriangle(x, y, 14, 18.5, 18, 13.6, 24, 18.5) ||
		(x >= 8 && x <= 24 && y >= 17.6 && y <= 18.8)
	play := inTriangle(x, y, 14, 19.8, 14, 24.8, 21.2, 22.3)
	switch {
	case mountain || play:
		return ink, 1
	case inRoundedRect(x, y, 7, 7, 26, 26, 3):
		return front, 1
	case inRoundedRect(x, y, 4, 4, 27, 27, 4):
		return mid, 1
	case inRoundedRect(x, y, 2, 2, 30, 30, 6):
		return bg, 1
	}
	return iconColor{}, 0
}

func inTriangle(x, y, ax, ay, bx, by, cx, cy float64) bool {
	d1 := (x-bx)*(ay-by) - (ax-bx)*(y-by)
	d2 := (x-cx)*(by-cy) - (bx-cx)*(y-cy)
	d3 := (x-ax)*(cy-ay) - (cx-ax)*(y-ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func inRoundedRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := min(max(x, x0+r), x1-r)
	cy := min(max(y, y0+r), y1-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

type trayDisplayState struct {
	ID      string
	Index   int
	Name    string
	HasName bool
	Paused  bool
}

type trayMenuState struct {
	PausedAll  bool
	Profile    string
	FewItems   bool
	Header     string
	Status     string
	Recent     []trayWallpaperState
	Covered    bool
	Fullscreen bool
	Importing  bool
	Lang       string
	SafeMode   bool
	Displays   []trayDisplayState
}

func trayState(v app.View) trayMenuState {
	s := trayMenuState{PausedAll: v.PausedAll, Profile: v.Config.Perf.Profile,
		FewItems: len(v.Library) < 2, Lang: v.Config.UI.Lang, SafeMode: v.SafeMode}
	s.Header, s.Status = traySummary(v)
	s.Recent = trayRecent(v)
	s.Covered, s.Fullscreen, s.Importing = v.Config.Pause.OnCovered, v.Config.Pause.OnFullscreen, v.Importing
	for _, d := range v.Displays {
		s.Displays = append(s.Displays, trayDisplayState{d.ID, d.Index, d.Name, d.Alias != "" || d.Friendly != "", d.Options.Paused})
	}
	return s
}

func (s trayMenuState) equal(other trayMenuState) bool {
	return s.PausedAll == other.PausedAll && s.Profile == other.Profile &&
		s.FewItems == other.FewItems && s.Lang == other.Lang && s.SafeMode == other.SafeMode &&
		s.Header == other.Header && s.Status == other.Status && s.Covered == other.Covered &&
		s.Fullscreen == other.Fullscreen && s.Importing == other.Importing &&
		slices.Equal(s.Recent, other.Recent) && slices.Equal(s.Displays, other.Displays)
}

func trayToolTip(v app.View) string {
	label := "动态壁纸"
	if v.SafeMode {
		label = "动态壁纸 · 安全模式"
	} else if v.PausedAll {
		label = "动态壁纸 · 已手动暂停全部"
	}
	return translate(v.Config.UI.Lang, label)
}

// updateTrayState 在 sh.mu 保护下比较并保存本次快照。
func (sh *Shell) updateTrayState(v app.View) (menuChanged, tipChanged bool) {
	state, tip := trayState(v), trayToolTip(v)
	menuChanged, tipChanged = !state.equal(sh.lastMenu), tip != sh.lastToolTip
	sh.lastMenu, sh.lastToolTip = state, tip
	return
}

func (sh *Shell) menu(v app.View) *mygo.Menu {
	prof := v.Config.Perf.Profile
	tr := func(label string) string { return translate(v.Config.UI.Lang, label) }
	profItem := func(id, label string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: tr(label), Type: mygo.MenuItemRadio, Checked: prof == id, Click: func(*mygo.MenuItem, *mygo.Window) {
			sh.S.ctl.UpdateConfig(func(c *config.Config) { c.Perf.Profile = id })
		}}
	}
	var items []*mygo.MenuItem
	if v.SafeMode {
		items = append(items,
			&mygo.MenuItem{Label: tr("安全模式：壁纸未加载"), Disabled: true},
			&mygo.MenuItem{Type: mygo.MenuItemSeparator},
			&mygo.MenuItem{Label: tr("退出安全模式"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.S.ctl.ExitSafeMode() }},
		)
	}
	if !v.SafeMode {
		header, status := traySummary(v)
		items = append(items, &mygo.MenuItem{Label: header, Disabled: true}, &mygo.MenuItem{Label: status, Disabled: true}, &mygo.MenuItem{Type: mygo.MenuItemSeparator})
	}
	if !v.SafeMode && (trayHasWallpaper(v) || v.PausedAll) {
		items = append(items,
			&mygo.MenuItem{Label: tr("暂停全部壁纸"), Type: mygo.MenuItemCheckbox, Checked: v.PausedAll, Click: func(*mygo.MenuItem, *mygo.Window) {
				sh.S.ctl.SetPausedAll(!sh.S.ctl.View().PausedAll)
			}},
			&mygo.MenuItem{Label: tr("切换到下一张壁纸"), Disabled: len(v.Library) < 2, Click: func(*mygo.MenuItem, *mygo.Window) { sh.S.ctl.Next() }},
		)
		if len(v.Displays) >= 2 {
			var displays []*mygo.MenuItem
			for _, d := range v.Displays {
				id := d.ID
				label := fmt.Sprintf(tr("暂停 显示器 %d"), d.Index)
				if d.Alias != "" || d.Friendly != "" {
					label = fmt.Sprintf(tr("暂停 显示器 %d · %s"), d.Index, d.Name)
				}
				displays = append(displays, &mygo.MenuItem{
					Label: label,
					Type:  mygo.MenuItemCheckbox, Checked: d.Options.Paused,
					Click: func(*mygo.MenuItem, *mygo.Window) {
						for _, current := range sh.S.ctl.View().Displays {
							if current.ID == id {
								options := current.Options
								options.Paused = !options.Paused
								sh.S.ctl.SetDisplayOptions(id, options)
								return
							}
						}
					},
				})
			}
			items = append(items, &mygo.MenuItem{Label: tr("显示器"), Submenu: displays})
		}
	}
	if !v.SafeMode && len(v.Library) > 0 {
		items = append(items, sh.wallpaperMenu(v))
	}
	if len(items) > 0 && items[len(items)-1].Type != mygo.MenuItemSeparator {
		items = append(items, &mygo.MenuItem{Type: mygo.MenuItemSeparator})
	}
	items = append(items,
		&mygo.MenuItem{Label: tr("打开壁纸库"), Accelerator: "CmdOrCtrl+L", Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgLibrary) }},
		&mygo.MenuItem{Label: tr("导入壁纸…"), Accelerator: "CmdOrCtrl+O", Disabled: v.Importing, Click: func(*mygo.MenuItem, *mygo.Window) {
			if sh.S.ctl.View().Importing {
				return
			}
			sh.OpenPage(pgLibrary)
			mygo.RunOnMain(func() {
				sh.S.importFiles()
				if sh.S.Invalidate != nil {
					sh.S.Invalidate()
				}
			})
		}},
		&mygo.MenuItem{Label: tr("显示器设置…"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgDisplays) }},
		&mygo.MenuItem{Type: mygo.MenuItemSeparator},
		&mygo.MenuItem{Label: tr("自动暂停"), Submenu: []*mygo.MenuItem{
			{Label: tr("被窗口遮挡时"), Type: mygo.MenuItemCheckbox, Checked: v.Config.Pause.OnCovered, Click: func(*mygo.MenuItem, *mygo.Window) {
				sh.S.ctl.UpdateConfig(func(c *config.Config) { c.Pause.OnCovered = !c.Pause.OnCovered })
			}},
			{Label: tr("其他应用全屏时"), Type: mygo.MenuItemCheckbox, Checked: v.Config.Pause.OnFullscreen, Click: func(*mygo.MenuItem, *mygo.Window) {
				sh.S.ctl.UpdateConfig(func(c *config.Config) { c.Pause.OnFullscreen = !c.Pause.OnFullscreen })
			}},
			{Type: mygo.MenuItemSeparator},
			{Label: tr("更多电源设置…"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgPause) }},
		}},
		&mygo.MenuItem{Label: tr("性能档位"), Submenu: []*mygo.MenuItem{profItem("smooth", "流畅"), profItem("balanced", "均衡"), profItem("saver", "省电（15 fps）")}},
		&mygo.MenuItem{Type: mygo.MenuItemSeparator},
		&mygo.MenuItem{Label: tr("设置…"), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgPerf) }},
		&mygo.MenuItem{Label: tr("版本与诊断"), Submenu: []*mygo.MenuItem{
			{Label: tr("版本管理"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgStartup) }},
			{Label: tr("关于与诊断"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgAbout) }},
		}},
		&mygo.MenuItem{Type: mygo.MenuItemSeparator},
		&mygo.MenuItem{Label: tr("退出"), Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) {
			if sh.OnQuit != nil {
				sh.OnQuit() // 由调用方负责最终 mygo.App.Quit()
				return
			}
			mygo.App.Quit()
		}},
	)
	return mygo.NewMenu(items)
}

// Start 建托盘；openWindow 为 true 时同时打开设置窗口（首次运行/第二实例）。
func (sh *Shell) Start(openWindow bool) error {
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.SetLanguages("zh-Hans", "en")
	sh.S.SetLanguage = mygo.App.SetLocale
	mygo.App.SetLocale(sh.S.view0().Config.UI.Lang)
	sh.S.Invalidate = func() {
		sh.mu.Lock()
		w := sh.win
		sh.mu.Unlock()
		if w != nil && !w.IsDestroyed() {
			w.Invalidate()
		}
	}
	sh.S.PickFolder = func() string {
		sh.mu.Lock()
		w := sh.win
		sh.mu.Unlock()
		ps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w, Title: translate(sh.S.view0().Config.UI.Lang, "选择壁纸或素材文件夹"), Directory: true})
		if err != nil {
			sh.S.pickerStatus = "文件选择失败：" + err.Error()
		}
		if len(ps) == 0 {
			return ""
		}
		return ps[0]
	}
	sh.S.PickFiles = func() []string {
		sh.mu.Lock()
		w := sh.win
		sh.mu.Unlock()
		ps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w, Title: translate(sh.S.view0().Config.UI.Lang, "导入壁纸"), Multiple: true,
			Filters: []mygo.FileFilter{{Name: translate(sh.S.view0().Config.UI.Lang, "视频 / 图片 / 壁纸包"), Extensions: []string{"mp4", "mov", "m4v", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp", "wpk", "zip"}}}})
		if err != nil {
			sh.S.pickerStatus = "文件选择失败：" + err.Error()
		}
		return ps
	}
	v := sh.S.view0()
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "wallpaperd", Icon: trayIcon(), IconIsTemplate: runtime.GOOS == "darwin", ToolTip: trayToolTip(v), Menu: sh.menu(v)})
	if err != nil {
		return err
	}
	sh.mu.Lock()
	sh.tray = t
	sh.updateTrayState(v)
	sh.mu.Unlock()
	if runtime.GOOS == "windows" {
		t.OnClick(func() { sh.OpenPage(pgLibrary) })
	}
	if openWindow {
		sh.Open()
	}
	return nil
}

// Changed 在编排器快照变化时调用（任意 goroutine）。
func (sh *Shell) Changed() {
	sh.S.Refresh()
	v := sh.S.view0()
	sh.mu.Lock()
	w := sh.win
	t := sh.tray
	var rebuild, tipChanged bool
	if t != nil {
		rebuild, tipChanged = sh.updateTrayState(v)
	}
	sh.mu.Unlock()
	if w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
	if t == nil || (!rebuild && !tipChanged) {
		return
	}
	mygo.RunOnMain(func() {
		if rebuild {
			t.SetMenu(sh.menu(v))
			if w != nil && !w.IsDestroyed() {
				w.SetTitle("")
			}
		}
		if tipChanged {
			t.SetToolTip(trayToolTip(v))
		}
	})
}

// OpenPage 打开设置窗口并切到某一页。
func (sh *Shell) OpenPage(p string) {
	mygo.RunOnMain(func() { sh.S.SetPage(p) })
	sh.Open()
}

// Open 显示设置窗口（已打开则聚焦）。
func (sh *Shell) Open() {
	mygo.RunOnMain(func() {
		sh.mu.Lock()
		defer sh.mu.Unlock()
		if sh.win != nil && !sh.win.IsDestroyed() {
			sh.win.Show()
			sh.win.Focus()
			return
		}
		sh.win = mygo.NewWindow(mygo.WindowOptions{Title: "", TitleBarStyle: mygo.TitleBarHidden, TitleBarHeight: 38, Width: 1120, Height: 740, MinWidth: 900, MinHeight: 600, Content: ui.View(sh.S.View)})
		// MyGo substitutes App.Name for an empty creation title; clear it for
		// desktop backends that fall back to a conventional title bar.
		sh.win.SetTitle("")
	})
}
