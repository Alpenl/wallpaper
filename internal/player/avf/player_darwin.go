//go:build darwin

// Package avf owns an AVQueuePlayer, looper and hidden-until-ready video layer.
package avf

import (
	"context"
	"errors"
	"fmt"

	"github.com/ebitengine/purego/objc"

	"wpapp/internal/platform"
	macos "wpapp/internal/platform/mac"
)

// Player native fields belong exclusively to the AppKit main thread. Session
// serializes Evidence/Close; the surface outlives this player.
type Player struct {
	player, looper, layer objc.ID
	revealed              bool
}

func Load(ctx context.Context, path string, s platform.Surface) (*Player, error) {
	if err := macos.Load(); err != nil {
		return nil, err
	}
	p := &Player{}
	var err error
	macos.Main(func() {
		macos.Pool(func() {
			if err = ctx.Err(); err != nil {
				return
			}
			view := objc.ID(s.Native())
			root := view.Send(macos.Sel("layer"))
			if root == 0 {
				err = errors.New("avf: missing surface layer")
				return
			}
			url := macos.Class("NSURL").Send(macos.Sel("fileURLWithPath:"), macos.NSString(path))
			item := macos.Class("AVPlayerItem").Send(macos.Sel("playerItemWithURL:"), url)
			p.player = macos.Class("AVQueuePlayer").Send(macos.Sel("new"))
			p.player.Send(macos.Sel("setMuted:"), true)
			p.looper = macos.Class("AVPlayerLooper").Send(macos.Sel("playerLooperWithPlayer:templateItem:"), p.player, item)
			p.looper.Send(macos.Sel("retain"))
			p.layer = macos.Class("AVPlayerLayer").Send(macos.Sel("playerLayerWithPlayer:"), p.player)
			p.layer.Send(macos.Sel("retain"))
			if p.player == 0 || p.looper == 0 || p.layer == 0 {
				err = errors.New("avf: player allocation failed")
				return
			}
			macos.WithoutActions(func() {
				p.layer.Send(macos.Sel("setHidden:"), true)
				p.layer.Send(macos.Sel("setVideoGravity:"), macos.GravityFill)
				macos.MsgSetFrame(p.layer, macos.Sel("setFrame:"), objc.Send[macos.CGRect](root, macos.Sel("bounds")))
				p.layer.Send(macos.Sel("setAutoresizingMask:"), uint(2|16))
				root.Send(macos.Sel("addSublayer:"), p.layer)
			})
			p.player.Send(macos.Sel("play"))
		})
	})
	return p, err
}
func (p *Player) Evidence(ctx context.Context) (platform.PlayerEvidence, error) {
	e := platform.PlayerEvidence{Ready: platform.Unknown}
	var err error
	macos.Main(func() {
		macos.Pool(func() {
			if err = ctx.Err(); err != nil {
				return
			}
			if p.player == 0 {
				err = errors.New("avf: player closed")
				return
			}
			item := p.player.Send(macos.Sel("currentItem"))

			looperError := p.looper.Send(macos.Sel("error"))
			if objc.Send[int64](item, macos.Sel("status")) == 2 || objc.Send[int64](p.looper, macos.Sel("status")) == 2 || (item == 0 && looperError != 0) {
				nativeError := item.Send(macos.Sel("error"))
				reason := macos.GoString(nativeError.Send(macos.Sel("localizedDescription")))
				if reason == "" {
					reason = macos.GoString(looperError.Send(macos.Sel("localizedDescription")))
				}
				if reason == "" {
					reason = "player item or looper reported failure without a description"
				}
				err = fmt.Errorf("avf: playback failed: %s", reason)
				return
			}
			// A loop boundary may temporarily have no current item. This is
			// unknown evidence, not a native error or a synthetic zero position.
			if item == 0 {
				return
			}

			ready := objc.Send[bool](p.layer, macos.Sel("isReadyForDisplay"))
			if ready && !p.revealed {
				macos.WithoutActions(func() { p.layer.Send(macos.Sel("setHidden:"), false) })
				p.revealed = true
			}
			e.Ready = platform.Known(ready && !objc.Send[bool](p.layer, macos.Sel("isHidden")) && p.layer.Send(macos.Sel("superlayer")) != 0)
			t := objc.Send[macos.CMTime](p.player, macos.Sel("currentTime"))
			if t.Flags&1 != 0 && t.Flags&(4|8|16) == 0 && t.Timescale > 0 && t.Value >= 0 {
				pos := float64(t.Value) / float64(t.Timescale)
				e.Position = &pos
			}
			loops := objc.Send[int64](p.looper, macos.Sel("loopCount"))
			if loops >= 0 {
				v := uint64(loops)
				e.Loops = &v
			}
		})
	})
	return e, err
}
func (p *Player) Close() error {
	macos.Main(func() {
		p.player.Send(macos.Sel("pause"))
		p.looper.Send(macos.Sel("disableLooping"))
		p.layer.Send(macos.Sel("removeFromSuperlayer"))
		p.layer.Send(macos.Sel("setPlayer:"), objc.ID(0))
		p.layer.Send(macos.Sel("release"))
		p.looper.Send(macos.Sel("release"))
		p.player.Send(macos.Sel("release"))
		p.layer, p.looper, p.player = 0, 0, 0
	})
	return nil
}
