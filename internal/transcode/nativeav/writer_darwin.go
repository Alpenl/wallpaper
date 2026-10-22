//go:build darwin

package nativeav

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"os"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"
	xdraw "golang.org/x/image/draw"
)

const (
	pixelFormatBGRA                  = 0x42475241
	bitmapByteOrder32Little          = 2 << 12
	bitmapAlphaNoneSkipFirst         = 6
	cvWouldExceedAllocationThreshold = -6689
)

type writer struct {
	object, input, adaptor, aux objc.ID
	pool, colorSpace            uintptr
	width, height               int
	scratch                     *image.RGBA
	path                        string
	owned                       os.FileInfo
	complete                    bool
}

func newWriter(ctx context.Context, path string, width, height int) (_ *writer, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	w := &writer{width: width, height: height, path: path}
	defer func() {
		if err != nil {
			w.close()
		}
	}()
	// Only one video input is ever created; no source audio, metadata, subtitle,
	// or data track can accidentally pass through to the result.
	compression := dict(
		constants["AVVideoAverageBitRateKey"], number(int64(min(25000000, max(1500000, width*height*4)))),
		constants["AVVideoExpectedSourceFrameRateKey"], number(FPS),
		constants["AVVideoMaxKeyFrameIntervalKey"], number(FPS),
		constants["AVVideoMaxKeyFrameIntervalDurationKey"], number(1),
		constants["AVVideoAllowFrameReorderingKey"], boolean(false),
		constants["AVVideoProfileLevelKey"], constants["AVVideoProfileLevelH264HighAutoLevel"],
	)
	settings := dict(constants["AVVideoCodecKey"], constants["AVVideoCodecTypeH264"], constants["AVVideoWidthKey"], number(int64(width)), constants["AVVideoHeightKey"], number(int64(height)), constants["AVVideoCompressionPropertiesKey"], compression)
	url := class("NSURL").Send(sel("fileURLWithPath:"), ns(path))
	var failure objc.ID
	w.object = class("AVAssetWriter").Send(sel("alloc")).Send(sel("initWithURL:fileType:error:"), url, constants["AVFileTypeMPEG4"], &failure)
	if w.object == 0 {
		return nil, nativeError("cannot create MP4 writer", failure)
	}
	if !objc.Send[bool](w.object, sel("canApplyOutputSettings:forMediaType:"), settings, constants["AVMediaTypeVideo"]) {
		return nil, errors.New("nativeav: system does not support requested H.264 settings")
	}
	w.input = class("AVAssetWriterInput").Send(sel("alloc")).Send(sel("initWithMediaType:outputSettings:"), constants["AVMediaTypeVideo"], settings)
	if w.input == 0 {
		return nil, errors.New("nativeav: cannot create H.264 writer input")
	}
	w.input.Send(sel("setExpectsMediaDataInRealTime:"), false)
	w.input.Send(sel("setMediaTimeScale:"), int32(600))
	if !objc.Send[bool](w.object, sel("canAddInput:"), w.input) {
		return nil, errors.New("nativeav: cannot add video writer input")
	}
	w.object.Send(sel("addInput:"), w.input)
	attrs := dict(constants["kCVPixelBufferPixelFormatTypeKey"], number(pixelFormatBGRA), constants["kCVPixelBufferWidthKey"], number(int64(width)), constants["kCVPixelBufferHeightKey"], number(int64(height)), constants["kCVPixelBufferCGImageCompatibilityKey"], boolean(true), constants["kCVPixelBufferCGBitmapContextCompatibilityKey"], boolean(true), constants["kCVPixelBufferIOSurfacePropertiesKey"], dict())
	w.adaptor = class("AVAssetWriterInputPixelBufferAdaptor").Send(sel("alloc")).Send(sel("initWithAssetWriterInput:sourcePixelBufferAttributes:"), w.input, attrs)
	if w.adaptor == 0 {
		return nil, errors.New("nativeav: cannot create pixel buffer adaptor")
	}
	w.aux = dict(constants["kCVPixelBufferPoolAllocationThresholdKey"], number(4)).Send(sel("retain"))
	w.colorSpace = cgColorSpaceCreateDeviceRGB()
	if w.colorSpace == 0 {
		return nil, errors.New("nativeav: cannot create RGB color space")
	}
	if !objc.Send[bool](w.object, sel("startWriting")) {
		return nil, w.failure("cannot start MP4 writer")
	}
	w.owned, _ = os.Lstat(path)
	msgSetTime(w.object, sel("startSessionAtSourceTime:"), frameTime(0))
	// Per Apple, the adaptor's pool is unavailable until the session has started.
	w.pool = objc.Send[uintptr](w.adaptor, sel("pixelBufferPool"))
	if w.pool == 0 {
		return nil, w.failure("writer did not create a pixel buffer pool")
	}
	return w, nil
}
func (w *writer) failure(prefix string) error {
	return nativeError(prefix, w.object.Send(sel("error")))
}
func (w *writer) close() {
	if w.object != 0 && !w.complete {
		w.object.Send(sel("cancelWriting"))
	}
	if w.colorSpace != 0 {
		cgColorSpaceRelease(w.colorSpace)
		w.colorSpace = 0
	}
	for _, p := range []objc.ID{w.aux, w.adaptor, w.input, w.object} {
		if p != 0 {
			p.Send(sel("release"))
		}
	}
	w.aux = 0
	w.adaptor = 0
	w.input = 0
	w.object = 0
	if !w.complete && w.owned != nil {
		if now, e := os.Lstat(w.path); e == nil && os.SameFile(now, w.owned) {
			_ = os.Remove(w.path)
		}
	}
}

func waitTick(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (w *writer) buffer(ctx context.Context) (uintptr, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if err := waitCtx.Err(); err != nil {
			return 0, fmt.Errorf("nativeav: waiting for encoder buffer: %w", err)
		}
		if objc.Send[int64](w.object, sel("status")) != 1 {
			return 0, w.failure("writer stopped before accepting a frame")
		}
		if objc.Send[bool](w.input, sel("isReadyForMoreMediaData")) {
			var p uintptr
			status := cvPoolCreatePixelBuffer(0, w.pool, uintptr(w.aux), &p)
			if status == 0 && p != 0 {
				return p, nil
			}
			if p != 0 {
				cfRelease(p)
			}
			if status != cvWouldExceedAllocationThreshold {
				return 0, fmt.Errorf("nativeav: cannot allocate pixel buffer (%d)", status)
			}
		}
		if err := waitTick(waitCtx); err != nil {
			return 0, err
		}
	}
}
func (w *writer) locked(p uintptr) ([]byte, int, error) {
	if status := cvPixelBufferLockBaseAddress(p, 0); status != 0 {
		return nil, 0, fmt.Errorf("nativeav: cannot lock pixel buffer (%d)", status)
	}
	stride, h, base := cvPixelBufferGetBytesPerRow(p), cvPixelBufferGetHeight(p), cvPixelBufferGetBaseAddress(p)
	if base == 0 || h != uintptr(w.height) || stride < uintptr(w.width*4) || stride > uintptr(w.width*4+65536) || stride*h > 256<<20 {
		cvPixelBufferUnlockBaseAddress(p, 0)
		return nil, 0, errors.New("nativeav: invalid pixel buffer layout")
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(base)), int(stride*h)), int(stride), nil
}
func (w *writer) append(ctx context.Context, p uintptr, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !msgAppendPixelBuffer(w.adaptor, sel("appendPixelBuffer:withPresentationTime:"), p, frameTime(n)) {
		return w.failure("cannot append encoded frame")
	}
	return nil
}
func (w *writer) appendImage(ctx context.Context, im uintptr, n int) error {
	p, err := w.buffer(ctx)
	if err != nil {
		return err
	}
	defer cfRelease(p)
	b, stride, err := w.locked(p)
	if err != nil {
		return err
	}
	clear(b)
	// BGRA bytes with an ignored alpha channel form an opaque black canvas. Core
	// Graphics composites source transparency over that canvas and scales it.
	cg := cgBitmapContextCreate(cvPixelBufferGetBaseAddress(p), uintptr(w.width), uintptr(w.height), 8, uintptr(stride), w.colorSpace, bitmapByteOrder32Little|bitmapAlphaNoneSkipFirst)
	if cg == 0 {
		cvPixelBufferUnlockBaseAddress(p, 0)
		return errors.New("nativeav: cannot create pixel buffer drawing context")
	}
	cgContextSetInterpolationQuality(cg, 3) // kCGInterpolationHigh
	cgContextDrawImage(cg, cgRect{Size: cgSize{float64(w.width), float64(w.height)}}, im)
	cgContextRelease(cg)
	if status := cvPixelBufferUnlockBaseAddress(p, 0); status != 0 {
		return fmt.Errorf("nativeav: cannot unlock pixel buffer (%d)", status)
	}
	return w.append(ctx, p, n)
}
func (w *writer) appendRGBA(ctx context.Context, im *image.RGBA, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src := im
	if im.Bounds().Dx() != w.width || im.Bounds().Dy() != w.height {
		if w.scratch == nil {
			w.scratch = image.NewRGBA(image.Rect(0, 0, w.width, w.height))
		}
		xdraw.BiLinear.Scale(w.scratch, w.scratch.Bounds(), im, im.Bounds(), draw.Src, nil)
		src = w.scratch
	}
	p, err := w.buffer(ctx)
	if err != nil {
		return err
	}
	defer cfRelease(p)
	b, stride, err := w.locked(p)
	if err != nil {
		return err
	}
	// image.RGBA stores premultiplied RGB, so dropping alpha flattens it on black.
	// Use the actual CVPixelBuffer stride, which may include alignment padding.
	for y := 0; y < w.height; y++ {
		if y%64 == 0 {
			if err := ctx.Err(); err != nil {
				cvPixelBufferUnlockBaseAddress(p, 0)
				return err
			}
		}
		in := src.Pix[y*src.Stride : y*src.Stride+w.width*4]
		out := b[y*stride : y*stride+w.width*4]
		for x := 0; x < len(in); x += 4 {
			out[x] = in[x+2]
			out[x+1] = in[x+1]
			out[x+2] = in[x]
			out[x+3] = 255
		}
	}
	if status := cvPixelBufferUnlockBaseAddress(p, 0); status != 0 {
		return fmt.Errorf("nativeav: cannot unlock GIF pixel buffer (%d)", status)
	}
	return w.append(ctx, p, n)
}
func (w *writer) finish(ctx context.Context, count int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Explicit end is one full tick AFTER the final PTS. Otherwise the writer can
	// give the last sample zero duration, violating constant frame rate metadata.
	msgSetTime(w.object, sel("endSessionAtSourceTime:"), frameTime(count))
	w.input.Send(sel("markAsFinished"))
	done := make(chan struct{})
	block := objc.NewBlock(func(_ objc.Block) { close(done) })
	defer block.Release()
	w.object.Send(sel("finishWritingWithCompletionHandler:"), block)
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	select {
	case <-waitCtx.Done():
		return fmt.Errorf("nativeav: finishing MP4: %w", waitCtx.Err())
	case <-done:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if objc.Send[int64](w.object, sel("status")) != 2 {
		return w.failure("MP4 writing did not complete")
	}
	w.complete = true
	return nil
}
