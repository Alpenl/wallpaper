package mf

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
