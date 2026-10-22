package mf

import "math"

// normalizedRect has the ABI of MFVideoNormalizedRect: four 32-bit floats in
// left, top, right, bottom order, with the full video represented by [0,0,1,1].
type normalizedRect struct {
	Left, Top, Right, Bottom float32
}

// fillSourceRect center-crops the picture aspect into the output aspect before
// Media Foundation applies its mandatory aspect-preserving destination fit.
// aspectW/aspectH must come from GetVideoAspectRatio, not encoded pixel size.
func fillSourceRect(aspectW, aspectH, outputW, outputH uint32) (normalizedRect, bool) {
	if aspectW == 0 || aspectH == 0 || outputW == 0 || outputH == 0 {
		return normalizedRect{}, false
	}
	r := normalizedRect{Right: 1, Bottom: 1}
	// uint64 products are exact and cannot overflow for uint32 dimensions.
	source, output := uint64(aspectW)*uint64(outputH), uint64(outputW)*uint64(aspectH)
	if source > output {
		fraction := float64(output) / float64(source)
		r.Left, r.Right = float32((1-fraction)/2), float32((1+fraction)/2)
	} else if source < output {
		fraction := float64(source) / float64(output)
		r.Top, r.Bottom = float32((1-fraction)/2), float32((1+fraction)/2)
	}
	// Extremely disproportionate untrusted geometry can collapse during the
	// required float32 conversion; never hand an empty crop to Media Foundation.
	return r, r.Left < r.Right && r.Top < r.Bottom
}

type videoGeometry struct {
	source        normalizedRect
	width, height uint32
}

// TransferVideoFrame always letterboxes. For stretch, render the entire picture
// into a picture-aspect backbuffer, then let DXGI_SCALING_STRETCH resize that
// backbuffer to the HWND. This is a real nonuniform GPU scale, not a fake crop.
// Common rational aspects are represented exactly; unusual ratios round at the
// backbuffer pixel boundary. No buffer is larger than the original canvas.
func frameGeometry(fit string, aspectW, aspectH, canvasW, canvasH uint32) (videoGeometry, bool) {
	g := videoGeometry{source: normalizedRect{Right: 1, Bottom: 1}, width: canvasW, height: canvasH}
	if aspectW == 0 || aspectH == 0 || canvasW == 0 || canvasH == 0 || canvasW > math.MaxInt32 || canvasH > math.MaxInt32 {
		return g, false
	}
	switch fit {
	case "fill":
		var ok bool
		g.source, ok = fillSourceRect(aspectW, aspectH, canvasW, canvasH)
		return g, ok
	case "fit":
		return g, true
	case "stretch":
		a, b := aspectW, aspectH
		for b != 0 {
			a, b = b, a%b
		}
		x, y := aspectW/a, aspectH/a
		if scale := min(canvasW/x, canvasH/y); scale > 0 {
			g.width, g.height = x*scale, y*scale
		} else if uint64(aspectW)*uint64(canvasH) > uint64(canvasW)*uint64(aspectH) {
			g.height = uint32(math.Round(float64(canvasW) * float64(aspectH) / float64(aspectW)))
		} else {
			g.width = uint32(math.Round(float64(canvasH) * float64(aspectW) / float64(aspectH)))
		}
		return g, g.width > 0 && g.height > 0
	default:
		return g, false
	}
}
