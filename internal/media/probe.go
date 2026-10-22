// Package media 是导入时的轻量探测：纯 Go 解析 MP4/MOV 盒子结构（ftyp/moov/trak/stsd/mdhd/stts），
// 以及 GIF/PNG/JPEG/WebP/WebM 的文件头。10.4 要求主进程不解析不可信媒体，所以产品里 Probe 只在
// helper 子进程（wallpaperd -helper probe）里运行，主进程只读它输出的 JSON。
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Info 是探测结果。
type Info struct {
	Container           string  `json:"container"` // mp4 | mov | webm | gif | png | jpeg | webp | bmp
	Kind                string  `json:"kind"`      // video | image
	Codec               string  `json:"codec"`     // h264 | hevc | av1 | vp9 | prores | other | ""
	FourCC              string  `json:"fourcc,omitempty"`
	W                   int     `json:"w,omitempty"`
	H                   int     `json:"h,omitempty"`
	FPS                 float64 `json:"fps,omitempty"`
	DurationSec         float64 `json:"durationSec,omitempty"`
	HasAudio            bool    `json:"hasAudio"`
	VideoTracks         int     `json:"videoTracks,omitempty"`
	FrameCount          uint64  `json:"frameCount,omitempty"`
	ConstantFrameRate   bool    `json:"constantFrameRate,omitempty"`
	StartsWithKeyframe  bool    `json:"startsWithKeyframe,omitempty"`
	MaxKeyframeInterval uint64  `json:"maxKeyframeInterval,omitempty"`
}

// ErrUnknown 表示不认识的文件类型。
var ErrUnknown = errors.New("media: unsupported file type")

const maxBox = 64 << 20 // moov 之类的容器盒子最多读 64 MiB

// ProbeFile 探测文件。
func ProbeFile(path string) (*Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return Probe(f, st.Size())
}

// Probe 探测 r（size 为总长度）。
func Probe(r io.ReaderAt, size int64) (*Info, error) {
	head := make([]byte, 16)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("GIF8")):
		// Only inspect the fixed logical-screen descriptor. Frame decoding and
		// allocation stay in the isolated native/ffmpeg encoder process.
		if len(head) < 13 || (string(head[:6]) != "GIF87a" && string(head[:6]) != "GIF89a") {
			return nil, errors.New("media: invalid GIF header")
		}
		w, h := int(binary.LittleEndian.Uint16(head[6:8])), int(binary.LittleEndian.Uint16(head[8:10]))
		if w == 0 || h == 0 {
			return nil, errors.New("media: invalid GIF canvas")
		}
		return &Info{Container: "gif", Kind: "video", Codec: "gif", W: w, H: h}, nil
	case bytes.HasPrefix(head, []byte("\x89PNG")):
		return &Info{Container: "png", Kind: "image"}, nil
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		return &Info{Container: "jpeg", Kind: "image"}, nil
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return &Info{Container: "webp", Kind: "image"}, nil
	case bytes.HasPrefix(head, []byte("BM")):
		return &Info{Container: "bmp", Kind: "image"}, nil
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return &Info{Container: "webm", Kind: "video", Codec: "other"}, nil // 不解析 EBML；按需转码
	case len(head) >= 8 && (bytes.Equal(head[4:8], []byte("ftyp")) || bytes.Equal(head[4:8], []byte("moov")) ||
		bytes.Equal(head[4:8], []byte("mdat")) || bytes.Equal(head[4:8], []byte("wide")) || bytes.Equal(head[4:8], []byte("free"))):
		return probeISO(r, size)
	}
	return nil, ErrUnknown
}

type box struct {
	typ       string
	off, size int64 // payload 偏移和长度
}

func readBoxes(r io.ReaderAt, off, end int64) ([]box, error) {
	var out []box
	hdr := make([]byte, 16)
	if off < 0 || end < off {
		return nil, errors.New("media: invalid box range")
	}
	for end-off >= 8 {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return out, err
		}
		sz := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		hl := int64(8)
		switch sz {
		case 1:
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return out, err
			}
			sz = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hl = 16
		case 0:
			sz = end - off
		}
		if sz < hl || sz > end-off {
			return out, fmt.Errorf("media: bad box %q", typ)
		}
		out = append(out, box{typ, off + hl, sz - hl})
		off += sz
		if len(out) > 100000 {
			return out, errors.New("media: too many boxes")
		}
	}
	return out, nil
}

func find(bs []box, t string) (box, bool) {
	for _, b := range bs {
		if b.typ == t {
			return b, true
		}
	}
	return box{}, false
}

func children(r io.ReaderAt, b box) ([]box, error) {
	return readBoxes(r, b.off, b.off+b.size)
}

func readN(r io.ReaderAt, b box, max int64) []byte {
	n := min(b.size, max)
	buf := make([]byte, n)
	k, _ := r.ReadAt(buf, b.off)
	return buf[:k]
}

func probeISO(r io.ReaderAt, size int64) (*Info, error) {
	top, err := readBoxes(r, 0, size)
	if err != nil {
		return nil, err
	}
	info := &Info{Container: "mp4", Kind: "video"}
	if ft, ok := find(top, "ftyp"); ok {
		if b := readN(r, ft, 4); len(b) == 4 && string(b) == "qt  " {
			info.Container = "mov"
		}
	}
	moov, ok := find(top, "moov")
	if !ok {
		return nil, errors.New("media: no moov box")
	}
	if moov.size > maxBox {
		return nil, errors.New("media: moov too large")
	}
	tracks, err := children(r, moov)
	if err != nil {
		return nil, err
	}
	for _, trak := range tracks {
		if trak.typ != "trak" {
			continue
		}
		tc, err := children(r, trak)
		if err != nil {
			return nil, err
		}
		mdia, ok := find(tc, "mdia")
		if !ok {
			continue
		}
		mc, err := children(r, mdia)
		if err != nil {
			return nil, err
		}
		hdlr, ok := find(mc, "hdlr")
		if !ok {
			continue
		}
		hb := readN(r, hdlr, 12)
		if len(hb) < 12 {
			continue
		}
		switch string(hb[8:12]) {
		case "soun":
			info.HasAudio = true
		case "vide":
			info.VideoTracks++
			if info.Codec != "" {
				continue // 只看第一条视频轨
			}
			var timescale uint32
			var dur uint64
			if mdhd, ok := find(mc, "mdhd"); ok {
				b := readN(r, mdhd, 32)
				if len(b) >= 24 && b[0] == 1 && len(b) >= 32 {
					timescale = binary.BigEndian.Uint32(b[20:24])
					dur = binary.BigEndian.Uint64(b[24:32])
				} else if len(b) >= 20 {
					timescale = binary.BigEndian.Uint32(b[12:16])
					dur = uint64(binary.BigEndian.Uint32(b[16:20]))
				}
			}
			if timescale > 0 {
				info.DurationSec = float64(dur) / float64(timescale)
			}
			minf, ok := find(mc, "minf")
			if !ok {
				continue
			}
			mic, err := children(r, minf)
			if err != nil {
				return nil, err
			}
			stbl, ok := find(mic, "stbl")
			if !ok {
				continue
			}
			sc, err := children(r, stbl)
			if err != nil {
				return nil, err
			}
			if stsd, ok := find(sc, "stsd"); ok {
				b := readN(r, stsd, 8+8+78)
				// stsd: ver/flags(4) count(4)，然后第一个 sample entry：size(4) type(4) reserved(6) dref(2) predef(16) w(2) h(2)
				if len(b) >= 8+8+28 {
					fc := string(b[12:16])
					info.FourCC = fc
					info.Codec = codecOf(fc)
					info.W = int(binary.BigEndian.Uint16(b[16+24 : 16+26]))
					info.H = int(binary.BigEndian.Uint16(b[16+26 : 16+28]))
				}
			}
			if stts, ok := find(sc, "stts"); ok && timescale > 0 {
				if err := parseTiming(r, stts, timescale, info); err != nil {
					return nil, err
				}
			}
			if info.FrameCount > 0 {
				if stss, ok := find(sc, "stss"); ok {
					if err := parseSyncSamples(r, stss, info); err != nil {
						return nil, err
					}
				} else {
					// ISO BMFF: absent stss means every sample is a sync sample.
					info.StartsWithKeyframe, info.MaxKeyframeInterval = true, 1
				}
			}
		}
	}
	if info.Codec == "" {
		return nil, errors.New("media: no video track")
	}
	return info, nil
}

// Parse complete, bounded sample tables. A prefix-only fps average can mislabel
// variable-rate or maliciously truncated output as a successful 30fps rendition.
func parseTiming(r io.ReaderAt, b box, timescale uint32, info *Info) error {
	if b.size < 8 || b.size > maxBox {
		return errors.New("media: invalid stts size")
	}
	data := readN(r, b, maxBox)
	if int64(len(data)) != b.size {
		return errors.New("media: truncated stts")
	}
	count := uint64(binary.BigEndian.Uint32(data[4:8]))
	if count > uint64((len(data)-8)/8) {
		return errors.New("media: truncated stts entries")
	}
	var samples, ticks uint64
	var delta uint32
	constant := count > 0
	for i := uint64(0); i < count; i++ {
		entry := data[8+i*8:]
		c := uint64(binary.BigEndian.Uint32(entry[:4]))
		d := binary.BigEndian.Uint32(entry[4:8])
		if c == 0 || d == 0 || samples > math.MaxUint64-c || ticks > math.MaxUint64-c*uint64(d) {
			return errors.New("media: invalid stts timing")
		}
		if i == 0 {
			delta = d
		} else if d != delta {
			constant = false
		}
		samples += c
		ticks += c * uint64(d)
	}
	info.FrameCount, info.ConstantFrameRate = samples, constant
	if ticks > 0 {
		info.FPS = float64(samples) * float64(timescale) / float64(ticks)
	}
	return nil
}

func parseSyncSamples(r io.ReaderAt, b box, info *Info) error {
	if b.size < 8 || b.size > maxBox {
		return errors.New("media: invalid stss size")
	}
	data := readN(r, b, maxBox)
	if int64(len(data)) != b.size {
		return errors.New("media: truncated stss")
	}
	count := uint64(binary.BigEndian.Uint32(data[4:8]))
	if count > uint64((len(data)-8)/4) {
		return errors.New("media: truncated stss entries")
	}
	var prev uint64
	for i := uint64(0); i < count; i++ {
		n := uint64(binary.BigEndian.Uint32(data[8+i*4:]))
		if n <= prev || n > info.FrameCount {
			return errors.New("media: invalid sync sample index")
		}
		if i == 0 {
			info.StartsWithKeyframe = n == 1
		} else {
			info.MaxKeyframeInterval = max(info.MaxKeyframeInterval, n-prev)
		}
		prev = n
	}
	if prev > 0 {
		info.MaxKeyframeInterval = max(info.MaxKeyframeInterval, info.FrameCount-prev+1)
	}
	return nil
}

func codecOf(fc string) string {
	switch fc {
	case "avc1", "avc3":
		return "h264"
	case "hvc1", "hev1":
		return "hevc"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "apch", "apcn", "apcs", "apco", "ap4h", "ap4x":
		return "prores"
	}
	return "other"
}
