// Package desktop 定义 02 章 2.7 的平台宿主接口。各平台实现在 desktop/win、desktop/mac、desktop/linux（build tag 隔离）。
//
// 与 2.7 的差异（MVP 简化）：可见性探针（07 章 7.4 的信号源）由各平台 Host 自己实现，
// 通过 Events() 以 EventSignal 上报，而不是单独的 visibility.Probe 对象。
package desktop

import (
	"errors"
	"image"

	"wpapp/internal/visibility"
)

// DisplayID 是稳定 ID（Win：IDesktopWallpaper 的 monitor device path 哈希；mac：CGDisplay UUID；X11：输出名）。
type DisplayID string

// Display 描述一块显示器。Bounds 为物理像素、虚拟屏坐标。
type Display struct {
	ID      DisplayID
	Bounds  image.Rectangle
	Scale   float64
	Primary bool
	Name    string
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
