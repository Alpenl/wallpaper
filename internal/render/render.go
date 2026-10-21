// Package render 定义 02 章 2.7 的渲染器接口。实现：render/mpv（Win/Linux，mpv 子进程 + JSON IPC）、
// render/mf（Win，Media Foundation 子进程 + IPC v1）、render/avf（mac，AVPlayerLayer + AVPlayerLooper，进程内）、
// render/image（静态图：不起渲染器，直接用系统壁纸）。
package render

import (
	"errors"

	"wpapp/internal/desktop"
	"wpapp/internal/visibility"
)

// Item 是要播放的壁纸。
type Item struct {
	ID    string
	Path  string // 入口文件绝对路径（已校验在包目录内）
	Kind  string // video | image
	Start float64
}

// Stats 是渲染统计。
type Stats struct {
	Backend string  `json:"backend"`
	HWDec   string  `json:"hwdec"`
	Dropped uint64  `json:"dropped"`
	Pos     float64 `json:"pos"`
	Loops   int64   `json:"loops"`
	LastErr string  `json:"lastErr,omitempty"`
}

// Renderer 是一块显示器上的渲染器。除 Stats/Done 外的方法可能阻塞（IPC），调用方不应在 UI 线程上调用。
type Renderer interface {
	Load(it Item, s desktop.Surface, startPaused bool) error
	SetLevel(l visibility.Level) error // 幂等；L4 由调用方直接 Close
	SetFPSCap(fps int) error           // 0 = 不限
	Snapshot(path string) error
	Stats() Stats
	Done() <-chan error // 意外退出/卡住时发送一次
	Close() error       // 有意结束，不触发 Done
}

// Factory 按壁纸类型和后端名创建渲染器。
type Factory func(kind, backend string) (Renderer, error)

// ErrUnsupported 表示该渲染器不支持此操作。
var ErrUnsupported = errors.New("render: unsupported")
