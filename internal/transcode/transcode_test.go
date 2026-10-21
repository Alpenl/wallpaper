package transcode

import (
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
