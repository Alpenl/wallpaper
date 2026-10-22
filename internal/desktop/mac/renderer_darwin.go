//go:build darwin

package mac

import (
	"fmt"
	"github.com/ebitengine/purego/objc"
	"image"
	"wpapp/internal/platform/macos"
)

// RendererWindow creates a process-owned desktop view on the caller's AppKit
// thread. NSView pointers cannot be inherited by renderer children, so isolated
// renderers use the same geometry/Space/desktop-level contract in their process.
// The caller initializes AppKit and the macos native function bindings first,
// and owns closing and releasing the returned NSWindow.
func RendererWindow(bounds image.Rectangle) (window, view objc.ID, err error) {
	class := func(s string) objc.ID { return objc.ID(objc.GetClass(s)) }
	sel := objc.RegisterName
	screens := class("NSScreen").Send(sel("screens"))
	if objc.Send[uintptr](screens, sel("count")) == 0 {
		return 0, 0, fmt.Errorf("desktop: no accessible macOS graphical session")
	}
	primary := objc.Send[macos.CGRect](screens.Send(sel("objectAtIndex:"), 0), sel("frame"))
	frame := macos.CGRect{Origin: macos.CGPoint{X: float64(bounds.Min.X), Y: primary.Size.H - float64(bounds.Max.Y)}, Size: macos.CGSize{W: float64(bounds.Dx()), H: float64(bounds.Dy())}}
	window = macos.MsgInitWindow(class("NSWindow").Send(sel("alloc")), sel("initWithContentRect:styleMask:backing:defer:"), frame, 0, 2, false, 0)
	if window == 0 {
		return 0, 0, fmt.Errorf("desktop: cannot create renderer window")
	}
	window.Send(sel("setReleasedWhenClosed:"), false)
	window.Send(sel("setLevel:"), int64(macos.CGWindowLevelForKey(2)))
	window.Send(sel("setCollectionBehavior:"), uintptr(1|16|64))
	window.Send(sel("setIgnoresMouseEvents:"), true)
	return window, window.Send(sel("contentView")), nil
}
