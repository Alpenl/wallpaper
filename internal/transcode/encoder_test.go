package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wpapp/internal/media"
)

func TestExplicitPathNeverFallsBack(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		fn, ext, name := Encoder(goos, filepath.Join(t.TempDir(), "missing-ffmpeg"))
		if fn != nil || ext != "" || name != "" || CapabilitiesFor(goos, "/missing/ffmpeg").Available {
			t.Fatal(goos, "invalid path silently fell back")
		}
	}
	if fn, _, _ := Encoder("linux", t.TempDir()); fn != nil {
		t.Fatal("directory accepted as encoder")
	}
}

func TestCodecSelectionIsExplicit(t *testing.T) {
	all := " V....D libx264 test\n V..... h264_mf test\n V....D h264_videotoolbox test\n"
	for goos, want := range map[string]string{"linux": "libx264", "windows": "h264_mf", "darwin": "h264_videotoolbox"} {
		if got, err := selectCodec(goos, all); err != nil || got != want {
			t.Fatal(goos, got, err)
		}
	}
	if _, err := selectCodec("windows", " V....D libx264 test"); err == nil {
		t.Fatal("Windows silently substituted libx264")
	}
	if _, err := selectCodec("darwin", " V....D libx264 test"); err == nil {
		t.Fatal("macOS silently substituted software")
	}
	if _, err := selectCodec("linux", "libx264 (not a video encoder line)"); err == nil {
		t.Fatal("incorrect encoder listing accepted")
	}
}

func TestFFmpegArgsPlatformSpecific(t *testing.T) {
	for _, codec := range []string{"libx264", "h264_mf", "h264_videotoolbox"} {
		args := strings.Join(ffmpegArgs("source.mp4", "out.mp4", Target{2560, 1440}, codec), " ")
		for _, want := range []string{"-nostdin", "-map 0:v:0", "-an -sn -dn", "-g 30", "-bf 0", "-protocol_whitelist file,pipe", "2560", "1440", "-f mp4"} {
			if !strings.Contains(args, want) {
				t.Fatalf("%s missing %s: %s", codec, want, args)
			}
		}
		if codec != "libx264" && strings.Contains(args, "-crf") {
			t.Fatal("CRF passed to system encoder")
		}
	}
	if got := (Target{1, 9999999}).normalized(); got.Width != 2 || got.Height != 8192 {
		t.Fatal(got)
	}
}

func TestBoundedLog(t *testing.T) {
	var b boundedLog
	for i := 0; i < 100; i++ {
		b.Write([]byte(strings.Repeat("a", 1000)))
	}
	b.Write([]byte("end"))
	if len(b.bytes) != maxLog || !strings.HasSuffix(b.String(), "end") {
		t.Fatal(len(b.bytes))
	}
	b.Write([]byte(strings.Repeat("b", maxLog+10)))
	if len(b.bytes) != maxLog || strings.Contains(b.String(), "a") {
		t.Fatal("large write not bounded")
	}
}

func TestAtomicEncodingFailuresPreserveDestination(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "out.mp4")
	os.WriteFile(src, []byte("source"), 0600)
	os.WriteFile(dst, []byte("previous"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fail := errors.New("encoder failed")
	for _, cancelIt := range []bool{false, true} {
		err := encodeAtomic(ctx, src, dst, ".mp4", func(_ string, tmp string) error {
			os.WriteFile(tmp, []byte("partial"), 0600)
			if cancelIt {
				cancel()
				return nil
			}
			return fail
		})
		if err == nil {
			t.Fatal("failed/cancelled conversion committed")
		}
		if got, _ := os.ReadFile(dst); string(got) != "previous" {
			t.Fatal(string(got))
		}
		if leftovers, _ := filepath.Glob(filepath.Join(dir, ".wallpaper-transcode-*")); len(leftovers) != 0 {
			t.Fatal(leftovers)
		}
	}
	if err := encodeAtomic(context.Background(), src, src, ".mp4", func(string, string) error { return nil }); err == nil {
		t.Fatal("source overwrite allowed")
	}
}

func TestEncoderCancellationAndMissingOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake encoder is POSIX")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mp4")
	os.WriteFile(src, []byte("source"), 0600)
	for _, tc := range []struct {
		name, body string
		deadline   bool
	}{
		{"missing-output", "exit 0", false},
		{"blocked-encoder", "exec sleep 30", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$2\" = \"-encoders\" ]; then echo ' V....D libx264 test'; exit 0; fi\n"+tc.body+"\n"), 0700)
			fn, _, _ := Encoder("linux", p)
			if fn == nil {
				t.Fatal("fake executable unresolved")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := fn(ctx, src, filepath.Join(dir, "out.mp4"))
			if err == nil {
				t.Fatal("missing output was accepted")
			}
			if tc.deadline && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("cancellation did not stop encoder promptly")
			}
		})
	}
}

func TestVerifyRejectsInvalidRenditions(t *testing.T) {
	valid := media.Info{Container: "mp4", Kind: "video", Codec: "h264", W: 320, H: 180, FPS: 30, DurationSec: 1, VideoTracks: 1, FrameCount: 30, ConstantFrameRate: true, StartsWithKeyframe: true, MaxKeyframeInterval: 30}
	if _, err := VerifyInfo(&valid, Target{}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*media.Info){
		func(i *media.Info) { i.HasAudio = true }, func(i *media.Info) { i.FPS = 60 }, func(i *media.Info) { i.W = 321 }, func(i *media.Info) { i.H = 3000 },
		func(i *media.Info) { i.ConstantFrameRate = false }, func(i *media.Info) { i.VideoTracks = 2 }, func(i *media.Info) { i.StartsWithKeyframe = false }, func(i *media.Info) { i.MaxKeyframeInterval = 31 }, func(i *media.Info) { i.FrameCount = 0 },
	} {
		bad := valid
		change(&bad)
		if _, err := VerifyInfo(&bad, Target{}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

type probeResult struct {
	Streams []struct {
		CodecName     string `json:"codec_name"`
		CodecType     string `json:"codec_type"`
		PixFmt        string `json:"pix_fmt"`
		Width, Height int
		Rate          string `json:"r_frame_rate"`
		SAR           string `json:"sample_aspect_ratio"`
	}
	Frames []struct {
		KeyFrame int `json:"key_frame"`
	}
}

func realTools(t *testing.T) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	return ff
}

func inspectReal(t *testing.T, path string) probeResult {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_streams", "-show_frames", "-show_entries", "stream=codec_name,codec_type,pix_fmt,width,height,r_frame_rate,sample_aspect_ratio:frame=key_frame", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var r probeResult
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Streams) != 1 || len(r.Frames) == 0 {
		t.Fatal(string(b))
	}
	s := r.Streams[0]
	if s.CodecName != "h264" || s.PixFmt != "yuv420p" || s.Rate != "30/1" || s.SAR != "1:1" {
		t.Fatalf("%+v", s)
	}
	if r.Frames[0].KeyFrame != 1 {
		t.Fatal("first frame is not a key frame")
	}
	last := 0
	for n, f := range r.Frames {
		if f.KeyFrame == 1 {
			if n-last > 30 {
				t.Fatal("keyframe gap", n-last)
			}
			last = n
		}
	}
	if len(r.Frames)-last > 30 {
		t.Fatal("tail exceeds GOP")
	}
	return r
}

func TestTargetScalingReal(t *testing.T) {
	ff := realTools(t)
	for _, tc := range []struct {
		name, size, filter string
		target             Target
		w, h               int
	}{
		{"landscape", "640x360", "null", Target{320, 180}, 320, 180},
		{"portrait", "180x640", "null", Target{320, 180}, 50, 180},
		{"odd", "321x181", "null", Target{320, 180}, 318, 180},
		{"small-no-upscale", "64x36", "null", Target{320, 180}, 64, 36},
		{"anamorphic", "720x576", "setsar=16/15", Target{320, 180}, 240, 180},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source.mkv"), filepath.Join(dir, "out.mp4")
			out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size="+tc.size+":rate=60:duration=1", "-vf", tc.filter, "-c:v", "ffv1", src).CombinedOutput()
			if err != nil {
				t.Fatal(err, string(out))
			}
			fn, _, _ := EncoderForTarget("linux", ff, tc.target)
			if err := fn(context.Background(), src, dst); err != nil {
				t.Fatal(err)
			}
			r := inspectReal(t, dst)
			if s := r.Streams[0]; s.Width != tc.w || s.Height != tc.h {
				t.Fatalf("got %dx%d, want %dx%d", s.Width, s.Height, tc.w, tc.h)
			}
		})
	}
}

func TestGIFConvertsOneCycleWithVariableDelaysReal(t *testing.T) {
	ff := realTools(t)
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "loop.gif"), filepath.Join(dir, "loop.mp4")
	palette := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}, color.Transparent}
	g := &gif.GIF{Delay: []int{5, 25, 30}, LoopCount: 0, Config: image.Config{ColorModel: palette, Width: 64, Height: 36}, Disposal: []byte{gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious}}
	for i := 0; i < 3; i++ {
		im := image.NewPaletted(image.Rect(0, 0, 64, 36), palette)
		for p := range im.Pix {
			im.Pix[p] = uint8(i)
		}
		g.Image = append(g.Image, im)
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fn, _, _ := EncoderForTarget("linux", ff, Target{320, 180})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fn(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	r := inspectReal(t, dst)
	if len(r.Frames) != 18 {
		t.Fatalf("one 0.6s GIF cycle must be 18 frames, got %d", len(r.Frames))
	}
	// Independently decode frames to ensure conversion did not flatten the GIF.
	b, err := exec.Command(ff, "-v", "error", "-i", dst, "-vf", "scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 18*3 {
		t.Fatal(len(b))
	}
	for _, sample := range []struct{ frame, channel int }{{0, 0}, {4, 1}, {15, 2}} {
		px := b[sample.frame*3 : sample.frame*3+3]
		if px[sample.channel] < 200 {
			t.Fatalf("frame%d color %v", sample.frame, px)
		}
	}
}

func TestNativeHelperSelectionAndContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake helper")
	}
	ff := realTools(t)
	dir := t.TempDir()
	src, dst, helper := filepath.Join(dir, "source.mp4"), filepath.Join(dir, "destination.mp4"), filepath.Join(dir, "wallpaperd")
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "color=c=red:size=64x36:rate=30:duration=1", "-c:v", "libx264", "-g", "30", "-pix_fmt", "yuv420p", src).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	body := "#!/bin/sh\n[ \"$1\" = '-helper' ] && [ \"$2\" = 'transcode' ] && [ \"$3\" = '-in' ] && [ \"$5\" = '-out' ] && [ \"$7\" = '-width' ] && [ \"$8\" = '320' ] && [ \"$9\" = '-height' ] && [ \"${10}\" = '180' ] || exit 9\n[ ! -e \"$6\" ] || exit 10\ncp \"$4\" \"$6\"\n"
	if err := os.WriteFile(helper, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	fn, ext, name := EncoderForTargetWithHelper("darwin", "", helper, Target{320, 180})
	if fn == nil || ext != ".mp4" || !strings.Contains(name, "AVFoundation") {
		t.Fatal(ext, name)
	}
	if c := CapabilitiesForWithHelper("darwin", "", helper); !c.Strict || !c.GIF || !c.RemovesAudio || !c.FPS30 {
		t.Fatal(c)
	}
	if err := fn(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	inspectReal(t, dst)
	if fn, _, _ := EncoderForTargetWithHelper("darwin", "/explicit/missing", helper, Target{}); fn != nil {
		t.Fatal("native helper ignored explicit path")
	}
	if fn, _, _ := EncoderForTargetWithHelper("darwin", "", "relative-helper", Target{}); fn != nil {
		t.Fatal("helper was resolved through PATH")
	}
	if c := CapabilitiesForWithHelper("darwin", ff, helper); strings.Contains(c.Name, "AVFoundation") {
		t.Fatal("native helper ignored explicit ffmpeg")
	}
}

func TestLimitedSystemRouteRejectsGIF(t *testing.T) {
	src := filepath.Join("..", "media", "testdata", "anim.gif")
	dst := filepath.Join(t.TempDir(), "out.mov")
	// No process should be launched at all for an unsupported GIF input.
	err := avconvertEncoder("/must/not/be/launched")(context.Background(), src, dst)
	if err == nil || !strings.Contains(err.Error(), "cannot convert GIF") {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed GIF wrote destination", err)
	}
}

func TestFrameRateNormalizationReal(t *testing.T) {
	ff := realTools(t)
	for _, rate := range []string{"24", "25", "30000/1001", "60"} {
		t.Run(strings.ReplaceAll(rate, "/", "_"), func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source.mkv"), filepath.Join(dir, "out.mp4")
			out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=64x36:rate="+rate+":duration=1", "-c:v", "ffv1", src).CombinedOutput()
			if err != nil {
				t.Fatal(err, string(out))
			}
			fn, _, _ := EncoderForTarget("linux", ff, Target{64, 36})
			if err := fn(context.Background(), src, dst); err != nil {
				t.Fatal(err)
			}
			got := inspectReal(t, dst)
			if len(got.Frames) < 29 || len(got.Frames) > 31 {
				t.Fatalf("%s input changed cycle duration: %d frames", rate, len(got.Frames))
			}
		})
	}
}
