package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"wpapp/internal/app"
	"wpapp/internal/config"
)

// Keep the application icon separate from the macOS monochrome status symbol.
func trayIcon() []byte {
	if runtime.GOOS != "darwin" {
		return Icon()
	}
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	triangle := func(x, y, ax, ay, bx, by, cx, cy float64) bool {
		a := (x-bx)*(ay-by) - (ax-bx)*(y-by)
		b := (x-cx)*(by-cy) - (bx-cx)*(y-cy)
		c := (x-ax)*(cy-ay) - (cx-ax)*(y-ay)
		return !(a < 0 || b < 0 || c < 0) || !(a > 0 || b > 0 || c > 0)
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			coverage := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px, py := float64(x)+(float64(sx)+0.5)/4, float64(y)+(float64(sy)+0.5)/4
					dx, dy := px-min(max(px, 4), 28), py-min(max(py, 7), 25)
					outer := dx*dx+dy*dy <= 4
					inner := px >= 5 && px <= 27 && py >= 8 && py <= 24
					mountains := triangle(px, py, 7, 22, 13, 13, 20, 22) || triangle(px, py, 17, 22, 22, 15, 26, 22)
					if (outer && !inner) || mountains {
						coverage++
					}
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{A: uint8(coverage * 255 / 16)})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

type trayWallpaperState struct {
	ID, Title string
	Checked   bool
}

// Menu labels stay single-line and bounded even for user-supplied titles.
func trayLabel(s string) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > 32 {
		return string(r[:31]) + "…"
	}
	return string(r)
}

func trayAssigned(v app.View) []config.Item {
	var out []config.Item
	seen := map[string]bool{}
	for _, d := range v.Displays {
		for _, it := range v.Library {
			if d.ItemID == it.ID && !seen[it.ID] {
				seen[it.ID] = true
				out = append(out, it)
			}
		}
	}
	return out
}

func trayHasWallpaper(v app.View) bool { return len(trayAssigned(v)) > 0 }

func trayRecent(v app.View) []trayWallpaperState {
	items := slices.Clone(v.Library)
	slices.SortStableFunc(items, func(a, b config.Item) int { return b.AddedAt.Compare(a.AddedAt) })
	if len(items) > 8 {
		items = items[:8]
	}
	var out []trayWallpaperState
	for _, it := range items {
		checked := len(v.Displays) > 0
		for _, d := range v.Displays {
			checked = checked && d.ItemID == it.ID
		}
		title := trayLabel(it.Title)
		if title == "" {
			title = translate(v.Config.UI.Lang, "未命名壁纸")
		}
		out = append(out, trayWallpaperState{it.ID, title, checked})
	}
	return out
}

func traySummary(v app.View) (string, string) {
	tr := func(s string) string { return translate(v.Config.UI.Lang, s) }
	assigned := trayAssigned(v)
	if len(assigned) == 0 {
		if v.PausedAll {
			return tr("未设置壁纸"), tr("已手动暂停全部")
		}
		return tr("未设置壁纸"), tr("导入视频或图片开始使用")
	}
	header := trayLabel(assigned[0].Title)
	if header == "" {
		header = tr("未命名壁纸")
	}
	if len(assigned) > 1 {
		header = fmt.Sprintf(tr("正在使用 %d 张壁纸"), len(assigned))
	}
	playing, paused := 0, 0
	for _, d := range v.Displays {
		if d.ItemID == "" {
			continue
		}
		if d.State == "failed" {
			return header, tr("播放失败，请打开壁纸库")
		}
		if d.State == "recovering" {
			return header, tr("正在恢复…")
		}
		if d.Level == "L0" && d.State == "active" {
			playing++
		} else {
			paused++
		}
	}
	if v.PausedAll {
		return header, tr("已手动暂停全部")
	}
	if playing == 0 && paused > 0 {
		for _, reason := range []string{"Locked", "Suspend", "DisplayOff", "ThermalCritical", "BatterySaver", "LowBattery", "Fullscreen", "Covered", "FocusedApp", "Remote", "Battery", "User"} {
			for _, d := range v.Displays {
				if d.ItemID != "" && slices.Contains(strings.Split(d.Reasons, "|"), reason) {
					if reason == "Battery" && v.Config.Pause.BatteryPolicy == "ignore" {
						continue
					}
					return header, tr("已暂停") + " · " + tr(reasonNames[reason])
				}
			}
		}
		return header, tr("已暂停")
	}
	return header, fmt.Sprintf(tr("%d 块屏播放 · %d 块屏暂停"), playing, paused)
}

func (sh *Shell) wallpaperMenu(v app.View) *mygo.MenuItem {
	tr := func(s string) string { return translate(v.Config.UI.Lang, s) }
	var items []*mygo.MenuItem
	if len(v.Displays) > 1 {
		items = append(items, &mygo.MenuItem{Label: tr("应用到所有显示器"), Disabled: true})
	}
	for _, it := range trayRecent(v) {
		id := it.ID
		items = append(items, &mygo.MenuItem{Label: it.Title, Type: mygo.MenuItemCheckbox, Checked: it.Checked, Click: func(*mygo.MenuItem, *mygo.Window) {
			// A menu may outlive library deletion or entry into safe mode.
			current := sh.S.ctl.View()
			if current.SafeMode {
				return
			}
			for _, latest := range current.Library {
				if latest.ID == id {
					sh.S.ctl.AssignAll(id)
					return
				}
			}
		}})
	}
	items = append(items, &mygo.MenuItem{Type: mygo.MenuItemSeparator}, &mygo.MenuItem{Label: tr("浏览全部壁纸…"), Click: func(*mygo.MenuItem, *mygo.Window) { sh.OpenPage(pgLibrary) }})
	return &mygo.MenuItem{Label: tr("切换壁纸"), Submenu: items}
}
