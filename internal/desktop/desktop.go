// Package desktop 定义 02 章 2.7 的平台宿主接口。各平台实现在 desktop/win、desktop/mac、desktop/linux（build tag 隔离）。
//
// 与 2.7 的差异（MVP 简化）：可见性探针（07 章 7.4 的信号源）由各平台 Host 自己实现，
// 通过 Events() 以 EventSignal 上报，而不是单独的 visibility.Probe 对象。
package desktop

import (
	"errors"
	"fmt"
	"image"
	"sort"
	"strings"

	"wpapp/internal/visibility"
)

// DisplayID 是稳定 ID，用来持久化每屏设置（壁纸、别名）。
//   - Win：monitor device interface path（DisplayConfig 的 monitorDevicePath /
//     EnumDisplayDevices(EDD_GET_DEVICE_INTERFACE_NAME)，包含 EDID 厂商/型号和实例号）的哈希，
//     不用 GDI 名 \\.\DISPLAYn（它随驱动和插拔顺序变化）。
//   - mac：CGDisplayCreateUUIDFromDisplayID 的 UUID 哈希。
//   - X11：固定 "x11-0"（Linux 只支持单屏）。
type DisplayID string

// Display 描述一块显示器。Bounds 为物理像素、虚拟屏坐标。
type Display struct {
	ID      DisplayID
	Bounds  image.Rectangle
	Scale   float64
	Primary bool
	// Name 是系统给的友好名（Win：monitorFriendlyDeviceName / 显示器 DeviceString；mac：NSScreen.localizedName）。
	// 拿不到或只是“通用即插即用监视器”这类泛称时为空，UI 显示“显示器 N”。
	Name string
	// Device 是系统内部名（Win：\\.\DISPLAYn），只用于日志和诊断，不显示给用户、不做持久化键。
	Device string
	// LegacyIDs 是旧版本用过的 ID（迁移 assignments.json 用）。
	LegacyIDs []DisplayID
}

// genericNames 是系统在拿不到 EDID 名字时给的泛称，不当作友好名。
var genericNames = []string{"generic pnp monitor", "通用即插即用监视器", "generic non-pnp monitor", "通用非即插即用监视器", "default monitor", "默认监视器"}

// CleanName 去掉空白，泛称返回空串。
func CleanName(n string) string {
	n = strings.TrimSpace(n)
	l := strings.ToLower(n)
	for _, g := range genericNames {
		if l == g {
			return ""
		}
	}
	return n
}

// Dedupe 保证 ID 唯一：两块同型号且没有序列号的屏可能得到相同 ID，后出现的加 "#2"、"#3"。
// 调用前应先按 SortByLayout 排序，这样编号稳定。
func Dedupe(ds []Display) {
	seen := map[DisplayID]int{}
	for i := range ds {
		id := ds[i].ID
		seen[id]++
		if n := seen[id]; n > 1 {
			ds[i].ID = DisplayID(fmt.Sprintf("%s#%d", id, n))
		}
	}
}

// SortByLayout 按物理排列排序：从左到右，同一列从上到下（左边缘相同时比上边缘）。
// 这个顺序就是 UI 里的编号 1、2、3…，与系统内部编号（DISPLAYn）无关。
func SortByLayout(ds []Display) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i].Bounds.Min, ds[j].Bounds.Min
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
}

// Surface 是渲染器画画的原生表面。
type Surface interface {
	Native() uintptr // Win：子 HWND；mac：NSView*；X11：XID
	Bounds() image.Rectangle
	SetVisible(bool) error // L4/Failed 时隐藏，露出系统壁纸（首帧截图）
	Close() error
}

// Caps 是平台能力。
type Caps struct{ Video, Web, Shader, PerDisplay, ExternalSurfaceOnly bool }

// EventKind 是事件类型。
type EventKind int

const (
	EventDisplaysChanged EventKind = iota // 显示器增删或几何变化
	EventShellRestarted                   // Explorer 重启/挂载点失效；所有 Surface 已失效，需重建
	EventSignal                           // 可见性/电源信号（07 章 7.2 的原因）
	EventBattery                          // 电池电量（Percent，-1=未知）
)

// Event 是宿主事件。Display 为空表示全局。
type Event struct {
	Kind    EventKind
	Display DisplayID
	Reason  visibility.Reason
	Active  bool
	Percent int
	Source  string
}

// ErrDetached 表示表面已脱离桌面层级。
var ErrDetached = errors.New("desktop: surface detached")

// Host 是平台宿主。
type Host interface {
	Start() error
	Displays() []Display
	CreateSurface(id DisplayID) (Surface, error)
	Health() error
	Events() <-chan Event
	Caps() Caps
	SetSystemWallpaper(id DisplayID, imagePath string) error
	Close() error
}
