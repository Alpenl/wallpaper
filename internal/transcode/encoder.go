package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"wpapp/internal/media"
)

// ErrNoEncoder does not trigger downloads or silently substitute another path.
var ErrNoEncoder = errors.New("transcode: no compatible encoder; install ffmpeg and select its executable in Playback & Performance")

// AvconvertPath is the limited-capability encoder supplied with macOS.
const AvconvertPath = "/usr/bin/avconvert"

// Target is a bounding box in physical pixels. Aspect ratio is preserved, output
// dimensions are even, and smaller sources are not upscaled. Zero uses 3840x2160.
// It does not crop, so a rendition remains usable with every playback fit mode.
type Target struct{ Width, Height int }

func (t Target) normalized() Target {
	if t.Width <= 0 {
		t.Width = 3840
	}
	if t.Height <= 0 {
		t.Height = 2160
	}
	// Bound allocator/encoder input even when a caller passes corrupt settings.
	t.Width = max(2, min(t.Width, 8192)/2*2)
	t.Height = max(2, min(t.Height, 8192)/2*2)
	return t
}

// Capabilities describe the selected route, not a claim of hardware availability.
// Strict output is checked after encoding; a missing codec or driver is an error.
type Capabilities struct {
	Available, Strict, GIF, TargetSize, FPS30, RemovesAudio bool
	Name                                                    string
}

// CapabilitiesFor honors a configured path even if it is invalid. Only an empty
// setting allows PATH discovery and then the limited macOS system fallback.
func CapabilitiesFor(goos, ffmpegPath string) Capabilities {
	return CapabilitiesForWithHelper(goos, ffmpegPath, "")
}

// CapabilitiesForWithHelper only accepts the caller's known absolute application
// executable, never a PATH-discovered helper. The caller enables this route only
// when the nativeav package reports that its platform runtime is available.
func CapabilitiesForWithHelper(goos, ffmpegPath, helperExecutable string) Capabilities {
	_, kind := resolveWithHelper(goos, ffmpegPath, helperExecutable)
	switch kind {
	case "ffmpeg":
		name := "ffmpeg/libx264（用户安装，软件编码）"
		if goos == "darwin" {
			name = "ffmpeg + VideoToolbox（用户安装）"
		}
		if goos == "windows" {
			name = "ffmpeg + Media Foundation（用户安装）"
		}
		return Capabilities{true, true, true, true, true, true, name}
	case "nativeav":
		return Capabilities{true, true, true, true, true, true, "AVFoundation（系统原生，独立转码进程）"}
	case "avconvert":
		return Capabilities{Available: true, Name: "avconvert（系统自带，有限能力；不支持 GIF/固定帧率/去音轨）"}
	}
	return Capabilities{}
}

func executable(path string) string {
	p, err := exec.LookPath(path)
	if err != nil {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0111 == 0 {
		return ""
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return ""
	}
	return p
}

func resolve(goos, path string) (string, string) { return resolveWithHelper(goos, path, "") }

func resolveWithHelper(goos, path, helperExecutable string) (string, string) {
	if strings.TrimSpace(path) != "" {
		if p := executable(strings.TrimSpace(path)); p != "" {
			return p, "ffmpeg"
		}
		return "", "" // Explicit bad configuration is never silently ignored.
	}
	if goos == "darwin" && helperExecutable != "" {
		if filepath.IsAbs(helperExecutable) {
			if p := executable(helperExecutable); p != "" {
				return p, "nativeav"
			}
		}
		return "", "" // A broken built-in helper must not be hidden by a fallback.
	}
	if p := executable("ffmpeg"); p != "" {
		return p, "ffmpeg"
	}
	if goos == "darwin" {
		if p := executable(AvconvertPath); p != "" {
			return p, "avconvert"
		}
	}
	return "", ""
}

// System retains the native macOS fallback. It cannot implement the strict F-07
// contract and rejects GIF before invoking avconvert. Callers must label it as
// limited rather than promising 30fps, no audio, or target-size conversion.
func System(goos string) func(context.Context, string, string) error {
	if goos != "darwin" {
		return nil
	}
	p := executable(AvconvertPath)
	if p == "" {
		return nil
	}
	return avconvertEncoder(p)
}

// Encoder keeps the original API and defaults to a 4K bounding box.
func Encoder(goos, ffmpegPath string) (func(context.Context, string, string) error, string, string) {
	return EncoderForTarget(goos, ffmpegPath, Target{})
}

// EncoderForTarget returns a cancellable, serialized encoder. Configured paths
// take precedence over every system fallback. Optional ffmpeg is never bundled.
func EncoderForTarget(goos, ffmpegPath string, target Target) (func(context.Context, string, string) error, string, string) {
	return EncoderForTargetWithHelper(goos, ffmpegPath, "", target)
}

// EncoderForTargetWithHelper prefers the built-in isolated native route on
// macOS unless the user explicitly selects ffmpeg. A helper dispatch must call
// nativeav.Encode directly, never this function, to avoid recursive spawning.
func EncoderForTargetWithHelper(goos, ffmpegPath, helperExecutable string, target Target) (func(context.Context, string, string) error, string, string) {
	p, kind := resolveWithHelper(goos, ffmpegPath, helperExecutable)
	caps := CapabilitiesForWithHelper(goos, ffmpegPath, helperExecutable)
	switch kind {
	case "nativeav":
		return nativeHelperEncoder(p, target.normalized()), ".mp4", caps.Name
	case "avconvert":
		return avconvertEncoder(p), ".mov", caps.Name
	case "ffmpeg":
		target = target.normalized()
		return serialEncoder(func(ctx context.Context, src, dst string) error {
			ctx, cancel := context.WithTimeout(ctx, time.Hour)
			defer cancel()
			return encodeAtomic(ctx, src, dst, ".mp4", func(src, tmp string) error {
				codec, err := detectCodec(ctx, p, goos)
				if err != nil {
					return err
				}
				if err := run(ctx, p, ffmpegArgs(src, tmp, target, codec)...); err != nil {
					return fmt.Errorf("ffmpeg (%s): %w", codec, err)
				}
				if _, err := VerifyOutput(tmp, target); err != nil {
					return err
				}
				return nil
			})
		}), ".mp4", caps.Name
	}
	return nil, "", ""
}

func nativeHelperEncoder(executable string, target Target) func(context.Context, string, string) error {
	return serialEncoder(func(ctx context.Context, src, dst string) error {
		ctx, cancel := context.WithTimeout(ctx, time.Hour)
		defer cancel()
		return encodeAtomic(ctx, src, dst, ".mp4", func(src, tmp string) error {
			// AVAssetWriter requires a nonexistent destination. This is only our
			// newly-created private temporary file, never the user's destination.
			if err := os.Remove(tmp); err != nil {
				return err
			}
			if err := run(ctx, executable, "-helper", "transcode", "-in", src, "-out", tmp,
				"-width", strconv.Itoa(target.Width), "-height", strconv.Itoa(target.Height)); err != nil {
				return fmt.Errorf("native AVFoundation helper: %w", err)
			}
			_, err := VerifyOutput(tmp, target)
			return err
		})
	})
}

func avconvertEncoder(p string) func(context.Context, string, string) error {
	return serialEncoder(func(ctx context.Context, src, dst string) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		return encodeAtomic(ctx, src, dst, ".mov", func(src, tmp string) error {
			if isGIF(src) {
				return errors.New("avconvert cannot convert GIF; select a user-installed ffmpeg with h264_videotoolbox in Playback & Performance")
			}
			if err := run(ctx, p, "--preset", "PresetHEVCHighestQuality", "--source", src, "--output", tmp, "--replace"); err != nil {
				return fmt.Errorf("avconvert (limited system fallback): %w", err)
			}
			in, err := media.ProbeFile(tmp)
			if err != nil {
				return fmt.Errorf("avconvert output verification: %w", err)
			}
			if in.Kind != "video" || (in.Codec != "h264" && in.Codec != "hevc") || in.W < 2 || in.H < 2 || in.DurationSec <= 0 {
				return errors.New("avconvert output is not a playable H.264/HEVC video")
			}
			return nil
		})
	})
}

// encodeAtomic preserves an existing destination on all failure/cancel paths.
// User media must be a local regular file, never a network URL or device.
func encodeAtomic(ctx context.Context, src, dst, ext string, encode func(string, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	st, err := os.Stat(absSrc)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("transcode: source must be a regular local file")
	}
	if absSrc == absDst {
		return errors.New("transcode: destination must differ from source")
	}
	if d, err := os.Stat(absDst); err == nil && os.SameFile(st, d) {
		return errors.New("transcode: destination refers to source")
	}
	f, err := os.CreateTemp(filepath.Dir(absDst), ".wallpaper-transcode-*"+ext)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Close(); err != nil {
		return err
	}
	if err := encode(absSrc, tmp); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmp, absDst)
}

func isGIF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [6]byte
	n, _ := f.Read(b[:])
	return n == 6 && (string(b[:]) == "GIF87a" || string(b[:]) == "GIF89a")
}

// FFmpegArgs is retained for callers/tests using the software encoder. Strict
// macOS/Windows selection is performed at execution time by EncoderForTarget.
func FFmpegArgs(src, dst string, maxW int) []string {
	return ffmpegArgs(src, dst, Target{Width: maxW}.normalized(), "libx264")
}

func ffmpegArgs(src, dst string, target Target, codec string) []string {
	target = target.normalized()
	// Use display aspect ratio, not only stored width, for anamorphic sources.
	filter := fmt.Sprintf("fps=30,scale=w='max(2,trunc(min(min(%d,iw*sar),%d*dar)/2)*2)':h='max(2,trunc(min(min(%d,ih),%d/dar)/2)*2)':flags=bicubic,setsar=1", target.Width, target.Height, target.Height, target.Width)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-max_alloc", "268435456", "-threads", "2", "-filter_threads", "1", "-protocol_whitelist", "file,pipe"}
	if isGIF(src) {
		args = append(args, "-ignore_loop", "1")
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-an", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-vf", filter,
		"-c:v", codec, "-profile:v", "high", "-pix_fmt", "yuv420p", "-threads", "2", "-r", "30", "-g", "30", "-bf", "0")
	bitrate := "25000000"
	if target.Width*target.Height <= 1920*1080 {
		bitrate = "8000000"
	} else if target.Width*target.Height <= 2560*1440 {
		bitrate = "14000000"
	}
	switch codec {
	case "h264_videotoolbox":
		// System VideoToolbox may use its own software fallback. Never describe
		// this as verified hardware acceleration merely because VT is selected.
		args = append(args, "-allow_sw", "1", "-b:v", bitrate)
	case "h264_mf":
		args = append(args, "-b:v", bitrate, "-scenario", "camera_record")
	default:
		args = append(args, "-preset", "medium", "-crf", "20", "-keyint_min", "30", "-sc_threshold", "0", "-maxrate", bitrate, "-bufsize", "50000000", "-flags", "+cgop")
	}
	return append(args, "-movflags", "+faststart", "-f", "mp4", dst)
}

func detectCodec(ctx context.Context, path, goos string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out boundedLog
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-encoders")
	hide(cmd)
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("cannot inspect configured ffmpeg: %w: %s", err, out.String())
	}
	return selectCodec(goos, out.String())
}

func selectCodec(goos, output string) (string, error) {
	available := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && len(fields[0]) == 6 && fields[0][0] == 'V' {
			available[fields[1]] = true
		}
	}
	if goos == "darwin" {
		if available["h264_videotoolbox"] {
			return "h264_videotoolbox", nil
		}
		return "", errors.New("configured ffmpeg lacks h264_videotoolbox; select a macOS build with VideoToolbox support (no silent avconvert/software substitution)")
	}
	if goos == "windows" {
		if available["h264_mf"] {
			return "h264_mf", nil
		}
		return "", errors.New("configured ffmpeg lacks h264_mf; select a Windows build with Media Foundation support (no silent libx264 substitution)")
	}
	if available["libx264"] {
		return "libx264", nil
	}
	return "", fmt.Errorf("%w: this ffmpeg lacks a compatible H.264 encoder", ErrNoEncoder)
}

const maxLog = 64 << 10

type boundedLog struct{ bytes []byte }

func (l *boundedLog) Write(p []byte) (int, error) {
	n := len(p)
	if n >= maxLog {
		l.bytes = append(l.bytes[:0], p[n-maxLog:]...)
		return n, nil
	}
	if len(l.bytes)+n > maxLog {
		l.bytes = append(l.bytes[:0], l.bytes[len(l.bytes)+n-maxLog:]...)
	}
	l.bytes = append(l.bytes, p...)
	return n, nil
}
func (l *boundedLog) String() string { return string(l.bytes) }

func run(ctx context.Context, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	hide(cmd)
	cmd.WaitDelay = time.Second
	var out boundedLog
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", err, tail(out.bytes, 2000))
	}
	return nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
