//go:build darwin

package mac

import (
	"context"
	"errors"

	"github.com/ebitengine/purego/objc"

	"wpapp/internal/platform"
)

// Surface fields except immutable size/view are main-thread owned. The session
// closes its player first, then closes/releases the NSWindow and its content view.
type Surface struct {
	window, view objc.ID
	w, h         int
	closed       bool
}

func New(ctx context.Context) (*Surface, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	var s *Surface
	var err error
	Main(func() {
		Pool(func() {
			if err = ctx.Err(); err != nil {
				return
			}
			screens := Class("NSScreen").Send(Sel("screens"))
			if objc.Send[uint](screens, Sel("count")) == 0 {
				err = errors.New("mac: no screen")
				return
			}
			screen := screens.Send(Sel("objectAtIndex:"), uint(0))
			frame := objc.Send[CGRect](screen, Sel("frame"))
			window := MsgInitWindow(Class("NSWindow").Send(Sel("alloc")), Sel("initWithContentRect:styleMask:backing:defer:screen:"), frame, 0, 2, false, screen)
			if window == 0 {
				err = errors.New("mac: cannot create desktop window")
				return
			}
			window.Send(Sel("setReleasedWhenClosed:"), false)
			window.Send(Sel("setLevel:"), int(windowLevel(2)))
			window.Send(Sel("setCollectionBehavior:"), uint(1|16|64))
			window.Send(Sel("setIgnoresMouseEvents:"), true)
			window.Send(Sel("setHasShadow:"), false)
			window.Send(Sel("setOpaque:"), false)
			window.Send(Sel("setBackgroundColor:"), Class("NSColor").Send(Sel("clearColor")))
			view := window.Send(Sel("contentView"))
			view.Send(Sel("setWantsLayer:"), true)
			if view == 0 || view.Send(Sel("layer")) == 0 {
				window.Send(Sel("close"))
				window.Send(Sel("release"))
				err = errors.New("mac: no content layer")
				return
			}
			s = &Surface{window: window, view: view, w: int(frame.Size.W), h: int(frame.Size.H)}
		})
	})
	return s, err
}
func (s *Surface) Native() uintptr  { return uintptr(s.view) }
func (s *Surface) Size() (int, int) { return s.w, s.h }
func (s *Surface) Show() error {
	var err error
	Main(func() {
		if s.closed {
			err = errors.New("mac: surface closed")
			return
		}
		s.window.Send(Sel("orderFront:"), objc.ID(0))
	})
	return err
}
func (s *Surface) Evidence() (platform.SurfaceEvidence, error) {
	e := platform.SurfaceEvidence{Visible: platform.No, OnScreen: platform.No, Attached: platform.No, Occluded: platform.Unknown, Window: 0}
	Main(func() {
		Pool(func() {
			if s.closed {
				return
			}
			e.Window = objc.Send[uintptr](s.window, Sel("windowNumber"))
			e.Visible = platform.Known(objc.Send[bool](s.window, Sel("isVisible")))
			screen := s.window.Send(Sel("screen"))
			e.Attached = platform.Known(screen != 0 && s.window.Send(Sel("contentView")) == s.view)
			if screen != 0 {
				a := objc.Send[CGRect](screen, Sel("frame"))
				b := objc.Send[CGRect](s.window, Sel("frame"))
				// AppKit occlusion is informational: desktop windows may report
				// occluded even while their video is visible on the desktop.
				state := objc.Send[uint64](s.window, Sel("occlusionState"))
				e.Occluded = platform.Known(state&2 == 0)
				intersects := a.Origin.X < b.Origin.X+b.Size.W && b.Origin.X < a.Origin.X+a.Size.W && a.Origin.Y < b.Origin.Y+b.Size.H && b.Origin.Y < a.Origin.Y+a.Size.H
				if intersects {
					e.OnScreen = onScreen(e.Window)
				}
			}
		})
	})
	return e, nil
}
func (s *Surface) Close() error {
	Main(func() {
		if s.closed {
			return
		}
		s.closed = true
		s.window.Send(Sel("orderOut:"), objc.ID(0))
		s.window.Send(Sel("close"))
		s.window.Send(Sel("release"))
	})
	return nil
}

// onScreen runs on the main thread. CGWindow metadata describes desktop-space
// membership independently of whether other windows cover the wallpaper.
func onScreen(number uintptr) platform.Truth {
	list := windowInfo(8, uint32(number)) // kCGWindowListOptionIncludingWindow
	if list == 0 {
		return platform.Unknown
	}
	defer list.Send(Sel("release")) // Copy result; CFArray is toll-free bridged.
	for i, n := uint(0), objc.Send[uint](list, Sel("count")); i < n; i++ {
		info := list.Send(Sel("objectAtIndex:"), i)
		id := info.Send(Sel("objectForKey:"), windowNumber)
		if id == 0 || objc.Send[uintptr](id, Sel("unsignedIntegerValue")) != number {
			continue
		}
		value := info.Send(Sel("objectForKey:"), windowIsOnscreen)
		if value == 0 {
			return platform.Unknown
		}
		return platform.Known(objc.Send[bool](value, Sel("boolValue")))
	}
	return platform.No
}
