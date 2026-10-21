package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wpapp/internal/media"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		in   media.Info
		goos string
		want Action
	}{
		{media.Info{Kind: "image"}, "windows", None},
		{media.Info{Kind: "video", Codec: "h264", W: 1920, H: 1080, FPS: 30}, "windows", None},
		{media.Info{Kind: "video", Codec: "hevc", W: 3840, H: 2160, FPS: 30}, "darwin", None},
		{media.Info{Kind: "video", Codec: "h264", W: 7680, H: 4320, FPS: 30}, "windows", Recommend},
		{media.Info{Kind: "video", Codec: "h264", W: 1920, H: 1080, FPS: 120}, "linux", Recommend},
		{media.Info{Kind: "video", Codec: "gif"}, "windows", None},
		{media.Info{Kind: "video", Codec: "gif"}, "darwin", Required},
		{media.Info{Kind: "video", Codec: "vp9", Container: "webm"}, "darwin", Required},
		{media.Info{Kind: "video", Codec: "av1"}, "windows", Recommend},
		{media.Info{Kind: "video", Codec: "prores"}, "darwin", None},
		{media.Info{Kind: "video", Codec: "prores"}, "windows", Recommend},
	}
	for _, c := range cases {
		if p := Decide(&c.in, c.goos); p.Action != c.want {
			t.Errorf("%+v on %s: %v %v", c.in, c.goos, p.Action, p.Notes)
		}
	}
	if p := Decide(&media.Info{Kind: "video", Codec: "h264", HasAudio: true}, "windows"); p.Action != None || len(p.Notes) != 1 {
		t.Error(p)
	}
	if System("windows") != nil || System("linux") != nil {
		t.Error("Windows/Linux 在 MVP 不应有编码器挂钩")
	}
}

func TestFFmpegEncoderReal(t *testing.T) {
	fn, ext, _ := Encoder("linux", "")
	if fn == nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=60:duration=2", "-f", "lavfi", "-i", "sine=duration=2", "-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Skip(string(out))
	}
	dst := filepath.Join(dir, "out"+ext)
	if err := fn(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,r_frame_rate", "-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(out)); s != "video,30/1" {
		t.Fatalf("want only video 30fps, got %q", s)
	}
}
