// Package render 定义 02 章 2.7 的渲染器接口。实现：render/mpv（Win/Linux，mpv 子进程 + JSON IPC）、
// render/mf（Win，Media Foundation 子进程 + IPC v1）、render/avf（mac，AVPlayerLayer + AVPlayerLooper，进程内）、
// render/image（静态图：不起渲染器，直接用系统壁纸）。
package render

import (
	"errors"
	"fmt"
	"math"

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
	Pos     float64 `json:"pos"` // last known position; initialized from a requested resume offset until sampled
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

// PlaybackOptions are applied before Load. Scale is the video-frame resolution
// multiplier, not the surface size or a guaranteed power saving. A software
// downscale may require copying hardware-decoded frames into system memory.
type PlaybackOptions struct {
	Fit   string  `json:"fit"` // fill | fit | stretch
	Speed float64 `json:"speed"`
	Muted bool    `json:"muted"`
	// Volume is 1–100 (percent); 0 means unspecified (100). Silence is Muted.
	Volume int     `json:"volume,omitempty"`
	Scale  float64 `json:"scale"`
}

func DefaultPlaybackOptions() PlaybackOptions {
	return PlaybackOptions{Fit: "fill", Speed: 1, Muted: true, Volume: 100, Scale: 1}
}

// NormalizePlaybackOptions fills unspecified numeric/string fields and rejects
// invalid input. Muted is always explicit: false means audio is enabled.
func NormalizePlaybackOptions(o PlaybackOptions) (PlaybackOptions, error) {
	if o.Fit == "" {
		o.Fit = "fill"
	}
	if o.Speed == 0 {
		o.Speed = 1
	}
	if o.Scale == 0 {
		o.Scale = 1
	}
	if o.Volume == 0 {
		o.Volume = 100
	}
	if o.Volume < 1 || o.Volume > 100 {
		return o, fmt.Errorf("render: volume must be between 1 and 100")
	}
	if o.Fit != "fill" && o.Fit != "fit" && o.Fit != "stretch" {
		return o, fmt.Errorf("render: invalid fit %q", o.Fit)
	}
	if math.IsNaN(o.Speed) || math.IsInf(o.Speed, 0) || o.Speed < .25 || o.Speed > 4 {
		return o, fmt.Errorf("render: speed must be between 0.25 and 4")
	}
	if math.IsNaN(o.Scale) || math.IsInf(o.Scale, 0) || o.Scale < .25 || o.Scale > 1 {
		return o, fmt.Errorf("render: scale must be between 0.25 and 1")
	}
	return o, nil
}

// Configurable is optional. Configure must be called before Load, not while the
// renderer is active. Callers should reset unsupported fields to their defaults.
type Configurable interface {
	Configure(PlaybackOptions) error
}

// PlaybackUpdater optionally applies supported settings without unloading the
// current media. ErrUnsupported requests a restart fallback; other errors must
// be surfaced to the caller rather than being mistaken for unsupported input.
// Like Configure, UpdatePlayback may block and belongs on the renderer worker.
type PlaybackUpdater interface {
	UpdatePlayback(PlaybackOptions) error
}

// Capabilities describes adjustable properties, not platform availability or
// hardware verification. A renderer without CapabilityProvider supports none.
type Capabilities struct {
	Fit    bool `json:"fit"`
	Speed  bool `json:"speed"`
	Audio  bool `json:"audio"`
	Scale  bool `json:"scale"`
	Resume bool `json:"resume"` // Load honors Item.Start and Stats exposes current position
	FPS    bool `json:"fps"`    // SetFPSCap implements an actual frame/presentation cap
}

type CapabilityProvider interface {
	Capabilities() Capabilities
}

// ValidateOptions prevents a backend from silently ignoring an unsupported
// preference. Defaults remain usable on every backend.
func (c Capabilities) ValidateOptions(o PlaybackOptions) error {
	if !c.Fit && o.Fit != "fill" || !c.Speed && o.Speed != 1 || !c.Audio && !o.Muted || !c.Scale && o.Scale != 1 {
		return fmt.Errorf("%w: playback options", ErrUnsupported)
	}
	return nil
}

// ErrUnsupported 表示该渲染器不支持此操作。
var ErrUnsupported = errors.New("render: unsupported")

// SourceUpdater preserves the playback timeline and policy during a media swap.
type SourceUpdater interface {
	UpdateSource(Item, visibility.Level) error
}
