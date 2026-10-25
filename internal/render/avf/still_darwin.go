//go:build darwin

package avf

import (
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"wpapp/internal/platform/macos"
)

var stillOnce sync.Once
var stillErr error
var copyStill func(objc.ID, objc.SEL, macos.CMTime, *macos.CMTime, *objc.ID) uintptr
var setStillSize func(objc.ID, objc.SEL, macos.CGSize)
var releaseStill func(uintptr)
var stillGravity map[string]objc.ID

func loadStill() error {
	stillOnce.Do(func() {
		lo, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
		if err != nil {
			stillErr = err
			return
		}
		msg, err := purego.Dlsym(lo, "objc_msgSend")
		if err != nil {
			stillErr = err
			return
		}
		purego.RegisterFunc(&copyStill, msg)
		purego.RegisterFunc(&setStillSize, msg)
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
		if err != nil {
			stillErr = err
			return
		}
		purego.RegisterLibFunc(&releaseStill, cg, "CGImageRelease")
		stillGravity = map[string]objc.ID{
			"fill":    macos.NSString("resizeAspectFill").Send(macos.Sel("retain")),
			"fit":     macos.NSString("resizeAspect").Send(macos.Sel("retain")),
			"stretch": macos.NSString("resize").Send(macos.Sel("retain")),
		}
	})
	return stillErr
}

// Layer swaps must not implicitly fade through black during the handoff.
func withoutLayerActions(fn func()) {
	t := macos.Class("CATransaction")
	t.Send(macos.Sel("begin"))
	t.Send(macos.Sel("setDisableActions:"), true)
	defer t.Send(macos.Sel("commit"))
	fn()
}

// A bounded raster survives L3 without retaining a player, looper or video layer.
// Image decoding runs on the renderer worker, never inside the AppKit callback.
func (r *Renderer) releaseWithStill(stop chan struct{}) error {
	var gen objc.ID
	var at macos.CMTime
	var setupErr error
	macos.Main(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stop != stop || r.player == 0 {
			return
		}
		if setupErr = loadStill(); setupErr != nil {
			return
		}
		r.paused = true
		r.player.Send(macos.Sel("pause"))
		tm := objc.Send[macos.CMTime](r.player, macos.Sel("currentTime"))
		if pos, ok := mediaTimeSeconds(tm.Value, tm.Timescale, tm.Flags); ok {
			r.item.Start = pos
		}
		if r.pendingStart > 0 {
			r.item.Start = r.pendingStart
		}
		item := r.player.Send(macos.Sel("currentItem"))
		duration := objc.Send[macos.CMTime](item, macos.Sel("duration"))
		if seconds, ok := mediaTimeSeconds(duration.Value, duration.Timescale, duration.Flags); ok && seconds > 0 && r.item.Start >= seconds {
			r.item.Start = math.Mod(r.item.Start, seconds)
		}
		ticks, _ := seekTicks(r.item.Start)
		at = macos.CMTime{Value: ticks, Timescale: seekTimescale, Flags: 1}
		asset := r.player.Send(macos.Sel("currentItem")).Send(macos.Sel("asset"))
		gen = macos.Class("AVAssetImageGenerator").Send(macos.Sel("alloc")).Send(macos.Sel("initWithAsset:"), asset)
		gen.Send(macos.Sel("setAppliesPreferredTrackTransform:"), true)
		setStillSize(gen, macos.Sel("setMaximumSize:"), macos.CGSize{W: 1920, H: 1920})
		zero := macos.CMTime{Timescale: seekTimescale, Flags: 1}
		macos.MsgSetCMTime(gen, macos.Sel("setRequestedTimeToleranceBefore:"), zero)
		macos.MsgSetCMTime(gen, macos.Sel("setRequestedTimeToleranceAfter:"), zero)
	})
	if setupErr != nil {
		return setupErr
	}
	if gen == 0 {
		return nil
	}
	defer macos.Main(func() { gen.Send(macos.Sel("release")) })
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := macos.Class("NSAutoreleasePool").Send(macos.Sel("new"))
	defer pool.Send(macos.Sel("drain"))
	var failure objc.ID
	image := copyStill(gen, macos.Sel("copyCGImageAtTime:actualTime:error:"), at, nil, &failure)
	if image == 0 {
		return fmt.Errorf("avf: cannot retain pause frame: %s", macos.GoString(failure.Send(macos.Sel("localizedDescription"))))
	}
	defer releaseStill(image)
	var p, lp, ly objc.ID
	macos.Main(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stop != stop || r.player == 0 {
			return
		}
		withoutLayerActions(func() {
			root := objc.ID(r.surf.Native()).Send(macos.Sel("layer"))
			root.Send(macos.Sel("setContentsGravity:"), stillGravity[r.options.Fit])
			root.Send(macos.Sel("setContents:"), image)
		})
		p, lp, ly = r.player, r.looper, r.layer
		r.player, r.looper, r.layer = 0, 0, 0
		close(r.stop)
		r.stop = nil
		r.released = true
		st := r.Stats()
		st.Pos = r.item.Start
		r.stats.Store(st)
	})
	releasePlayer(p, lp, ly)
	return nil
}
