package transcode

import (
	"fmt"
	"math"

	"wpapp/internal/media"
)

// VerifyOutput checks the produced MP4, rather than trusting command arguments
// or process exit status. It only inspects the encoder's local output. Original
// untrusted imports continue to be probed in the application's helper process.
// Sync samples are checked here; real ffprobe tests additionally verify key-frame
// flags. These flags alone are not a complete H.264 bitstream/IDR audit.
func VerifyOutput(path string, target Target) (*media.Info, error) {
	in, err := media.ProbeFile(path)
	if err != nil {
		return nil, fmt.Errorf("transcode output verification: %w", err)
	}
	return VerifyInfo(in, target)
}

// VerifyInfo also allows callers to validate metadata from an isolated helper.
func VerifyInfo(in *media.Info, target Target) (*media.Info, error) {
	target = target.normalized()
	bad := func(reason string) (*media.Info, error) {
		return nil, fmt.Errorf("transcode output verification: %s", reason)
	}
	if in == nil || in.Kind != "video" || in.Container != "mp4" || in.Codec != "h264" || in.VideoTracks != 1 {
		return bad("expected one H.264 MP4 video track")
	}
	if in.HasAudio {
		return bad("unexpected audio track")
	}
	if in.W < 2 || in.H < 2 || in.W%2 != 0 || in.H%2 != 0 || in.W > target.Width || in.H > target.Height {
		return bad("dimensions do not match the requested even-pixel target bounds")
	}
	if math.IsNaN(in.FPS) || math.IsInf(in.FPS, 0) || math.Abs(in.FPS-30) > 0.01 || !in.ConstantFrameRate {
		return bad("expected constant 30fps")
	}
	if in.DurationSec <= 0 || math.IsNaN(in.DurationSec) || math.IsInf(in.DurationSec, 0) || in.FrameCount == 0 {
		return bad("empty or invalid video duration")
	}
	if math.Abs(in.DurationSec-float64(in.FrameCount)/30) > 1.0/30 {
		return bad("track duration does not match frame count at 30fps")
	}
	if !in.StartsWithKeyframe || in.MaxKeyframeInterval == 0 || in.MaxKeyframeInterval > 30 {
		return bad("expected a first-frame sync sample and at most 30 frames between sync samples")
	}
	return in, nil
}
