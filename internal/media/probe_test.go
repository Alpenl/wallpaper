package media

import (
	"bytes"
	"encoding/binary"
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

func makeBox(typ string, parts ...[]byte) []byte {
	var body []byte
	for _, p := range parts {
		body = append(body, p...)
	}
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], typ)
	copy(out[8:], body)
	return out
}

func makeFTYP() []byte {
	return makeBox("ftyp", []byte("isom\x00\x00\x02\x00isom"))
}

func makeHDLR(handler string) []byte {
	b := make([]byte, 24)
	copy(b[8:12], handler)
	return makeBox("hdlr", b)
}

func makeTKHD(enabled bool, a, b, c, d int32) []byte {
	buf := make([]byte, 84)
	if enabled {
		buf[3] = 1
	}
	binary.BigEndian.PutUint32(buf[40:44], uint32(a))
	binary.BigEndian.PutUint32(buf[44:48], uint32(b))
	binary.BigEndian.PutUint32(buf[52:56], uint32(c))
	binary.BigEndian.PutUint32(buf[56:60], uint32(d))
	binary.BigEndian.PutUint32(buf[72:76], 0x40000000)
	return makeBox("tkhd", buf)
}

func makeSTSD(fourCC string, w, h uint16, extra ...[]byte) []byte {
	vse := make([]byte, 78)
	binary.BigEndian.PutUint16(vse[24:26], w)
	binary.BigEndian.PutUint16(vse[26:28], h)
	entry := makeBox(fourCC, append([][]byte{vse}, extra...)...)
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[4:8], 1)
	return makeBox("stsd", hdr, entry)
}

func makeAVCC(profile byte, withExt bool, lumaMinus8, chromaMinus8 byte) []byte {
	// version=1, profile, compat=0, level=40, lengthSizeMinusOne=3, numSPS=0, numPPS=0
	b := []byte{1, profile, 0, 40, 0xff, 0xe0, 0}
	if withExt {
		b = append(b, 0xfd, 0xf8|lumaMinus8, 0xf8|chromaMinus8, 0)
	}
	return makeBox("avcC", b)
}

func makeVideoTrack(tkhd, mdhd, stsd, stts []byte) []byte {
	var stblParts [][]byte
	if stsd != nil {
		stblParts = append(stblParts, stsd)
	}
	if stts != nil {
		stblParts = append(stblParts, stts)
	}
	mdiaParts := [][]byte{makeHDLR("vide")}
	if mdhd != nil {
		mdiaParts = append(mdiaParts, mdhd)
	}
	mdiaParts = append(mdiaParts, makeBox("minf", makeBox("stbl", stblParts...)))
	var trakParts [][]byte
	if tkhd != nil {
		trakParts = append(trakParts, tkhd)
	}
	trakParts = append(trakParts, makeBox("mdia", mdiaParts...))
	return makeBox("trak", trakParts...)
}

func TestProbeMDHDStrictV1AndSentinelDuration(t *testing.T) {
	stsd := makeSTSD("avc1", 640, 360)

	t.Run("truncated-v1-rejected", func(t *testing.T) {
		// version=1 but only 24 bytes payload (must NOT fall through to v0!).
		mdhdBody := make([]byte, 24)
		mdhdBody[0] = 1
		binary.BigEndian.PutUint32(mdhdBody[12:16], 1000)
		binary.BigEndian.PutUint32(mdhdBody[16:20], 5000)
		mp4 := append(makeFTYP(), makeBox("moov", makeVideoTrack(nil, makeBox("mdhd", mdhdBody), stsd, nil))...)
		if _, err := Probe(bytes.NewReader(mp4), int64(len(mp4))); err == nil {
			t.Fatal("truncated v1 mdhd should be rejected")
		}
	})

	t.Run("v0-sentinel-duration-unknown", func(t *testing.T) {
		mdhdBody := make([]byte, 20)
		mdhdBody[0] = 0
		binary.BigEndian.PutUint32(mdhdBody[12:16], 1000)
		binary.BigEndian.PutUint32(mdhdBody[16:20], math.MaxUint32)
		mp4 := append(makeFTYP(), makeBox("moov", makeVideoTrack(nil, makeBox("mdhd", mdhdBody), stsd, nil))...)
		in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
		if err != nil {
			t.Fatal(err)
		}
		if in.DurationSec != 0 {
			t.Fatalf("expected unknown duration 0 for v0 sentinel, got %v", in.DurationSec)
		}
	})

	t.Run("v1-sentinel-duration-unknown", func(t *testing.T) {
		mdhdBody := make([]byte, 32)
		mdhdBody[0] = 1
		binary.BigEndian.PutUint32(mdhdBody[20:24], 1000)
		binary.BigEndian.PutUint64(mdhdBody[24:32], math.MaxUint64)
		mp4 := append(makeFTYP(), makeBox("moov", makeVideoTrack(nil, makeBox("mdhd", mdhdBody), stsd, nil))...)
		in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
		if err != nil {
			t.Fatal(err)
		}
		if in.DurationSec != 0 {
			t.Fatalf("expected unknown duration 0 for v1 sentinel, got %v", in.DurationSec)
		}
	})

	t.Run("v1-valid-duration", func(t *testing.T) {
		mdhdBody := make([]byte, 32)
		mdhdBody[0] = 1
		binary.BigEndian.PutUint32(mdhdBody[20:24], 1000)
		binary.BigEndian.PutUint64(mdhdBody[24:32], 12500)
		mp4 := append(makeFTYP(), makeBox("moov", makeVideoTrack(nil, makeBox("mdhd", mdhdBody), stsd, nil))...)
		in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(in.DurationSec-12.5) > 0.001 {
			t.Fatalf("expected 12.5s, got %v", in.DurationSec)
		}
	})
}

func TestProbeMultiTrackIsolation(t *testing.T) {
	mdhd1 := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd1[12:16], 1000)
	binary.BigEndian.PutUint32(mdhd1[16:20], 99000) // 99s on track 1 (no stsd)

	mdhd2 := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd2[12:16], 1000)
	binary.BigEndian.PutUint32(mdhd2[16:20], 5000) // 5s on track 2

	trak1 := makeVideoTrack(makeTKHD(true, 0x10000, 0, 0, 0x10000), makeBox("mdhd", mdhd1), nil, nil)
	trak2 := makeVideoTrack(makeTKHD(true, 0x10000, 0, 0, 0x10000), makeBox("mdhd", mdhd2), makeSTSD("avc1", 1920, 1080), nil)
	mp4 := append(makeFTYP(), makeBox("moov", trak1, trak2)...)
	in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
	if err != nil {
		t.Fatal(err)
	}
	if in.VideoTracks != 2 || in.Codec != "h264" || in.W != 1920 || in.H != 1080 || math.Abs(in.DurationSec-5.0) > 0.001 {
		t.Fatalf("cross-track metadata leak: %+v", in)
	}
}

func TestProbeTKHDRotation(t *testing.T) {
	const one = 0x10000
	cases := []struct {
		name       string
		a, b, c, d int32
		want       int
	}{
		{"0", one, 0, 0, one, 0},
		{"90", 0, one, -one, 0, 90},
		{"180", -one, 0, 0, -one, 180},
		{"270", 0, -one, one, 0, 270},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trak := makeVideoTrack(makeTKHD(true, tc.a, tc.b, tc.c, tc.d), nil, makeSTSD("avc1", 1920, 1080), nil)
			mp4 := append(makeFTYP(), makeBox("moov", trak)...)
			in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
			if err != nil {
				t.Fatal(err)
			}
			if in.Rotation != tc.want {
				t.Fatalf("got rotation %d, want %d", in.Rotation, tc.want)
			}
		})
	}
}

func TestProbeAVCCBitDepth(t *testing.T) {
	cases := []struct {
		name    string
		avcc    []byte
		wantBit int
	}{
		{"8bit-high", makeAVCC(100, true, 0, 0), 8},
		{"10bit-high10-ext", makeAVCC(110, true, 2, 2), 10},
		{"10bit-high10-no-ext", makeAVCC(110, false, 0, 0), 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trak := makeVideoTrack(nil, nil, makeSTSD("avc1", 1920, 1080, tc.avcc), nil)
			mp4 := append(makeFTYP(), makeBox("moov", trak)...)
			in, err := Probe(bytes.NewReader(mp4), int64(len(mp4)))
			if err != nil {
				t.Fatal(err)
			}
			if in.BitDepth != tc.wantBit {
				t.Fatalf("got bit depth %d, want %d", in.BitDepth, tc.wantBit)
			}
		})
	}
}
