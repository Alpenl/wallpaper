package mac

import (
	"image"
	"math"

	"wpapp/internal/desktop"
)

// A screen count alone cannot detect a rearrangement, a resolution or scale
// change, or replacing one output with another. Compare the sorted snapshot so
// every geometry change rebuilds native canvases through EventDisplaysChanged.
func sameDisplayTopology(a, b []desktop.Display) bool {
	if len(a) != len(b) {
		return false
	}
	for i, x := range a {
		y := b[i]
		if x.ID != y.ID || x.Bounds != y.Bounds || x.PixelSize() != y.PixelSize() || x.Scale != y.Scale || x.Primary != y.Primary || x.Name != y.Name || x.Device != y.Device {
			return false
		}
	}
	return true
}

// displayPixels keeps a mode's backing raster separate from the global logical
// desktop. CGDisplayModeGetPixelWidth/Height describe the current mode, not the
// panel's maximum resolution (a scaled Retina mode can render above that).
// Match the raster orientation to the already-rotated global bounds; do not
// blindly swap based on CGDisplayRotation and rotate an oriented mode twice.
func displayPixels(logical, mode image.Point, scale float64) image.Point {
	if mode.X > 0 && mode.Y > 0 {
		if logical.X > 0 && logical.Y > 0 && (logical.X < logical.Y) != (mode.X < mode.Y) && logical.X != logical.Y && mode.X != mode.Y {
			return image.Pt(mode.Y, mode.X)
		}
		return mode
	}
	// A mode can temporarily be unavailable during reconfiguration. Only the
	// macOS host uses this fallback; DPI scale must not multiply Windows bounds.
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		scale = 1
	}
	return image.Pt(int(math.Round(float64(logical.X)*scale)), int(math.Round(float64(logical.Y)*scale)))
}
