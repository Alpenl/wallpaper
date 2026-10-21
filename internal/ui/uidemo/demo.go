// Package uidemo 是设置窗口的演示控制器：内存里的壁纸库和显示器，命令会改变视图。
// 用于 UI 测试和无窗口截图（cmd/uishots），不连接真实的编排器。
package uidemo

import (
	"sync"
	"time"

	"wpapp/internal/app"
	"wpapp/internal/config"
)

// Ctl 实现 ui.Controller。
type Ctl struct {
	mu       sync.Mutex
	V        app.View
	Imported []string
	Removed  []string
	Transc   []string
	Nexts    int
	ExitSafe bool
}

func (f *Ctl) View() app.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.V
	v.Library = append([]config.Item(nil), f.V.Library...)
	v.Displays = append([]app.DisplayView(nil), f.V.Displays...)
	return v
}

func (f *Ctl) SetPausedAll(b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.V.PausedAll = b
	for i := range f.V.Displays {
		if b {
			f.V.Displays[i].Level, f.V.Displays[i].Reasons = "L1", "User"
		} else {
			f.V.Displays[i].Level, f.V.Displays[i].Reasons = "L0", ""
		}
	}
}

func (f *Ctl) Assign(d, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d == "" {
		f.V.Default = id
	}
	for i := range f.V.Displays {
		if d == "" || f.V.Displays[i].ID == d {
			if id == "" && d != "" {
				f.V.Displays[i].ItemID = f.V.Default
			} else {
				f.V.Displays[i].ItemID = id
			}
		}
	}
}

func (f *Ctl) Import(p string) {
	f.mu.Lock()
	f.Imported = append(f.Imported, p)
	f.mu.Unlock()
}

func (f *Ctl) Remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Removed = append(f.Removed, id)
	out := f.V.Library[:0]
	for _, it := range f.V.Library {
		if it.ID != id {
			out = append(out, it)
		}
	}
	f.V.Library = out
}

func (f *Ctl) UpdateConfig(fn func(*config.Config)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.V.Config)
	f.V.Config.Normalize()
}

func (f *Ctl) ExitSafeMode() { f.mu.Lock(); f.ExitSafe = true; f.V.SafeMode = false; f.mu.Unlock() }
func (f *Ctl) Next()         { f.mu.Lock(); f.Nexts++; f.mu.Unlock() }
func (f *Ctl) Transcode(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Transc = append(f.Transc, id)
	if f.V.Transcoding == nil {
		f.V.Transcoding = map[string]string{}
	}
	f.V.Transcoding[id] = "正在转码…"
}

// New 返回一个有 6 张壁纸、2 块显示器的演示库。thumbs 是 id → 缩略图 PNG 路径（可为空）。
func New(thumbs map[string]string) *Ctl {
	t0 := time.Date(2026, 10, 20, 21, 0, 0, 0, time.Local)
	mk := func(i int, id, title, typ, origin, codec, tc, note string) config.Item {
		return config.Item{ID: id, Title: title, Type: typ, Origin: origin, Codec: codec, Transcode: tc, Note: note,
			Entry: "content/" + id + ".mp4", Dir: "/demo/library/" + id, Snapshot: thumbs[id], AddedAt: t0.Add(-time.Duration(i) * 7 * time.Hour)}
	}
	lib := []config.Item{
		mk(0, "aurora", "极光之夜", "video", "file", "hevc", "none", ""),
		mk(1, "ocean", "海浪 4K", "video", "we", "h264", "recommended", "分辨率 5120x2880 超过 4K；帧率 60 超过 30 fps 目标"),
		mk(2, "city", "城市霓虹", "video", "lively", "h264", "done", "已转码为 H.264/HEVC、30 fps、无音轨"),
		mk(3, "forest", "雨林晨雾", "video", "file", "vp9", "recommended", "webm/vp9 可能走软解，耗电较高"),
		mk(4, "pixel", "像素小镇", "video", "file", "gif", "none", "GIF 已按视频播放"),
		mk(5, "mount", "雪山", "image", "file", "", "none", ""),
	}
	cfg := config.Defaults()
	cfg.Startup.Asked = true
	return &Ctl{V: app.View{
		Config: cfg, Library: lib, Default: "aurora", HasEncoder: true,
		LastImport: "已导入「极光之夜」",
		Power:      app.Power{OnBattery: false, Percent: 86},
		Displays: []app.DisplayView{
			{ID: "d1", Name: "DELL U2723QE", W: 3840, H: 2160, X: 0, Y: 0, Primary: true, ItemID: "aurora", Level: "L0", State: "active", Backend: "mpv", HWDec: "d3d11va"},
			{ID: "d2", Name: "内置显示器", W: 2560, H: 1600, X: 3840, Y: 560, ItemID: "city", Level: "L1", State: "active", Backend: "mpv", HWDec: "d3d11va", Reasons: "Fullscreen"},
		},
		Transcoding: map[string]string{},
	}}
}
