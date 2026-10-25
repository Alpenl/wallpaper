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

// Icon 生成托盘图标。
func Icon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			in := x >= 3 && x < 29 && y >= 6 && y < 26
			switch {
			case in && y > 18-(x/4)%3:
				img.Set(x, y, color.NRGBA{0x2b, 0x8a, 0xd8, 0xff})
			case in:
				img.Set(x, y, color.NRGBA{0xe8, 0xf1, 0xfb, 0xff})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

type trayDisplayState struct {
	ID      string
	Index   int
	Name    string
	HasName bool
	Paused  bool
}

type trayMenuState struct {
	PausedAll bool
	Profile   string
	FewItems  bool
	Lang      string
	SafeMode  bool
	Displays  []trayDisplayState
}

func trayState(v app.View) trayMenuState {
	s := trayMenuState{PausedAll: v.PausedAll, Profile: v.Config.Perf.Profile,
		FewItems: len(v.Library) < 2, Lang: v.Config.UI.Lang, SafeMode: v.SafeMode}
	for _, d := range v.Displays {
		s.Displays = append(s.Displays, trayDisplayState{d.ID, d.Index, d.Name, d.Alias != "" || d.Friendly != "", d.Options.Paused})
	}
	return s
}

func (s trayMenuState) equal(other trayMenuState) bool {
	return s.PausedAll == other.PausedAll && s.Profile == other.Profile &&
		s.FewItems == other.FewItems && s.Lang == other.Lang && s.SafeMode == other.SafeMode &&
		slices.Equal(s.Displays, other.Displays)
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
	items = append(items, &mygo.MenuItem{Label: tr("打开壁纸库"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgLibrary) }})
	if !v.SafeMode {
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
	items = append(items,
		&mygo.MenuItem{Label: tr("性能档位"), Submenu: []*mygo.MenuItem{profItem("smooth", "流畅"), profItem("balanced", "均衡"), profItem("saver", "省电（15 fps）")}},
		&mygo.MenuItem{Type: mygo.MenuItemSeparator},
		&mygo.MenuItem{Label: tr("设置…"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgPerf) }},
		&mygo.MenuItem{Label: tr("退出"), Click: func(*mygo.MenuItem, *mygo.Window) {
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
			Filters: []mygo.FileFilter{{Name: translate(sh.S.view0().Config.UI.Lang, "视频 / 图片 / Shader / 壁纸包"), Extensions: []string{"mp4", "mov", "m4v", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp", "wpk", "zip", "glsl", "frag"}}}})
		if err != nil {
			sh.S.pickerStatus = "文件选择失败：" + err.Error()
		}
		return ps
	}
	v := sh.S.view0()
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "wallpaperd", Icon: Icon(), ToolTip: trayToolTip(v), Menu: sh.menu(v)})
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
	if v.NetworkConsent != nil {
		sh.Open()
	}
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
