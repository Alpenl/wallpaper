package reactive

import (
	"image"
	"math"
	"testing"
	"time"
)

func TestSpectrum(t *testing.T) {
	for _, hz := range []float64{100, 1000, 10000} {
		a := Analyzer{}
		pcm := make([]float32, 16384)
		for i := range pcm {
			pcm[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 48000))
		}
		var s Spectrum
		for i := 0; i < 20; i++ {
			var e error
			s, e = a.Analyze(pcm, 48000)
			if e != nil {
				t.Fatal(e)
			}
		}
		peak := 0
		for i, v := range s.Bins {
			if v > s.Bins[peak] {
				peak = i
			}
		}
		want := int(math.Log(hz/20) / math.Log(1000) * Bins)
		if abs(peak-want) > 1 || math.Abs(float64(s.RMS)-1/math.Sqrt2) > .01 {
			t.Fatalf("%g Hz: peak %d want %d RMS %g", hz, peak, want, s.RMS)
		}
		before := s.Bins[peak]
		clear(pcm)
		s, _ = a.Analyze(pcm, 48000)
		if s.Bins[peak] >= before || s.Bins[peak] <= 0 || s.RMS != 0 {
			t.Fatal("release smoothing", s)
		}
	}
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func TestRate(t *testing.T) {
	for _, fps := range []int{15, 30, 60, 240} {
		l := Limiter{}
		count := 0
		now := time.Unix(10, 0)
		for i := 0; i < 1000; i++ {
			if l.Ready(now.Add(time.Duration(i)*time.Millisecond), fps) {
				count++
			}
		}
		if count > min(fps, 60) || count < min(fps, 60)-2 {
			t.Fatal(fps, count)
		}
	}
}
func TestGates(t *testing.T) {
	all := Gate{Declared: true, Consented: true, Master: true, Visible: true}
	if !all.Audio() || !all.Input() {
		t.Fatal("allowed")
	}
	cases := []Gate{all, all, all, all, all, all, all}
	cases[0].Declared = false
	cases[1].Consented = false
	cases[2].Master = false
	cases[3].Visible = false
	cases[4].Paused = true
	cases[5].Locked = true
	cases[6].Sleeping = true
	for _, g := range cases {
		if g.Audio() || g.Input() {
			t.Fatal("gate failed", g)
		}
	}
	all.Fullscreen = true
	if all.Input() {
		t.Fatal("fullscreen input")
	}
}

type fakeSource struct{ starts, stops, reads int }

func (s *fakeSource) Start() error { s.starts++; return nil }
func (s *fakeSource) Stop()        { s.stops++ }
func (s *fakeSource) Read() ([]float32, int, error) {
	s.reads++
	return make([]float32, 256), 48000, nil
}
func TestCaptureLifecycle(t *testing.T) {
	s := &fakeSource{}
	c := Capture{Source: s}
	now := time.Now()
	c.Frame(now, 30)
	if s.reads != 0 {
		t.Fatal("read disabled")
	}
	for i := 0; i < 3; i++ {
		if e := c.SetEnabled(true); e != nil {
			t.Fatal(e)
		}
	}
	c.Frame(now, 30)
	c.Frame(now, 30)
	c.SetEnabled(false)
	c.SetEnabled(false)
	c.Frame(now.Add(time.Second), 30)
	if s.starts != 1 || s.stops != 1 || s.reads != 1 {
		t.Fatal(s)
	}
	c.SetEnabled(true)
	c.Frame(now, 30)
	if s.starts != 2 || s.reads != 2 {
		t.Fatal(s)
	}
}
func TestMouse(t *testing.T) {
	m := Mouse{}
	if v := m.Step(4, 5, false); v != [4]float32{} {
		t.Fatal(v)
	}
	if v := m.Step(4, 5, true); v != [4]float32{4, 5, 4, 5} {
		t.Fatal(v)
	}
	if v := m.Step(8, 9, true); v != [4]float32{8, 9, 4, -5} {
		t.Fatal(v)
	}
	if v := m.Step(20, 30, false); v != [4]float32{8, 9, -4, -5} {
		t.Fatal(v)
	}
	v := m.Step(0, 0, true)
	if v[2] <= 0 || v[3] <= 0 {
		t.Fatal("zero sign", v)
	}
}
func TestCoordinates(t *testing.T) {
	for _, tc := range []struct {
		viewport, canvas image.Rectangle
		raster           image.Point
		x, y             float64
		wx, wy           float32
	}{
		{image.Rect(-1920, -200, 0, 880), image.Rect(-1920, -200, 0, 880), image.Pt(1920, 1080), -960, 340, 960, 540},
		{image.Rect(0, 0, 1440, 900), image.Rect(0, 0, 1440, 900), image.Pt(2880, 1800), 720, 450, 1440, 900},
		{image.Rect(-1440, 0, 0, 900), image.Rect(-1440, 0, 1920, 1080), image.Pt(6720, 2160), -720, 450, 1440, 1260},
		{image.Rect(0, 0, 1920, 1080), image.Rect(-1440, 0, 1920, 1080), image.Pt(3360, 1080), 960, 540, 2400, 540},
	} {
		x, y, ok := MapPointer(tc.x, tc.y, tc.viewport, tc.canvas, tc.raster)
		if !ok || x != tc.wx || y != tc.wy {
			t.Fatal(tc, x, y, ok)
		}
	}
	if _, _, ok := MapPointer(-1, 10, image.Rect(0, 0, 100, 100), image.Rect(-100, 0, 100, 100), image.Pt(200, 100)); ok {
		t.Fatal("outside viewport")
	}
}

type fakePointer struct {
	sample               PointerSample
	starts, stops, reads int
}

func (p *fakePointer) Start() error          { p.starts++; return nil }
func (p *fakePointer) Stop()                 { p.stops++ }
func (p *fakePointer) Sample() PointerSample { p.reads++; return p.sample }
func TestSessionStopsAndResumes(t *testing.T) {
	a := &fakeSource{}
	p := &fakePointer{sample: PointerSample{X: 30, Y: 40, Buttons: 1, Desktop: true}}
	s := Session{Capture: Capture{Source: a}, Pointer: p, Audio: true, Input: true, Viewport: image.Rect(0, 0, 100, 100), Canvas: image.Rect(0, 0, 100, 100)}
	now := time.Now()
	_, mouse := s.Frame(now, 30, image.Pt(200, 200))
	if mouse[0] != 60 || mouse[1] != 120 || p.reads != 1 || a.reads != 1 {
		t.Fatal(mouse, p, a)
	}
	// L1+, fullscreen, lock and sleep all reach this immediate policy stop.
	s.SetPaused(true)
	for i := 0; i < 10; i++ {
		_, v := s.Frame(now.Add(time.Second), 30, image.Pt(200, 200))
		if v != [4]float32{} {
			t.Fatal("stale input", v)
		}
	}
	if a.stops != 1 || p.stops != 1 || a.reads != 1 || p.reads != 1 {
		t.Fatal(a, p)
	}
	s.SetPaused(false)
	s.Frame(now.Add(time.Second), 30, image.Pt(200, 200))
	if a.starts != 2 || p.starts != 2 {
		t.Fatal(a, p)
	}
	p.sample.Desktop = false
	_, v := s.Frame(now.Add(2*time.Second), 30, image.Pt(200, 200))
	if v != [4]float32{} || s.InputFrame != nil {
		t.Fatal("covered cursor forwarded", v)
	}
	s.Close()
	if a.stops != 2 || p.stops != 2 {
		t.Fatal(a, p)
	}
}

func TestClickReprojectsWithAdaptiveScale(t *testing.T) {
	p := &fakePointer{sample: PointerSample{X: 25, Y: 75, Buttons: 1, Desktop: true}}
	s := Session{Pointer: p, Input: true, Viewport: image.Rect(0, 0, 100, 100), Canvas: image.Rect(0, 0, 100, 100)}
	_, a := s.Frame(time.Now(), 30, image.Pt(100, 100))
	_, b := s.Frame(time.Now(), 30, image.Pt(200, 200))
	if a[2] != 25 || b[2] != 50 || b[3] != -50 {
		t.Fatal(a, b)
	}
}
