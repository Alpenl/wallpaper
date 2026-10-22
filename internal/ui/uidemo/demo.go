// Package uidemo 是设置窗口的演示控制器：内存里的壁纸库和显示器，命令会改变视图。
// 用于 UI 测试和无窗口截图（cmd/uishots），不连接真实的编排器。
package uidemo

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"encoding/json"
	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/transcode"
	"wpapp/internal/wpk"
)

// Ctl 实现 ui.Controller。
type Ctl struct {
	GOOS               string // Target platform for headless previews and tests; empty uses the host.
	SignalAnswers      []bool
	NetworkAnswers     []bool
	mu                 sync.Mutex
	V                  app.View
	Imported           []string
	ImportTo           []string
	Assigns            []string // "显示器=壁纸"，"*" 表示所有显示器
	Removed            []string
	Transc             []string
	Nexts              int
	ExitSafe           bool
	independent        []app.DisplayView
	independentDefault string
}

func (f *Ctl) View() app.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.V
	v.Config = f.V.Config.Clone()
	v.Library = append([]config.Item(nil), f.V.Library...)
	v.Displays = append([]app.DisplayView(nil), f.V.Displays...)
	// 与 app 一致：显示器覆盖 > 壁纸组属性 > 默认值。
	for i := range v.Displays {
		var props config.ItemProps
		for _, it := range v.Library {
			if it.ID == v.Displays[i].ItemID {
				props = it.Props
			}
		}
		v.Displays[i].Effective = config.ResolvePlayback(props, v.Displays[i].Options, v.Config.Render.Muted)
	}
	return v
}

func (f *Ctl) SetItemProps(id string, props config.ItemProps) {
	f.mu.Lock()
	defer f.mu.Unlock()
	props.Normalize()
	for i := range f.V.Library {
		if f.V.Library[i].ID == id {
			f.V.Library[i].Props = props
		}
	}
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

// Assign 与 app.App 语义一致：d 为空只改默认壁纸（不动已单独设置的屏）；id 为空表示改回跟随默认。
func (f *Ctl) Assign(d, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.V.Layout == "span" {
		f.V.Default = id
		for i := range f.V.Displays {
			f.V.Displays[i].ItemID = id
		}
		f.Assigns = append(f.Assigns, "span="+id)
		return
	}
	if d == "" {
		f.V.Default = id
	}
	for i := range f.V.Displays {
		x := &f.V.Displays[i]
		switch {
		case d == "" && !x.Custom:
			x.ItemID = id
		case d != "" && x.ID == d:
			x.Custom = id != ""
			x.ItemID = id
			if id == "" {
				x.ItemID = f.V.Default
			}
		}
	}
	f.Assigns = append(f.Assigns, d+"="+id)
}

func (f *Ctl) AssignAll(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.V.Default = id
	for i := range f.V.Displays {
		f.V.Displays[i].ItemID, f.V.Displays[i].Custom = id, false
	}
	f.Assigns = append(f.Assigns, "*="+id)
}

func (f *Ctl) SetAlias(d, alias string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var a config.Assignments
	alias = a.SetAlias(d, alias)
	for i := range f.V.Displays {
		x := &f.V.Displays[i]
		if x.ID == d {
			x.Alias = alias
			x.Name = app.DisplayName(alias, x.Friendly, x.Index)
		}
	}
}

func (f *Ctl) Import(p, applyTo string) {
	f.mu.Lock()
	f.Imported = append(f.Imported, p)
	f.ImportTo = append(f.ImportTo, applyTo)
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
	goos := f.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	f.V.Config.NormalizeForOS(goos)
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

// New 返回一个有 8 张壁纸、2 块显示器的演示库。thumbs 是 id → 缩略图 PNG 路径（可为空）。
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
	lib = append(lib, mk(6, "web-orbit", "轨道网页", "web", "native", "", "none", ""), mk(7, "shader-waves", "光波 Shader", "shader", "native", "", "none", ""))
	lib[6].Entry = "content/index.html"
	lib[6].Web = &wpk.Web{FPSCap: 30}
	lib[7].Entry = "content/main.frag"
	lib[7].PropertyDefs = []wpk.Prop{{Key: "tint", Type: "color", Default: json.RawMessage(`"#6699cc"`), Label: wpk.I18n{"en": "Tint", "zh": "颜色"}}, {Key: "enabled", Type: "bool", Default: json.RawMessage(`true`), Label: wpk.I18n{"en": "Enabled", "zh": "启用"}}}
	lib[1].Props = config.ItemProps{Audio: "sound", Volume: 60, Speed: 0.75}
	for i := 8; i < 32; i++ {
		item := lib[(i-8)%6]
		item.ID = fmt.Sprintf("sample-%02d", i)
		item.Title = fmt.Sprintf("Landscape %02d", i)
		item.Transcode = "none"
		lib = append(lib, item)
	}
	cfg := config.Defaults()
	cfg.Startup.Asked = true
	return &Ctl{V: app.View{
		Capabilities: desktop.Caps{Spanning: true, Video: true, PerDisplay: true, ForegroundApp: true}, Config: cfg, Library: lib, Default: "aurora", HasEncoder: true,
		Encoder:    transcode.Capabilities{Available: true, Strict: true, GIF: true, TargetSize: true, FPS30: true, RemovesAudio: true, Name: "Demo H.264"},
		LastImport: "已导入「极光之夜」",
		Power:      app.Power{OnBattery: false, Percent: 86},
		Displays: []app.DisplayView{
			{Playback: render.Capabilities{Fit: true, Speed: true, Audio: true, Scale: true}, ID: "d1", Index: 1, Name: "DELL U2723QE", Friendly: "DELL U2723QE", Custom: true, W: 3840, H: 2160, X: 0, Y: 0, Primary: true, ItemID: "aurora", Level: "L0", State: "active", Backend: "mpv", HWDec: "d3d11va"},
			{Options: config.DisplayOptions{Audio: "mute"}, Playback: render.Capabilities{Fit: true, Speed: true, Audio: true, Scale: true}, ID: "d2", Index: 2, Name: "显示器 2", Custom: true, W: 2560, H: 1600, X: 3840, Y: 560, ItemID: "city", Level: "L1", State: "active", Backend: "mpv", HWDec: "d3d11va", Reasons: "Fullscreen"},
		},
		Transcoding: map[string]string{},
	}}
}

func (f *Ctl) SetDisplayOptions(id string, options config.DisplayOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.V.Displays {
		if f.V.Layout == "span" {
			f.V.Displays[i].Options.Fit, f.V.Displays[i].Options.Speed = options.Fit, options.Speed
		}
		if f.V.Displays[i].ID == id {
			f.V.Displays[i].Options = options
			if options.Paused {
				f.V.Displays[i].Level, f.V.Displays[i].Reasons = "L1", "User"
			} else {
				f.V.Displays[i].Level, f.V.Displays[i].Reasons = "L0", ""
			}
		}
	}
}
func (f *Ctl) RefreshStorage() {}
func (f *Ctl) ClearCache()     {}

// SetLayout mirrors the production control's retained independent assignments.
func (f *Ctl) SetLayout(layout, itemID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if layout == "span" && f.V.Capabilities.Spanning {
		if f.V.Layout != "span" {
			f.independent = append([]app.DisplayView(nil), f.V.Displays...)
			f.independentDefault = f.V.Default
		}
		f.V.Layout = "span"
		f.V.Default = itemID
		for i := range f.V.Displays {
			f.V.Displays[i].ItemID = itemID
			f.V.Displays[i].Custom = false
		}
	} else if layout == "independent" {
		if f.independent != nil {
			for i := range f.V.Displays {
				for _, old := range f.independent {
					if old.ID == f.V.Displays[i].ID {
						f.V.Displays[i].ItemID, f.V.Displays[i].Custom = old.ItemID, old.Custom
						f.V.Displays[i].Options.Fit, f.V.Displays[i].Options.Speed = old.Options.Fit, old.Options.Speed
					}
				}
			}
			f.V.Default = f.independentDefault
		}
		f.V.Layout = "independent"
	}
}

func (f *Ctl) CancelImports() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.V.Importing, f.V.ImportQueued = false, 0
	f.V.LastImport = "导入已取消；已完成的项目保留在壁纸库"
}

func (f *Ctl) OptimizeDisplays(id string) {}
func (f *Ctl) CancelRenditions(id string) {}

func (f *Ctl) ResolveNetworkConsent(allow bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.NetworkAnswers = append(f.NetworkAnswers, allow)
	f.V.NetworkConsent = nil
}
func (f *Ctl) RevokeNetwork(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.V.Library {
		if f.V.Library[i].ID == id {
			f.V.Library[i].NetworkGranted = false
		}
	}
}

func (f *Ctl) ResolveSignalConsent(allow bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SignalAnswers = append(f.SignalAnswers, allow)
	f.V.SignalConsent = nil
}
func (f *Ctl) RevokeSignal(id, permission string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.V.Library {
		it := &f.V.Library[i]
		if it.ID == id {
			if permission == "audio" {
				it.AudioGranted = false
			}
			if permission == "input" {
				it.InputGranted = false
			}
		}
	}
}
