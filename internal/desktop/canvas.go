package desktop

import (
	"fmt"
	"image"
	"math"
)

// CanvasHost keeps each native window clipped to its physical display while its
// rendering view covers the same virtual desktop. It does not imply that the
// separate decoders are frame-synchronized. Surface.Bounds describes the canvas;
// display IDs and visibility still belong to the physical display.
type CanvasHost interface {
	CreateCanvasSurface(id DisplayID, canvas image.Rectangle) (Surface, error)
}

// CanvasBounds returns the union in the host's virtual-desktop coordinate space.
// Empty/duplicate outputs and impractically large native surfaces are rejected,
// rather than silently displaying duplicate or misaligned wallpaper.
func CanvasBounds(displays []Display) (image.Rectangle, error) {
	if len(displays) == 0 {
		return image.Rectangle{}, fmt.Errorf("desktop: no displays")
	}
	var canvas image.Rectangle
	seen := map[DisplayID]bool{}
	for _, d := range displays {
		if d.ID == "" || seen[d.ID] || d.Bounds.Empty() {
			return image.Rectangle{}, fmt.Errorf("desktop: invalid display geometry or identity")
		}
		seen[d.ID] = true
		canvas = canvas.Union(d.Bounds)
	}
	if !validCanvasSize(canvas) {
		return image.Rectangle{}, fmt.Errorf("desktop: spanning canvas exceeds 32767 pixels per axis")
	}
	return canvas, nil
}

// CanvasFrame converts a top-down virtual canvas into the local coordinates of
// one native clipping viewport. Native dimensions may be points instead of
// pixels, and may differ between outputs. flipped=false is Cocoa's bottom-up Y.
func CanvasFrame(canvas, viewport image.Rectangle, width, height float64, flipped bool) (x, y, w, h float64, err error) {
	if !validCanvasSize(canvas) || viewport.Empty() || !viewport.In(canvas) || width <= 0 || height <= 0 || math.IsNaN(width) || math.IsNaN(height) || math.IsInf(width, 0) || math.IsInf(height, 0) {
		return 0, 0, 0, 0, fmt.Errorf("desktop: invalid canvas viewport")
	}
	sx, sy := width/float64(viewport.Dx()), height/float64(viewport.Dy())
	x = float64(canvas.Min.X-viewport.Min.X) * sx
	if flipped {
		y = float64(canvas.Min.Y-viewport.Min.Y) * sy
	} else {
		y = float64(viewport.Max.Y-canvas.Max.Y) * sy
	}
	return x, y, float64(canvas.Dx()) * sx, float64(canvas.Dy()) * sy, nil
}

// Unsigned differences reject overflow before any native or image arithmetic.
func validCanvasSize(r image.Rectangle) bool {
	if r.Empty() {
		return false
	}
	for _, n := range []int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} {
		if int64(n) < math.MinInt32 || int64(n) > math.MaxInt32 {
			return false
		}
	}
	return uint64(r.Max.X)-uint64(r.Min.X) <= 32767 && uint64(r.Max.Y)-uint64(r.Min.Y) <= 32767
}
