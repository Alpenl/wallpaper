package shader

import (
	"math"
	"testing"
	"time"
)

func TestAdaptiveHysteresis(t *testing.T) {
	a := NewAdaptive(.75)
	for i := 0; i < 2; i++ {
		if a.Sample(5) != .75 {
			t.Fatal("reacted to isolated slow frame")
		}
	}
	if a.Sample(5) != .7 {
		t.Fatal(a)
	}
	for i := 0; i < 119; i++ {
		if a.Sample(2) != .7 {
			t.Fatal("headroom not sustained")
		}
	}
	if a.Sample(2) != .75 {
		t.Fatal(a)
	}
	for i := 0; i < 100; i++ {
		a.Sample(8)
	}
	if a.Scale != .5 {
		t.Fatal(a)
	}
	a.Sample(math.NaN())
	a.Sample(-1)
	if a.Scale != .5 {
		t.Fatal(a)
	}
	for i := 0; i < 1000; i++ {
		a.Sample(1)
	}
	if a.Scale != .75 {
		t.Fatal(a)
	}
}
func TestPauseFreezesTimeline(t *testing.T) {
	var c Timeline
	n := time.Unix(0, 0)
	c.Tick(n)
	s, d := c.Tick(n.Add(time.Second))
	if s != 1 || d != 1 {
		t.Fatal(s, d)
	}
	c.Pause(true)
	f := c.Frame
	s, d = c.Tick(n.Add(time.Hour))
	if s != 1 || d != 0 || c.Frame != f {
		t.Fatal(s, d, c.Frame)
	}
	c.Pause(false)
	s, d = c.Tick(n.Add(2 * time.Hour))
	if s != 1 || d != 0 {
		t.Fatal(s, d)
	}
	s, d = c.Tick(n.Add(2*time.Hour + time.Second))
	if s != 2 || d != 1 {
		t.Fatal(s, d)
	}
}
