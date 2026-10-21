package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Shell 是托盘 + 设置窗口。必须在 mygo.App.WhenReady 里创建。
//
// MyGo 绕行（README「MyGo workaround」一节）：
//  1. 关闭最后一个窗口默认会退出应用：用 App.OnWindowAllClosed(空函数) 保持托盘常驻；设置窗口关闭后按需重建。
//  2. 托盘菜单项的 Checked 需要整份菜单重建（SetMenu）才会刷新：只在「暂停全部」状态变化时重建。
//  3. 托盘图标需要 PNG 字节：运行时用 image/png 画一个 32×32 图标，不提交二进制资源。
type Shell struct {
	S      *Settings
	OnQuit func()

	mu      sync.Mutex
	win     *mygo.Window
	tray    *mygo.Tray
	lastPau bool
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
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开设置", Click: func(*mygo.MenuItem, *mygo.Window) { sh.Open() }},
		{Label: "暂停全部壁纸", Type: mygo.MenuItemCheckbox, Checked: paused, Click: func(it *mygo.MenuItem, _ *mygo.Window) {
			sh.S.ctl.SetPausedAll(!sh.S.view0().PausedAll)
		}},
		{Type: mygo.MenuItemSeparator},
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) {
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
	sh.S.PickFiles = func() []string {
		ps, _ := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "导入壁纸", Multiple: true,
			Filters: []mygo.FileFilter{{Name: "视频 / 图片 / 壁纸包", Extensions: []string{"mp4", "mov", "m4v", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp", "wpk", "zip"}}}})
		return ps
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "wallpaperd", Icon: Icon(), ToolTip: "动态壁纸", Menu: sh.menu(false)})
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
	p := sh.S.view0().PausedAll
	sh.mu.Lock()
	w := sh.win
	rebuild := p != sh.lastPau && sh.tray != nil
	sh.lastPau = p
	sh.mu.Unlock()
	if w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
	if rebuild {
		mygo.RunOnMain(func() { sh.tray.SetMenu(sh.menu(p)) })
	}
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
		sh.win = mygo.NewWindow(mygo.WindowOptions{Title: "动态壁纸 设置", Width: 680, Height: 560, Content: ui.View(sh.S.View)})
	})
}
