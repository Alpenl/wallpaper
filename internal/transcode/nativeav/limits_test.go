package nativeav

import (
	"context"
	"math"
	"testing"
)

func TestOutputSize(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		w, h, bw, bh, ow, oh int
	}{
		{"landscape", 1920, 1080, 1280, 720, 1280, 720},
		{"portrait", 2160, 3840, 3840, 2160, 1214, 2160},
		{"no upscale and even", 321, 241, 3840, 2160, 320, 240},
		{"default", 7680, 4320, 0, 0, 3840, 2160},
		{"pixel budget", 32768, 32768, 8192, 8192, 4096, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, h, e := outputSize(tc.w, tc.h, tc.bw, tc.bh)
			if e != nil || w != tc.ow || h != tc.oh {
				t.Fatalf("got %dx%d %v, want %dx%d", w, h, e, tc.ow, tc.oh)
			}
		})
	}
	for _, d := range [][2]int{{1, 10}, {10, 1}, {32769, 10}, {32768, 2}, {0, 0}} {
		if _, _, e := outputSize(d[0], d[1], 2, 2); e == nil {
			t.Fatalf("accepted impossible size %v", d)
		}
	}
}
func TestFrameCountRational(t *testing.T) {
	for _, tc := range []struct {
		v    int64
		s    int32
		want int
	}{{1, 30, 1}, {1, 100, 1}, {101, 100, 31}, {1001, 30000, 2}, {1800, 1, 54000}, {60000, 1000, 1800}} {
		n, e := frameCount(tc.v, tc.s)
		if e != nil || n != tc.want {
			t.Fatalf("%d/%d => %d %v, want %d", tc.v, tc.s, n, e, tc.want)
		}
	}
	for _, tc := range []struct {
		v int64
		s int32
	}{{0, 30}, {-1, 30}, {1, 0}, {1, -1}, {1801, 1}, {math.MaxInt64, 1}} {
		if _, e := frameCount(tc.v, tc.s); e == nil {
			t.Fatalf("accepted invalid duration %+v", tc)
		}
	}
}
func TestUnavailableOrCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Encode(ctx, "unused", "unused", 0, 0); e != context.Canceled {
		t.Fatalf("cancel = %v", e)
	}
}
