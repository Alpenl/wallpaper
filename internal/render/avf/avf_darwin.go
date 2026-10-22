//go:build darwin

// Package avf 是 macOS 渲染器（04 章）：AVQueuePlayer + AVPlayerLooper + AVPlayerLayer，进程内，
// 挂到 desktop/mac 表面的 contentView.layer 上。L0 播放，L1–L3 暂停（L3 不释放 AVPlayer：MVP 简化），L4 由调用方 Close。
// 未在真机验证（盒子上无 macOS）。
package avf

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"wpapp/internal/desktop"
	"wpapp/internal/platform/macos"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

type Renderer struct {
	mu            sync.Mutex
	player        objc.ID
	looper        objc.ID
	layer         objc.ID
	done          chan error
	fps           int
	item          render.Item
	surf          desktop.Surface
	paused        bool
	options       render.PlaybackOptions
	stop          chan struct{}
	pendingStart  float64
	positionKnown bool
	stats         atomic.Value // render.Stats; no native/UI calls in Stats
}

func New() *Renderer {
	r := &Renderer{done: make(chan error, 1), options: render.DefaultPlaybackOptions()}
	r.stats.Store(render.Stats{Backend: "avf", HWDec: "unknown"})
	return r
}

func (r *Renderer) Capabilities() render.Capabilities {
	return render.Capabilities{Fit: true, Speed: true, Audio: true, Resume: true, FPS: true}
}

func (r *Renderer) Configure(o render.PlaybackOptions) error {
	o, err := render.NormalizePlaybackOptions(o)
	if err != nil {
		return err
	}
	if err := r.Capabilities().ValidateOptions(o); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.player != 0 {
		return errors.New("avf: configure before Load")
	}
	r.options = o
	return nil
}

// UpdatePlayback applies native properties in place. A paused player stays
// paused; an initial seek keeps its hold and uses the newest speed when ready.
func (r *Renderer) UpdatePlayback(o render.PlaybackOptions) error {
	o, err := render.NormalizePlaybackOptions(o)
	if err != nil {
		return err
	}
	if err := r.Capabilities().ValidateOptions(o); err != nil {
		return err
	}
	r.mu.Lock()
	if r.player == 0 {
		r.mu.Unlock()
		return errors.New("avf: not running")
	}
	stop := r.stop
	r.mu.Unlock()
	macos.Main(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stop != stop || r.player == 0 {
			err = errors.New("avf: player closed during playback update")
			return
		}
		if o.Fit != r.options.Fit {
			r.layer.Send(macos.Sel("setVideoGravity:"), gravities[o.Fit])
		}
		if o.Muted != r.options.Muted {
			r.player.Send(macos.Sel("setMuted:"), o.Muted)
		}
		if o.Volume != r.options.Volume {
			r.player.Send(macos.Sel("setVolume:"), float32(o.Volume)/100)
		}
		if o.Speed != r.options.Speed && !r.paused && r.pendingStart == 0 {
			r.player.Send(macos.Sel("setRate:"), float32(o.Speed))
		}
		r.options = o
	})
	return err
}

// Resolve the exported NSString constants rather than depending on their
// undocumented string values. Framework loading is deferred until Load.
var gravityOnce sync.Once
var gravities map[string]objc.ID
var gravityErr error

func loadGravities() error {
	gravityOnce.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			gravityErr = err
			return
		}
		gravities = map[string]objc.ID{}
		for fit, symbol := range map[string]string{"fill": "AVLayerVideoGravityResizeAspectFill", "fit": "AVLayerVideoGravityResizeAspect", "stretch": "AVLayerVideoGravityResize"} {
			address, err := purego.Dlsym(h, symbol)
			if err != nil {
				gravityErr = fmt.Errorf("avf: %s: %w", symbol, err)
				return
			}
			gravities[fit] = *(*objc.ID)(unsafe.Pointer(address))
		}
	})
	return gravityErr
}

func (r *Renderer) Load(it render.Item, s desktop.Surface, paused bool) error {
	if _, ok := seekTicks(it.Start); !ok {
		return errors.New("avf: invalid start position")
	}
	if err := macos.Load(); err != nil {
		return err
	}
	if err := loadGravities(); err != nil {
		return err
	}
	view := objc.ID(s.Native())
	if view == 0 {
		return errors.New("avf: no view")
	}
	r.Close()
	stop := make(chan struct{})
	r.mu.Lock()
	r.item, r.surf, r.paused = it, s, paused
	r.stop, r.pendingStart = stop, it.Start
	r.positionKnown = false
	fps := r.fps
	o := r.options
	r.mu.Unlock()
	// Retain the saved resume offset while readiness/seek is pending. Otherwise
	// an immediate restart would overwrite a valid saved position with zero.
	r.stats.Store(render.Stats{Backend: "avf", HWDec: "unknown", Pos: it.Start})
	var loadErr error
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
			} else {
				loadErr = errors.New("avf: could not create video composition for FPS cap")
				return
			}
		}
		p := macos.Class("AVQueuePlayer").Send(macos.Sel("new"))
		p.Send(macos.Sel("setMuted:"), o.Muted)
		p.Send(macos.Sel("setVolume:"), float32(o.Volume)/100)
		lp := macos.Class("AVPlayerLooper").Send(macos.Sel("playerLooperWithPlayer:templateItem:"), p, item)
		lp.Send(macos.Sel("retain"))
		ly := macos.Class("AVPlayerLayer").Send(macos.Sel("playerLayerWithPlayer:"), p)
		ly.Send(macos.Sel("retain"))
		ly.Send(macos.Sel("setVideoGravity:"), gravities[o.Fit])
		root := view.Send(macos.Sel("layer"))
		macos.MsgSetFrame(ly, macos.Sel("setFrame:"), objc.Send[macos.CGRect](root, macos.Sel("bounds")))
		ly.Send(macos.Sel("setAutoresizingMask:"), uint(2|16))
		root.Send(macos.Sel("addSublayer:"), ly)
		if !paused && it.Start == 0 {
			p.Send(macos.Sel("setRate:"), float32(o.Speed))
		}
		r.mu.Lock()
		r.player, r.looper, r.layer = p, lp, ly
		r.mu.Unlock()
	})
	if loadErr != nil {
		r.Close()
		return loadErr
	}
	go r.sampleLoop(stop)
	return nil
}

func (r *Renderer) sampleLoop(stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		pending := false
		macos.Main(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.stop != stop || r.player == 0 {
				return // a queued callback must not touch a closed/replaced player
			}
			r.sampleOnMain()
			pending = r.pendingStart > 0
		})
		delay := time.Second
		if pending {
			delay = 250 * time.Millisecond // wait for metadata before the initial seek
		}
		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// sampleOnMain requires the native main thread and r.mu. Seeking an item before
// it is ready may be ignored, so a nonzero Start holds playback until ready.
func (r *Renderer) sampleOnMain() {
	p := r.player
	if p == 0 {
		return
	}
	item := p.Send(macos.Sel("currentItem"))
	status := objc.Send[int64](item, macos.Sel("status"))
	if status == 2 { // AVPlayerItemStatusFailed
		st := r.Stats()
		if st.LastErr == "" {
			st.LastErr = "avf: " + macos.GoString(item.Send(macos.Sel("error")).Send(macos.Sel("localizedDescription")))
			r.stats.Store(st)
			select {
			case r.done <- errors.New(st.LastErr):
			default:
			}
		}
		return
	}
	if status != 1 { // AVPlayerItemStatusReadyToPlay
		return
	}
	if r.pendingStart > 0 {
		start := r.pendingStart
		d := objc.Send[macos.CMTime](item, macos.Sel("duration"))
		if duration, ok := mediaTimeSeconds(d.Value, d.Timescale, d.Flags); ok && duration > 0 && start >= duration {
			start = math.Mod(start, duration)
		}
		ticks, _ := seekTicks(start)
		macos.MsgSetCMTime(p, macos.Sel("seekToTime:"), macos.CMTime{Value: ticks, Timescale: seekTimescale, Flags: 1})
		r.pendingStart = 0
		if !r.paused {
			p.Send(macos.Sel("setRate:"), float32(r.options.Speed))
		}
		return // seek is asynchronous; only report a measured position next time
	}
	tm := objc.Send[macos.CMTime](p, macos.Sel("currentTime"))
	st := r.Stats()
	if pos, ok := mediaTimeSeconds(tm.Value, tm.Timescale, tm.Flags); ok {
		st.Pos = pos
		r.positionKnown = true
	}
	st.Loops = objc.Send[int64](r.looper, macos.Sel("loopCount"))
	r.stats.Store(st)
}

func (r *Renderer) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	if r.player == 0 {
		r.mu.Unlock()
		return nil
	}
	stop := r.stop
	r.mu.Unlock()
	macos.Main(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stop != stop || r.player == 0 {
			return
		}
		r.paused = l != visibility.L0
		if l == visibility.L0 && r.pendingStart == 0 {
			r.player.Send(macos.Sel("setRate:"), float32(r.options.Speed))
		} else {
			r.player.Send(macos.Sel("pause"))
		}
		r.sampleOnMain()
	})
	return nil
}

// SetFPSCap replaces the videoComposition/looper and resumes its last measured position.
func (r *Renderer) SetFPSCap(fps int) error {
	if fps < 0 || fps > 240 {
		return errors.New("avf: fps must be between 0 and 240")
	}
	r.mu.Lock()
	if r.fps == fps {
		r.mu.Unlock()
		return nil
	}
	r.fps = fps
	it, s, running := r.item, r.surf, r.player != 0
	if r.positionKnown {
		it.Start = r.Stats().Pos
	}
	paused := r.paused
	r.mu.Unlock()
	if !running || s == nil {
		return nil
	}
	r.Close()
	return r.Load(it, s, paused)
}

func (r *Renderer) Snapshot(string) error { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats   { return r.stats.Load().(render.Stats) }
func (r *Renderer) Done() <-chan error    { return r.done }

func (r *Renderer) Close() error {
	r.mu.Lock()
	p, lp, ly := r.player, r.looper, r.layer
	r.player, r.looper, r.layer = 0, 0, 0
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
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
var _ render.Configurable = (*Renderer)(nil)
var _ render.PlaybackUpdater = (*Renderer)(nil)
var _ render.CapabilityProvider = (*Renderer)(nil)
