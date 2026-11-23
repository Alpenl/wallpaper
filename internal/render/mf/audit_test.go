package mf

import (
	"testing"
	"time"
)

func TestVBlankFallbackConditions(t *testing.T) {
	// Normal 16ms VBlank wait with S_OK does not trigger fallback sleep.
	if shouldFallbackVBlank(0, 16*time.Millisecond) {
		t.Fatal("normal VBlank wait should not fallback")
	}
	// Positive DXGI_STATUS_OCCLUDED (0x087A0001) must trigger fallback sleep.
	if !shouldFallbackVBlank(0x087A0001, 16*time.Millisecond) {
		t.Fatal("DXGI_STATUS_OCCLUDED must trigger fallback")
	}
	// Negative HRESULT must trigger fallback sleep.
	if !shouldFallbackVBlank(0x887A0005, 16*time.Millisecond) {
		t.Fatal("device removed HR must trigger fallback")
	}
	// Instantaneous S_OK return (< 1ms) must trigger fallback sleep.
	if !shouldFallbackVBlank(0, 200*time.Microsecond) {
		t.Fatal("sub-1ms VBlank return must trigger fallback")
	}
}

func TestStallTrackerAllowsVFRStaticSceneWhenClockAdvances(t *testing.T) {
	var d stallTracker
	// Constant lastPTS across >=15s while playback clock advances must NOT stall.
	for i := 0; i < 6; i++ {
		if d.observe(false, 0, 1000000, float64(i)*5.0) {
			t.Fatalf("VFR static scene falsely flagged as stalled at sample %d", i)
		}
	}
	// Genuine stall: loops, pts, AND clock all frozen for 2 consecutive intervals.
	d = stallTracker{}
	if d.observe(false, 0, 500000, 10.0) {
		t.Fatal("first sample cannot be stalled")
	}
	if d.observe(false, 0, 500000, 10.0) {
		t.Fatal("single unchanged interval cannot be stalled")
	}
	if !d.observe(false, 0, 500000, 10.0) {
		t.Fatal("two consecutive unchanged intervals must detect stall")
	}
	// Paused resets stall tracking cleanly.
	if d.observe(true, 0, 500000, 10.0) {
		t.Fatal("paused playback must not stall")
	}
	if d.observe(false, 0, 500000, 10.0) || d.observe(false, 0, 500000, 10.0) {
		t.Fatal("pause should reset stall counter")
	}
}
