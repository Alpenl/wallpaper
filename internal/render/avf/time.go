package avf

import "math"

const seekTimescale int32 = 1_000_000

// Numeric CMTime is valid and neither infinite nor indefinite. Keep rounded
// numeric values (flag 2) but never persist invalid sentinel timestamps.
func mediaTimeSeconds(value int64, timescale int32, flags uint32) (float64, bool) {
	if flags&1 == 0 || flags&(4|8|16) != 0 || timescale <= 0 || value < 0 {
		return 0, false
	}
	return float64(value) / float64(timescale), true
}

func seekTicks(seconds float64) (int64, bool) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(math.MaxInt64)/float64(seekTimescale) {
		return 0, false
	}
	return int64(math.Round(seconds * float64(seekTimescale))), true
}
