package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"wpapp/internal/config"
)

// Shell 是托盘 + 设置窗口。必须在 mygo.App.WhenReady 里创建。
//
// MyGo 绕行（README「MyGo workaround」一节）：
//  1. 关闭最后一个窗口默认会退出应用：用 App.OnWindowAllClosed(空函数) 保持托盘常驻；设置窗口关闭后按需重建。
//  2. 托盘菜单项的 Checked 需要整份菜单重建（SetMenu）才会刷新：只在「暂停全部」/性能档位/能否切换变化时重建。
//  3. 托盘图标需要 PNG 字节：运行时用 image/png 画一个 32×32 图标，不提交二进制资源。
type Shell struct {
	S      *Settings
	OnQuit func()

	mu      sync.Mutex
	win     *mygo.Window
	tray    *mygo.Tray
	lastKey string
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

func (sh *Shell) menu(paused bool) *mygo.Menu {
	v := sh.S.view0()
	prof := v.Config.Perf.Profile
	tr := func(label string) string { return translate(v.Config.UI.Lang, label) }
	profItem := func(id, label string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: tr(label), Type: mygo.MenuItemRadio, Checked: prof == id, Click: func(*mygo.MenuItem, *mygo.Window) {
			sh.S.ctl.UpdateConfig(func(c *config.Config) { c.Perf.Profile = id })
		}}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: tr("打开壁纸库"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgLibrary) }},
		{Label: tr("暂停全部壁纸"), Type: mygo.MenuItemCheckbox, Checked: paused, Click: func(it *mygo.MenuItem, _ *mygo.Window) {
			sh.S.ctl.SetPausedAll(!sh.S.view0().PausedAll)
		}},
		{Label: tr("切换到下一张壁纸"), Disabled: len(v.Library) < 2, Click: func(*mygo.MenuItem, *mygo.Window) { sh.S.ctl.Next() }},
		{Label: tr("性能档位"), Submenu: []*mygo.MenuItem{profItem("smooth", "流畅"), profItem("balanced", "均衡"), profItem("saver", "省电（15 fps）")}},
		{Type: mygo.MenuItemSeparator},
		{Label: tr("设置…"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgPerf) }},
		{Label: tr("退出"), Click: func(*mygo.MenuItem, *mygo.Window) {
			if sh.OnQuit != nil {
				sh.OnQuit() // 由调用方负责最终 mygo.App.Quit()
				return
			}
			mygo.App.Quit()
		}},
	})
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
		ps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: translate(sh.S.view0().Config.UI.Lang, "选择壁纸或素材文件夹"), Directory: true})
		if err != nil {
			sh.S.pickerStatus = "文件选择失败：" + err.Error()
		}
		if len(ps) == 0 {
			return ""
		}
		return ps[0]
	}
	sh.S.PickFiles = func() []string {
		ps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: translate(sh.S.view0().Config.UI.Lang, "导入壁纸"), Multiple: true,
			Filters: []mygo.FileFilter{{Name: translate(sh.S.view0().Config.UI.Lang, "视频 / 图片 / Shader / 壁纸包"), Extensions: []string{"mp4", "mov", "m4v", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp", "wpk", "zip", "glsl", "frag"}}}})
		if err != nil {
			sh.S.pickerStatus = "文件选择失败：" + err.Error()
		}
		return ps
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "wallpaperd", Icon: Icon(), ToolTip: translate(sh.S.view0().Config.UI.Lang, "动态壁纸"), Menu: sh.menu(false)})
	if err != nil {
		return err
	}
	sh.tray = t
	if openWindow {
		sh.Open()
	}
	return nil
}

// Changed 在编排器快照变化时调用（任意 goroutine）。
func (sh *Shell) Changed() {
	sh.S.Refresh()
	if sh.S.view0().NetworkConsent != nil {
		sh.Open()
	}
	v := sh.S.view0()
	p := v.PausedAll
	key := fmt.Sprint(p, v.Config.Perf.Profile, len(v.Library) < 2, v.Config.UI.Lang)
	sh.mu.Lock()
	w := sh.win
	rebuild := key != sh.lastKey && sh.tray != nil
	sh.lastKey = key
	sh.mu.Unlock()
	if w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
	if rebuild {
		mygo.RunOnMain(func() {
			sh.tray.SetMenu(sh.menu(p))
			sh.tray.SetToolTip(translate(v.Config.UI.Lang, "动态壁纸"))
			if w != nil && !w.IsDestroyed() {
				w.SetTitle("")
			}
		})
	}
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
