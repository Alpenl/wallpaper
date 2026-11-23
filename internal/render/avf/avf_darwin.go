//go:build darwin

// Package avf 是 macOS 渲染器（04 章）：AVQueuePlayer + AVPlayerLooper + AVPlayerLayer，进程内，
// 挂到 desktop/mac 表面的 contentView.layer 上。
// L0 plays, L1–L2 pause, L3 keeps a bounded still while releasing the player; resume rebuilds and reveals a ready video layer. L4 is closed by the caller.
// L3 release/resume measured on Apple Silicon (#14); other paths, including the FPS cap, are still unverified on device.
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
	released      bool
	options       render.PlaybackOptions
	stop          chan struct{}
	pendingStart  float64
	seeking       bool
	positionKnown bool
	awaitingFrame bool
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
	if r.released {
		r.options = o
		r.mu.Unlock()
		return nil
	}
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
		if o.Speed != r.options.Speed && !r.paused && r.pendingStart == 0 && !r.seeking {
			r.player.Send(macos.Sel("setRate:"), float32(o.Speed))
		}
		r.options = o
	})
	return err
}

// rawPtr reinterprets a native (non-Go) symbol address as unsafe.Pointer.
func rawPtr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

// Resolve the exported NSString constants and seek completion msgSend binding
// rather than depending on undocumented string values.
var (
	gravityOnce       sync.Once
	gravities         map[string]objc.ID
	mediaTypeVideo    objc.ID
	msgSeekCompletion func(objc.ID, objc.SEL, macos.CMTime, macos.CMTime, macos.CMTime, objc.Block)
	gravityErr        error
)

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
			gravities[fit] = *(*objc.ID)(rawPtr(address))
		}
		if addr, err := purego.Dlsym(h, "AVMediaTypeVideo"); err == nil {
			mediaTypeVideo = *(*objc.ID)(rawPtr(addr))
		}
		lo, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
		if err != nil {
			gravityErr = err
			return
		}
		purego.RegisterLibFunc(&msgSeekCompletion, lo, "objc_msgSend")
	})
	return gravityErr
}

// buildFPSComposition inspects video track geometry and nominal frame rate off
// the main thread so AppKit never blocks synchronously loading tracks. It
// returns a retained AVMutableVideoComposition only when fps > 0, naturalSize
// is valid, and fps is strictly below the track's nominal frame rate.
func buildFPSComposition(path string, fps int) (objc.ID, error) {
	if fps <= 0 {
		return 0, nil
	}
	pool := macos.Class("NSAutoreleasePool").Send(macos.Sel("new"))
	defer pool.Send(macos.Sel("drain"))
	url := macos.Class("NSURL").Send(macos.Sel("fileURLWithPath:"), macos.NSString(path))
	asset := macos.Class("AVURLAsset").Send(macos.Sel("URLAssetWithURL:options:"), url, objc.ID(0))
	if asset == 0 {
		return 0, nil
	}
	mt := mediaTypeVideo
	if mt == 0 {
		mt = macos.NSString("vide")
	}
	tracks := asset.Send(macos.Sel("tracksWithMediaType:"), mt)
	if tracks == 0 || objc.Send[uintptr](tracks, macos.Sel("count")) == 0 {
		return 0, nil
	}
	track := tracks.Send(macos.Sel("objectAtIndex:"), uintptr(0))
	if track == 0 {
		return 0, nil
	}
	natural := objc.Send[macos.CGSize](track, macos.Sel("naturalSize"))
	nominalFPS := float64(objc.Send[float32](track, macos.Sel("nominalFrameRate")))
	if !shouldAttachFPSComposition(fps, natural.W, natural.H, nominalFPS) {
		return 0, nil
	}
	comp := macos.Class("AVMutableVideoComposition").Send(macos.Sel("videoCompositionWithPropertiesOfAsset:"), asset)
	if comp == 0 {
		return 0, errors.New("avf: could not create video composition for FPS cap")
	}
	macos.MsgSetCMTime(comp, macos.Sel("setFrameDuration:"), macos.CMTime{Value: 1, Timescale: int32(fps), Flags: 1})
	comp.Send(macos.Sel("retain"))
	return comp, nil
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
	r.mu.Lock()
	sameSurface := r.surf != nil && r.surf.Native() == uintptr(view)
	fps := r.fps
	o := r.options
	r.mu.Unlock()
	r.close(sameSurface)

	// Prepare optional FPS videoComposition off the main thread.
	comp, err := buildFPSComposition(it.Path, fps)
	if err != nil {
		return err
	}

	stop := make(chan struct{})
	r.mu.Lock()
	r.released = false
	r.item, r.surf, r.paused = it, s, paused
	r.stop, r.pendingStart = stop, it.Start
	r.seeking = false
	r.positionKnown = false
	r.awaitingFrame = true
	r.mu.Unlock()
	// Retain the saved resume offset while readiness/seek is pending. Otherwise
	// an immediate restart would overwrite a valid saved position with zero.
	r.stats.Store(render.Stats{Backend: "avf", HWDec: "unknown", Pos: it.Start})
	macos.Main(func() {
		defer func() {
			if comp != 0 {
				comp.Send(macos.Sel("release"))
			}
		}()
		url := macos.Class("NSURL").Send(macos.Sel("fileURLWithPath:"), macos.NSString(it.Path))
		item := macos.Class("AVPlayerItem").Send(macos.Sel("playerItemWithURL:"), url)
		if comp != 0 {
			item.Send(macos.Sel("setVideoComposition:"), comp)
		}
		p := macos.Class("AVQueuePlayer").Send(macos.Sel("new"))
		p.Send(macos.Sel("setMuted:"), o.Muted)
		p.Send(macos.Sel("setVolume:"), float32(o.Volume)/100)
		lp := macos.Class("AVPlayerLooper").Send(macos.Sel("playerLooperWithPlayer:templateItem:"), p, item)
		lp.Send(macos.Sel("retain"))
		ly := macos.Class("AVPlayerLayer").Send(macos.Sel("playerLayerWithPlayer:"), p)
		ly.Send(macos.Sel("retain"))
		ly.Send(macos.Sel("setVideoGravity:"), gravities[o.Fit])
		// Keep an opaque black backdrop on the video layer because the parent window may be transparent.
		if black := macos.Class("NSColor").Send(macos.Sel("blackColor")); black != 0 {
			if cg := black.Send(macos.Sel("CGColor")); cg != 0 {
				ly.Send(macos.Sel("setBackgroundColor:"), cg)
			}
		}
		root := view.Send(macos.Sel("layer"))
		macos.MsgSetFrame(ly, macos.Sel("setFrame:"), objc.Send[macos.CGRect](root, macos.Sel("bounds")))
		ly.Send(macos.Sel("setAutoresizingMask:"), uint(2|16))
		ly.Send(macos.Sel("setHidden:"), true)
		root.Send(macos.Sel("addSublayer:"), ly)
		if !paused && it.Start == 0 {
			p.Send(macos.Sel("setRate:"), float32(o.Speed))
		}
		r.mu.Lock()
		r.player, r.looper, r.layer = p, lp, ly
		r.mu.Unlock()
	})
	// Renderer 拥有采样循环；Close 关闭 stop 后退出。
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
			pending = r.pendingStart > 0 || r.seeking || r.awaitingFrame
		})
		delay := time.Second
		if pending {
			delay = 50 * time.Millisecond // transient readiness polling; steady playback stays at 1 Hz
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

func (r *Renderer) reportError(msg string) {
	st := r.Stats()
	if st.LastErr == "" {
		st.LastErr = msg
		r.stats.Store(st)
		select {
		case r.done <- errors.New(st.LastErr):
		default:
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
	if r.looper != 0 && objc.Send[int64](r.looper, macos.Sel("status")) == 2 { // AVPlayerLooperStatusFailed
		msg := macos.GoString(r.looper.Send(macos.Sel("error")).Send(macos.Sel("localizedDescription")))
		if msg == "" {
			msg = "looper failed"
		}
		r.reportError("avf: " + msg)
		return
	}
	item := p.Send(macos.Sel("currentItem"))
	if item == 0 {
		return
	}
	status := objc.Send[int64](item, macos.Sel("status"))
	if status == 2 { // AVPlayerItemStatusFailed
		msg := macos.GoString(item.Send(macos.Sel("error")).Send(macos.Sel("localizedDescription")))
		if msg == "" {
			msg = "item failed"
		}
		r.reportError("avf: " + msg)
		return
	}
	if status != 1 { // AVPlayerItemStatusReadyToPlay
		return
	}
	if r.seeking {
		return // wait for seek completionHandler before revealing or sampling position
	}
	if r.pendingStart > 0 {
		start := r.pendingStart
		d := objc.Send[macos.CMTime](item, macos.Sel("duration"))
		if duration, ok := mediaTimeSeconds(d.Value, d.Timescale, d.Flags); ok && duration > 0 && start >= duration {
			start = math.Mod(start, duration)
		}
		ticks, _ := seekTicks(start)
		// Default seek tolerances may land on a nearby keyframe, skipping seconds
		// on L3 resume. Require the saved presentation time in both directions
		// and wait for completionHandler before clearing pendingStart or revealing.
		zero := macos.CMTime{Timescale: seekTimescale, Flags: 1}
		target := macos.CMTime{Value: ticks, Timescale: seekTimescale, Flags: 1}
		r.seeking = true
		stop := r.stop
		var blk objc.Block
		blk = objc.NewBlock(func(_ objc.Block, finished bool) {
			// Dispatch back to main asynchronously so the block is never released
			// before the ObjC callback returns and locks are never contended on AVF's queue.
			go func() {
				macos.Main(func() {
					defer blk.Release()
					r.mu.Lock()
					defer r.mu.Unlock()
					if r.stop != stop || r.player != p || r.player == 0 {
						return
					}
					r.seeking = false
					if finished {
						r.pendingStart = 0
						if !r.paused {
							r.player.Send(macos.Sel("setRate:"), float32(r.options.Speed))
						}
						r.sampleOnMain()
					}
				})
			}()
		})
		msgSeekCompletion(p, macos.Sel("seekToTime:toleranceBefore:toleranceAfter:completionHandler:"), target, zero, zero, blk)
		return
	}
	tm := objc.Send[macos.CMTime](p, macos.Sel("currentTime"))
	st := r.Stats()
	if pos, ok := mediaTimeSeconds(tm.Value, tm.Timescale, tm.Flags); ok {
		st.Pos = pos
		r.positionKnown = true
	}
	st.Loops = objc.Send[int64](r.looper, macos.Sel("loopCount"))
	r.stats.Store(st)
	if r.awaitingFrame && r.surf != nil && objc.Send[bool](r.layer, macos.Sel("isReadyForDisplay")) {
		withoutLayerActions(func() {
			r.layer.Send(macos.Sel("setHidden:"), false)
			objc.ID(r.surf.Native()).Send(macos.Sel("layer")).Send(macos.Sel("setContents:"), objc.ID(0))
		})
		r.awaitingFrame = false
	}
}

func (r *Renderer) SetLevel(l visibility.Level) error {
	r.mu.Lock()
	if r.released {
		if l >= visibility.L3 {
			r.mu.Unlock()
			return nil
		}
		it, s, paused := r.item, r.surf, l > visibility.L0
		r.released = false
		r.mu.Unlock()
		return r.Load(it, s, paused)
	}
	if r.player == 0 {
		r.mu.Unlock()
		return nil
	}
	stop := r.stop
	r.mu.Unlock()
	if l >= visibility.L3 {
		return r.releaseWithStill(stop)
	}
	macos.Main(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stop != stop || r.player == 0 {
			return
		}
		r.paused = l != visibility.L0
		if l == visibility.L0 && r.pendingStart == 0 && !r.seeking {
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
	return r.Load(it, s, paused)
}

func (r *Renderer) Snapshot(string) error { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats   { return r.stats.Load().(render.Stats) }
func (r *Renderer) Done() <-chan error    { return r.done }

func (r *Renderer) Close() error {
	return r.close(false)
}

func (r *Renderer) close(keepStill bool) error {
	r.mu.Lock()
	r.released = false
	r.seeking = false
	p, lp, ly := r.player, r.looper, r.layer
	r.player, r.looper, r.layer = 0, 0, 0
	s := r.surf
	if !keepStill {
		r.surf = nil
	}
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
	r.mu.Unlock()
	releasePlayer(p, lp, ly)
	if !keepStill && s != nil {
		macos.Main(func() {
			withoutLayerActions(func() {
				objc.ID(s.Native()).Send(macos.Sel("layer")).Send(macos.Sel("setContents:"), objc.ID(0))
			})
		})
	}
	return nil
}

// releasePlayer must be called without r.mu; native teardown runs on main.
func releasePlayer(p, lp, ly objc.ID) {
	if p == 0 {
		return
	}
	macos.Main(func() {
		p.Send(macos.Sel("pause"))
		lp.Send(macos.Sel("disableLooping"))
		ly.Send(macos.Sel("removeFromSuperlayer"))
		ly.Send(macos.Sel("release"))
		lp.Send(macos.Sel("release"))
		p.Send(macos.Sel("release"))
	})
}

var _ render.Renderer = (*Renderer)(nil)
var _ render.Configurable = (*Renderer)(nil)
var _ render.PlaybackUpdater = (*Renderer)(nil)
var _ render.CapabilityProvider = (*Renderer)(nil)
