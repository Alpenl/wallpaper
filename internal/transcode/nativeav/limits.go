// Package nativeav is the macOS system-only H.264/MP4 encoder. Invoke Encode in
// an isolated helper process: native media decoders are not a security sandbox.
package nativeav

import (
	"errors"
	"fmt"
	"math"
)

const (
	FPS                 = 30
	maxDurationSeconds  = 30 * 60
	maxOutputFrames     = FPS * maxDurationSeconds
	maxInputBytes       = int64(2 << 30)
	maxGIFBytes         = 64 << 20
	maxGIFFrames        = 10000
	maxGIFDecodedPixels = 128 << 20
	maxCanvasPixels     = 16 << 20
	maxDimension        = 8192
)

var ErrUnavailable = errors.New("nativeav: AVFoundation encoding is only available on macOS")

// outputSize fits the source into a bounding box without enlarging it. The
// independent even-pixel rounding introduces at most one pixel of aspect error.
func outputSize(w, h, boundW, boundH int) (int, int, error) {
	if w < 2 || h < 2 || w > 32768 || h > 32768 {
		return 0, 0, errors.New("nativeav: invalid source dimensions")
	}
	if boundW <= 0 {
		boundW = 3840
	}
	if boundH <= 0 {
		boundH = 2160
	}
	boundW = max(2, min(boundW, maxDimension)/2*2)
	boundH = max(2, min(boundH, maxDimension)/2*2)
	scale := min(1.0, float64(boundW)/float64(w), float64(boundH)/float64(h))
	scale = min(scale, math.Sqrt(float64(maxCanvasPixels)/(float64(w)*float64(h))))
	ow, oh := int(float64(w)*scale)/2*2, int(float64(h)*scale)/2*2
	if ow < 2 || oh < 2 {
		return 0, 0, errors.New("nativeav: source aspect ratio cannot fit even output bounds")
	}
	return ow, oh, nil
}

// frameCount rounds the duration up to the next complete 1/30-second frame.
// Integer arithmetic avoids drift and rejects values before multiplying.
func frameCount(value int64, timescale int32) (int, error) {
	if value <= 0 || timescale <= 0 || value > int64(timescale)*maxDurationSeconds {
		return 0, fmt.Errorf("nativeav: duration must be positive and at most %d seconds", maxDurationSeconds)
	}
	n := (value*FPS + int64(timescale) - 1) / int64(timescale)
	if n < 1 || n > maxOutputFrames {
		return 0, errors.New("nativeav: frame budget exceeded")
	}
	return int(n), nil
}

func gifDelay(cs int) int {
	// GIF allows zero delay. Give it a finite, documented 100 ms display time;
	// preserve every positive centisecond delay, including 10 ms and 20 ms.
	if cs <= 0 {
		return 10
	}
	return cs
}

// renditionSize separates exact cover targets from aspect-preserving bounds.
func renditionSize(w, h, bw, bh int, cover bool) (int, int, error) {
	if !cover {
		return outputSize(w, h, bw, bh)
	}
	if w < 2 || h < 2 || w > 32768 || h > 32768 || bw < 2 || bh < 2 || bw > maxDimension || bh > maxDimension || int64(bw)*int64(bh) > maxCanvasPixels {
		return 0, 0, errors.New("nativeav: cover dimensions exceed source or canvas bounds")
	}
	return bw / 2 * 2, bh / 2 * 2, nil
}
