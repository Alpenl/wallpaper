//go:build darwin

package nativeav

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ebitengine/purego/objc"
	"wpapp/internal/media"
)

// Encode writes a new video-only H.264 MP4 at rational 30 fps. width/height are
// aspect-preserving even-pixel bounds by default; optional cover requests an
// exact center-cropped raster (and permits enlargement). It refuses an
// existing destination. Callers must atomically publish and verify the result.
//
// Run this in a killable helper process, never the application's UI process.
// AVAssetImageGenerator's sequential frame-accurate seeks prioritize correctness
// over throughput; synchronous native decode/metadata calls cannot be forcibly
// interrupted by a Go context. The process owner must enforce its deadline.
func Encode(ctx context.Context, src, dst string, width, height int, cover ...bool) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return err
	}
	dst, err = filepath.Abs(dst)
	if err != nil {
		return err
	}
	if src == dst {
		return errors.New("nativeav: source and destination must differ")
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > maxInputBytes {
		return errors.New("nativeav: source must be a regular file of at most 2 GiB")
	}
	if _, e := os.Lstat(dst); e == nil {
		return errors.New("nativeav: destination already exists")
	} else if !os.IsNotExist(e) {
		return e
	}
	info, err := media.ProbeFile(src)
	if err != nil {
		return err
	}
	if info.Container != "gif" && info.Container != "mp4" && info.Container != "mov" {
		return fmt.Errorf("nativeav: unsupported %s source; system encoder accepts GIF and AVFoundation-decodable MP4/MOV, not WebM/VP9", info.Container)
	}
	if info.Container != "gif" && (info.W < 2 || info.H < 2 || info.W > maxDimension || info.H > maxDimension || int64(info.W)*int64(info.H) > 32<<20) {
		return errors.New("nativeav: source video exceeds 8192-pixel/32-MiPixel decoder bounds or has missing dimensions")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err = loadNative(); err != nil {
		return err
	}
	pool := class("NSAutoreleasePool").Send(sel("alloc")).Send(sel("init"))
	defer pool.Send(sel("drain"))
	// Never remove a destination rejected above. After writer creation begins,
	// AVFoundation owns this fresh path and failures may remove only this output.
	if info.Container == "gif" {
		return encodeGIF(ctx, src, dst, width, height, len(cover) > 0 && cover[0])
	}
	return encodeVideo(ctx, src, dst, width, height, len(cover) > 0 && cover[0])
}

func encodeGIF(ctx context.Context, src, dst string, bw, bh int, cover bool) error {
	g, err := loadGIF(ctx, src)
	if err != nil {
		return err
	}
	frames, err := newGIFFrames(g)
	if err != nil {
		return err
	}
	width, height, err := renditionSize(g.Config.Width, g.Config.Height, bw, bh, cover)
	if err != nil {
		return err
	}
	count, err := frameCount(frames.duration(), 100)
	if err != nil {
		return err
	}
	w, err := newWriter(ctx, dst, width, height)
	if err != nil {
		return err
	}
	defer w.close()
	w.cover = cover
	for i := 0; i < count; i++ {
		im, e := frames.at(ctx, i)
		if e != nil {
			return e
		}
		if e = w.appendRGBA(ctx, im, i); e != nil {
			return e
		}
	}
	return w.finish(ctx, count)
}

func encodeVideo(ctx context.Context, src, dst string, bw, bh int, cover bool) error {
	url := class("NSURL").Send(sel("fileURLWithPath:"), ns(src))
	asset := class("AVURLAsset").Send(sel("alloc")).Send(sel("initWithURL:options:"), url, objc.ID(0))
	if asset == 0 {
		return errors.New("nativeav: cannot open video asset")
	}
	defer asset.Send(sel("release"))
	if objc.Send[bool](asset, sel("hasProtectedContent")) {
		return errors.New("nativeav: protected media is unsupported")
	}
	tracks := asset.Send(sel("tracksWithMediaType:"), constants["AVMediaTypeVideo"])
	if objc.Send[uintptr](tracks, sel("count")) != 1 {
		return errors.New("nativeav: source must contain exactly one video track")
	}
	track := tracks.Send(sel("objectAtIndex:"), uintptr(0))
	tr := objc.Send[cmTimeRange](track, sel("timeRange"))
	if !validTime(tr.Start) || !validTime(tr.Duration) {
		return errors.New("nativeav: video has indefinite or invalid time range")
	}
	count, err := frameCount(tr.Duration.Value, tr.Duration.Timescale)
	if err != nil {
		return err
	}
	gen := class("AVAssetImageGenerator").Send(sel("alloc")).Send(sel("initWithAsset:"), asset)
	if gen == 0 {
		return errors.New("nativeav: cannot create video frame generator")
	}
	defer gen.Send(sel("release"))
	gen.Send(sel("setAppliesPreferredTrackTransform:"), true)
	msgSetTime(gen, sel("setRequestedTimeToleranceBefore:"), frameTime(0))
	msgSetTime(gen, sel("setRequestedTimeToleranceAfter:"), frameTime(0))
	// Bounding before the first decode avoids a full-resolution CGImage allocation.
	// AVFoundation honors display orientation and the clean aperture/aspect ratio.
	mw, mh := bw, bh
	if mw <= 0 {
		mw = 3840
	}
	if mh <= 0 {
		mh = 2160
	}
	// Never rely on maximumSize to avoid upscaling: explicitly bound it by
	// the native track's oriented dimensions before decoding the first image.
	natural := objc.Send[cgSize](track, sel("naturalSize"))
	transform := objc.Send[cgAffineTransform](track, sel("preferredTransform"))
	displayW := math.Abs(transform.A)*natural.Width + math.Abs(transform.C)*natural.Height
	displayH := math.Abs(transform.B)*natural.Width + math.Abs(transform.D)*natural.Height
	if math.IsNaN(displayW) || math.IsInf(displayW, 0) || math.IsNaN(displayH) || math.IsInf(displayH, 0) || displayW < 2 || displayH < 2 || displayW > 32768 || displayH > 32768 {
		return errors.New("nativeav: invalid oriented track dimensions")
	}
	if cover {
		scale := min(1.0, max(float64(bw)/displayW, float64(bh)/displayH))
		mw, mh = int(math.Ceil(displayW*scale)), int(math.Ceil(displayH*scale))
	}
	mw = min(max(mw, 2), maxDimension, int(displayW))
	mh = min(max(mh, 2), maxDimension, int(displayH))
	if int64(mw)*int64(mh) > maxCanvasPixels {
		mw, mh, _ = outputSize(mw, mh, mw, mh)
	}
	msgSetSize(gen, sel("setMaximumSize:"), cgSize{float64(mw), float64(mh)})
	var actual cmTime
	var failure objc.ID
	first := msgCopyImage(gen, sel("copyCGImageAtTime:actualTime:error:"), tr.Start, &actual, &failure)
	if first == 0 {
		return nativeError("cannot decode first video frame (codec may be unsupported)", failure)
	}
	firstOwned := true
	defer func() {
		if firstOwned {
			cgImageRelease(first)
		}
	}()
	fw, fh := cgImageGetWidth(first), cgImageGetHeight(first)
	if fw > 32768 || fh > 32768 {
		return errors.New("nativeav: decoded frame exceeds source bounds")
	}
	width, height, err := renditionSize(int(fw), int(fh), bw, bh, cover)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	w, err := newWriter(ctx, dst, width, height)
	if err != nil {
		return err
	}
	defer w.close()
	w.cover = cover
	if err = w.appendImage(ctx, first, 0); err != nil {
		return err
	}
	cgImageRelease(first)
	firstOwned = false
	for i := 1; i < count; i++ {
		if err = ctx.Err(); err != nil {
			gen.Send(sel("cancelAllCGImageGeneration"))
			return err
		}
		// Drain per-frame autoreleases; retained CGImage and writer objects have
		// explicit ownership independent of this short-lived pool.
		framePool := class("NSAutoreleasePool").Send(sel("alloc")).Send(sel("init"))
		failure = 0
		image := msgCopyImage(gen, sel("copyCGImageAtTime:actualTime:error:"), cmTimeAdd(tr.Start, frameTime(i)), &actual, &failure)
		if image == 0 {
			err = nativeError(fmt.Sprintf("cannot decode video frame %d", i), failure)
		} else {
			err = w.appendImage(ctx, image, i)
			cgImageRelease(image)
		}
		framePool.Send(sel("drain"))
		if err != nil {
			return err
		}
	}
	return w.finish(ctx, count)
}
