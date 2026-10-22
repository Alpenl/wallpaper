package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func timingData(entries ...[2]uint32) []byte {
	b := make([]byte, 8+8*len(entries))
	binary.BigEndian.PutUint32(b[4:8], uint32(len(entries)))
	for i, e := range entries {
		binary.BigEndian.PutUint32(b[8+i*8:], e[0])
		binary.BigEndian.PutUint32(b[12+i*8:], e[1])
	}
	return b
}

func TestCompleteTimingTables(t *testing.T) {
	// The variable-rate entry is beyond the old 1024-entry prefix limit.
	entries := make([][2]uint32, 1025)
	for i := range entries {
		entries[i] = [2]uint32{1, 1000}
	}
	entries[1024][1] = 2000
	b := timingData(entries...)
	var in Info
	if err := parseTiming(bytes.NewReader(b), box{size: int64(len(b))}, 30000, &in); err != nil {
		t.Fatal(err)
	}
	if in.FrameCount != 1025 || in.ConstantFrameRate || in.FPS >= 30 {
		t.Fatalf("%+v", in)
	}
	b = timingData([2]uint32{30, 1000}, [2]uint32{60, 1000})
	if err := parseTiming(bytes.NewReader(b), box{size: int64(len(b))}, 30000, &in); err != nil {
		t.Fatal(err)
	}
	if in.FrameCount != 90 || !in.ConstantFrameRate || in.FPS != 30 {
		t.Fatalf("%+v", in)
	}
}

func TestInvalidTimingIsRejected(t *testing.T) {
	valid := timingData([2]uint32{30, 1000})
	for _, b := range [][]byte{
		valid[:len(valid)-1], timingData([2]uint32{1, 0}), timingData([2]uint32{0, 1000}),
		timingData([2]uint32{math.MaxUint32, math.MaxUint32}, [2]uint32{math.MaxUint32, math.MaxUint32}),
	} {
		if err := parseTiming(bytes.NewReader(b), box{size: int64(len(b))}, 30000, &Info{}); err == nil {
			t.Fatal("invalid timing accepted")
		}
	}
}

func TestSyncSamples(t *testing.T) {
	for _, tc := range []struct {
		samples []uint32
		first   bool
		gap     uint64
		bad     bool
	}{
		{[]uint32{1, 31, 61}, true, 30, false}, {[]uint32{2, 32, 62}, false, 30, false}, {[]uint32{1, 61}, true, 60, false},
		{[]uint32{1, 1}, false, 0, true}, {[]uint32{0}, false, 0, true}, {[]uint32{91}, false, 0, true},
	} {
		b := make([]byte, 8+4*len(tc.samples))
		binary.BigEndian.PutUint32(b[4:8], uint32(len(tc.samples)))
		for n, v := range tc.samples {
			binary.BigEndian.PutUint32(b[8+n*4:], v)
		}
		in := Info{FrameCount: 90}
		err := parseSyncSamples(bytes.NewReader(b), box{size: int64(len(b))}, &in)
		if tc.bad {
			if err == nil {
				t.Fatal(tc)
			}
			continue
		}
		if err != nil || in.StartsWithKeyframe != tc.first || in.MaxKeyframeInterval != tc.gap {
			t.Fatal(tc, in, err)
		}
	}
}

func TestBoxOverflowAndTruncation(t *testing.T) {
	b := make([]byte, 16)
	binary.BigEndian.PutUint32(b, 1)
	copy(b[4:8], "moov")
	binary.BigEndian.PutUint64(b[8:], math.MaxInt64)
	if _, err := readBoxes(bytes.NewReader(b), 0, 16); err == nil {
		t.Fatal("overflow box accepted")
	}
	if _, err := readBoxes(bytes.NewReader(b), -1, 16); err == nil {
		t.Fatal("negative offset accepted")
	}
}
