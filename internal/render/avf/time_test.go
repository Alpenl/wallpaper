package avf

import (
	"math"
	"testing"
)

func TestMediaTimeSeconds(t *testing.T) {
	for _, tc := range []struct {
		value int64
		scale int32
		flags uint32
		want  float64
		valid bool
	}{
		{0, 600, 1, 0, true}, {900, 600, 1, 1.5, true},
		{900, 600, 3, 1.5, true}, // rounded but numeric
		{900, 0, 1, 0, false}, {900, -600, 1, 0, false},
		{-900, 600, 1, 0, false}, {900, 600, 0, 0, false},
		{900, 600, 5, 0, false}, {900, 600, 9, 0, false}, {900, 600, 17, 0, false},
	} {
		got, valid := mediaTimeSeconds(tc.value, tc.scale, tc.flags)
		if valid != tc.valid || got != tc.want {
			t.Errorf("%+v got (%v,%v)", tc, got, valid)
		}
	}
}

func TestSeekTicks(t *testing.T) {
	for seconds, want := range map[float64]int64{0: 0, 1.5: 1_500_000, .000001: 1, 10.123456: 10_123_456} {
		if got, valid := seekTicks(seconds); !valid || got != want {
			t.Errorf("%v got %v,%v", seconds, got, valid)
		}
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), float64(math.MaxInt64)} {
		if _, valid := seekTicks(invalid); valid {
			t.Errorf("accepted %v", invalid)
		}
	}
}
