package media

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"
)

func TestProbeFixtures(t *testing.T) {
	cases := []struct {
		file, container, kind, codec string
		w, h                         int
		fps                          float64
		audio                        bool
	}{
		{"h264_audio.mp4", "mp4", "video", "h264", 64, 36, 30, true},
		{"hevc.mov", "mov", "video", "hevc", 64, 36, 24, false},
		{"vp9.webm", "webm", "video", "other", 0, 0, 0, false},
		{"anim.gif", "gif", "video", "gif", 32, 18, 0, false},
		{"still.png", "png", "image", "", 0, 0, 0, false},
	}
	for _, c := range cases {
		in, err := ProbeFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(c.file, err)
		}
		if in.Container != c.container || in.Kind != c.kind || in.Codec != c.codec || in.W != c.w || in.H != c.h ||
			math.Abs(in.FPS-c.fps) > 0.5 || in.HasAudio != c.audio {
			t.Errorf("%s: %+v", c.file, in)
		}
		if c.kind == "video" && c.codec != "gif" && c.w > 0 && math.Abs(in.DurationSec-1) > 0.2 {
			t.Errorf("%s duration %v", c.file, in.DurationSec)
		}
	}
}

func TestProbeRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{[]byte("hello world!!!!!"), {}, append([]byte{0, 0, 0, 0xff}, []byte("ftyp")...)} {
		if _, err := Probe(bytes.NewReader(b), int64(len(b))); err == nil {
			t.Errorf("%q 应报错", b)
		}
	}
	// 截断的 MP4
	mp4 := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 0, 0, 0, 8, 'f', 'r', 'e', 'e'}
	if _, err := Probe(bytes.NewReader(mp4), int64(len(mp4))); err == nil {
		t.Error("没有 moov 应报错")
	}
	for fc, want := range map[string]string{"avc3": "h264", "hev1": "hevc", "av01": "av1", "vp09": "vp9", "apcn": "prores", "mp4v": "other"} {
		if codecOf(fc) != want {
			t.Error(fc)
		}
	}
	jpg := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0}
	if in, _ := Probe(bytes.NewReader(jpg), 8); in == nil || in.Container != "jpeg" {
		t.Error("jpeg")
	}
}

func TestGIFHeaderDimensionsAndTruncation(t *testing.T) {
	valid := []byte{'G', 'I', 'F', '8', '9', 'a', 0x40, 0x01, 0xb4, 0x00, 0, 0, 0}
	in, err := Probe(bytes.NewReader(valid), int64(len(valid)))
	if err != nil || in.W != 320 || in.H != 180 {
		t.Fatal(in, err)
	}
	for _, bad := range [][]byte{valid[:10], []byte("GIF8notvalid!!"), append([]byte("GIF89a"), make([]byte, 7)...)} {
		if _, err := Probe(bytes.NewReader(bad), int64(len(bad))); err == nil {
			t.Fatalf("invalid GIF header accepted: %q", bad)
		}
	}
}
