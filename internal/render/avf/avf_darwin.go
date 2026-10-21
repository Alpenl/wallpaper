//go:build darwin

// Package avf 是 macOS 渲染器（04 章）：AVQueuePlayer + AVPlayerLooper + AVPlayerLayer，进程内，
// 挂到 desktop/mac 表面的 contentView.layer 上。L0 播放，L1–L3 暂停（L3 不释放 AVPlayer：MVP 简化），L4 由调用方 Close。
// 未在真机验证（盒子上无 macOS）。
package avf

import (
	"errors"
	"sync"

	"github.com/ebitengine/purego/objc"

	"wpapp/internal/desktop"
	"wpapp/internal/platform/macos"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

type Renderer struct {
	mu     sync.Mutex
	player objc.ID
	looper objc.ID
	layer  objc.ID
	done   chan error
	fps    int
	item   render.Item
	surf   desktop.Surface
	paused bool
}

func New() *Renderer { return &Renderer{done: make(chan error, 1)} }

func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	if err := macos.Load(); err != nil {
		return err
	}
	view := objc.ID(s.Native())
	if view == 0 {
		return errors.New("avf: no view")
	}
	r.mu.Lock()
	r.item, r.surf, r.paused = it, s, paused
	fps := r.fps
	r.mu.Unlock()
	macos.Main(func() {
		url := macos.Class("NSURL").Send(macos.Sel("fileURLWithPath:"), macos.NSString(it.Path))
		item := macos.Class("AVPlayerItem").Send(macos.Sel("playerItemWithURL:"), url)
		if fps > 0 {
			// F-05 限帧：给模板 item 挂一个 frameDuration=1/fps 的 videoComposition（AVPlayerLooper 复制模板，
			// 每一轮都带上）。未在真机验证【推断】。
			asset := item.Send(macos.Sel("asset"))
			comp := macos.Class("AVMutableVideoComposition").Send(macos.Sel("videoCompositionWithPropertiesOfAsset:"), asset)
			if comp != 0 {
				macos.MsgSetCMTime(comp, macos.Sel("setFrameDuration:"), macos.CMTime{Value: 1, Timescale: int32(fps), Flags: 1})
				item.Send(macos.Sel("setVideoComposition:"), comp)
			}
		}
		p := macos.Class("AVQueuePlayer").Send(macos.Sel("new"))
		p.Send(macos.Sel("setMuted:"), true)
		lp := macos.Class("AVPlayerLooper").Send(macos.Sel("playerLooperWithPlayer:templateItem:"), p, item)
		lp.Send(macos.Sel("retain"))
		ly := macos.Class("AVPlayerLayer").Send(macos.Sel("playerLayerWithPlayer:"), p)
		ly.Send(macos.Sel("retain"))
		ly.Send(macos.Sel("setVideoGravity:"), macos.GravityFill)
		root := view.Send(macos.Sel("layer"))
		macos.MsgSetFrame(ly, macos.Sel("setFrame:"), objc.Send[macos.CGRect](root, macos.Sel("bounds")))
		ly.Send(macos.Sel("setAutoresizingMask:"), uint(2|16))
		root.Send(macos.Sel("addSublayer:"), ly)
		if !paused {
			p.Send(macos.Sel("play"))
		}
		r.mu.Lock()
		r.player, r.looper, r.layer = p, lp, ly
		r.mu.Unlock()
	})
	return nil
}

func (r *Renderer) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	p := r.player
	r.mu.Unlock()
	if p == 0 {
		return nil
	}
	macos.Main(func() {
		r.mu.Lock()
		r.paused = l != visibility.L0
		r.mu.Unlock()
		if l == visibility.L0 {
			p.Send(macos.Sel("play"))
		} else {
			p.Send(macos.Sel("pause"))
		}
	})
	return nil
}

// SetFPSCap 换成带 videoComposition 的新 looper（AVPlayer 没有运行时限帧属性）。从头播放。
func (r *Renderer) SetFPSCap(fps int) error {
	r.mu.Lock()
	if r.fps == fps {
		r.mu.Unlock()
		return nil
	}
	r.fps = fps
	it, s, running := r.item, r.surf, r.player != 0
	paused := r.paused
	r.mu.Unlock()
	if !running || s == nil {
		return nil
	}
	r.Close()
	return r.Load(it, s, paused)
}

func (r *Renderer) Snapshot(string) error { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats   { return render.Stats{Backend: "avf", HWDec: "videotoolbox"} }
func (r *Renderer) Done() <-chan error    { return r.done }

func (r *Renderer) Close() error {
	r.mu.Lock()
	p, lp, ly := r.player, r.looper, r.layer
	r.player, r.looper, r.layer = 0, 0, 0
	r.mu.Unlock()
	if p == 0 {
		return nil
	}
	macos.Main(func() {
		p.Send(macos.Sel("pause"))
		lp.Send(macos.Sel("disableLooping"))
		ly.Send(macos.Sel("removeFromSuperlayer"))
		ly.Send(macos.Sel("release"))
		lp.Send(macos.Sel("release"))
		p.Send(macos.Sel("release"))
	})
	return nil
}

var _ render.Renderer = (*Renderer)(nil)
