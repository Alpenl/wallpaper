// Package reactive contains renderer-local signal processing and privacy gates.
// Sources must be owned by an isolated renderer, never the application process.
package reactive

import (
	"fmt"
	"image"
	"math"
	"time"
)

const Bins = 128

type Gate struct {
	Declared, Consented, Master, Visible bool
	Paused, Locked, Sleeping, Fullscreen bool
}

func (g Gate) Audio() bool {
	return g.Declared && g.Consented && g.Master && g.Visible && !g.Paused && !g.Locked && !g.Sleeping
}
func (g Gate) Input() bool { return g.Audio() && !g.Fullscreen }

type Limiter struct{ last time.Time }

func (l *Limiter) Ready(now time.Time, fps int) bool {
	if fps <= 0 {
		fps = 30
	}
	fps = min(fps, 60)
	if !l.last.IsZero() && now.Sub(l.last) < time.Second/time.Duration(fps) {
		return false
	}
	l.last = now
	return true
}

type Spectrum struct {
	Bins [Bins]float32
	RMS  float32
}
type Analyzer struct{ previous Spectrum }

const maxPCMSample = 1e6

func sanitizeFloat64(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v > maxPCMSample {
		return maxPCMSample
	}
	if v < -maxPCMSample {
		return -maxPCMSample
	}
	return v
}

func clampOutputFloat32(v float64) float32 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	if v > math.MaxFloat32 {
		return math.MaxFloat32
	}
	return float32(v)
}

// Analyze uses a Hann-windowed radix-2 FFT. Input is mono, normalized PCM.
// Logarithmic bands cover 20 Hz to min(20 kHz, Nyquist), peak aggregated with
// linear interpolation for low-frequency bands narrower than the FFT bin spacing.
func (a *Analyzer) Analyze(pcm []float32, rate int) (Spectrum, error) {
	n := len(pcm)
	if n < 256 || n > 65536 || n&(n-1) != 0 || rate < 8000 || rate > 192000 {
		return Spectrum{}, fmt.Errorf("reactive: invalid FFT input")
	}
	z := make([]complex128, n)
	sum, window := 0.0, 0.0
	for i, raw := range pcm {
		v := sanitizeFloat64(float64(raw))
		w := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		z[i] = complex(v*w, 0)
		sum += v * v
		window += w
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			z[i], z[j] = z[j], z[i]
		}
	}
	for size := 2; size <= n; size *= 2 {
		angle := -2 * math.Pi / float64(size)
		step := complex(math.Cos(angle), math.Sin(angle))
		for base := 0; base < n; base += size {
			w := complex(1, 0)
			for j := 0; j < size/2; j++ {
				u, v := z[base+j], w*z[base+j+size/2]
				z[base+j], z[base+j+size/2] = u+v, u-v
				w *= step
			}
		}
	}
	half := n / 2
	mags := make([]float64, half+1)
	if window > 0 {
		for k := 1; k <= half; k++ {
			m := 2 * math.Hypot(real(z[k]), imag(z[k])) / window
			if math.IsNaN(m) || math.IsInf(m, 0) || m < 0 {
				m = 0
			}
			mags[k] = m
		}
	}
	var rawBins [Bins]float64
	hi := math.Min(20000, float64(rate)/2)
	df := float64(rate) / float64(n)
	if hi > 20 && df > 0 {
		ratio := hi / 20.0
		for b := 0; b < Bins; b++ {
			f0 := 20.0 * math.Pow(ratio, float64(b)/float64(Bins))
			f1 := 20.0 * math.Pow(ratio, float64(b+1)/float64(Bins))
			lo := int(math.Ceil(f0 / df))
			hiK := int(math.Floor(f1 / df))
			if lo < 1 {
				lo = 1
			}
			if hiK > half {
				hiK = half
			}
			if lo <= hiK && float64(lo)*df <= f1+1e-9 && float64(hiK)*df >= f0-1e-9 {
				peak := 0.0
				for k := lo; k <= hiK; k++ {
					if mags[k] > peak {
						peak = mags[k]
					}
				}
				rawBins[b] = peak
			} else {
				// Band is narrower than Δf: linearly interpolate at the band center frequency.
				fc := math.Sqrt(f0 * f1)
				kf := fc / df
				kLow := int(math.Floor(kf))
				frac := kf - float64(kLow)
				if kLow < 0 {
					kLow = 0
					frac = 0
				} else if kLow > half {
					kLow = half
					frac = 0
				}
				kHigh := min(kLow+1, half)
				rawBins[b] = mags[kLow]*(1-frac) + mags[kHigh]*frac
			}
		}
	}
	// Fixed exponential smoothing at the delivery cadence (attack .65, release .2).
	var out Spectrum
	for i, cur := range rawBins {
		prev := float64(a.previous.Bins[i])
		if math.IsNaN(prev) || math.IsInf(prev, 0) || prev < 0 {
			prev = 0
		}
		if math.IsNaN(cur) || math.IsInf(cur, 0) || cur < 0 {
			cur = 0
		}
		alpha := 0.2
		if cur > prev {
			alpha = 0.65
		}
		out.Bins[i] = clampOutputFloat32(prev + alpha*(cur-prev))
	}
	out.RMS = clampOutputFloat32(math.Sqrt(sum / float64(n)))
	a.previous = out
	return out, nil
}

// MapPointer accepts host desktop units (physical pixels on Windows, logical
// points on macOS). Origins are never multiplied by DPI. Canvas is the shared
// spanning rectangle or the individual display rectangle. Raster is its actual
// shader resolution, including render scale. Gaps and other outputs are excluded.
func MapPointer(x, y float64, viewport, canvas image.Rectangle, raster image.Point) (float32, float32, bool) {
	if viewport.Empty() || canvas.Empty() || raster.X <= 0 || raster.Y <= 0 || math.IsNaN(x) || math.IsNaN(y) || x < float64(viewport.Min.X) || x >= float64(viewport.Max.X) || y < float64(viewport.Min.Y) || y >= float64(viewport.Max.Y) {
		return 0, 0, false
	}
	return float32((x - float64(canvas.Min.X)) * float64(raster.X) / float64(canvas.Dx())), float32((float64(canvas.Max.Y) - y) * float64(raster.Y) / float64(canvas.Dy())), true
}

type Mouse struct {
	Value [4]float32
	down  bool
}

// Step implements Shadertoy: xy updates only while held, z is positive while
// down, w is positive only on the initial click frame. Avoid negative zero.
func (m *Mouse) Step(x, y float32, down bool) [4]float32 {
	if down {
		m.Value[0], m.Value[1] = x, y
		if !m.down {
			m.Value[2], m.Value[3] = max(x, .0001), max(y, .0001)
		} else {
			m.Value[3] = -float32(math.Abs(float64(m.Value[3])))
		}
	} else if m.down {
		m.Value[2] = -float32(math.Abs(float64(m.Value[2])))
		m.Value[3] = -float32(math.Abs(float64(m.Value[3])))
	}
	m.down = down
	return m.Value
}

// Source owns raw PCM, including its bounded buffer. Stop must discard it.
type Source interface {
	Start() error
	Stop()
	Read() ([]float32, int, error)
}
type Capture struct {
	Source   Source
	active   bool
	limiter  Limiter
	analyzer Analyzer
}

func (c *Capture) SetEnabled(enabled bool) error {
	if enabled == c.active {
		return nil
	}
	if !enabled {
		c.Source.Stop()
		c.active = false
		c.analyzer = Analyzer{}
		c.limiter = Limiter{}
		return nil
	}
	if err := c.Source.Start(); err != nil {
		return err
	}
	c.active = true
	return nil
}
func (c *Capture) Frame(now time.Time, fps int) (Spectrum, bool, error) {
	if !c.active || !c.limiter.Ready(now, fps) {
		return Spectrum{}, false, nil
	}
	p, r, e := c.Source.Read()
	if e != nil {
		return Spectrum{}, false, e
	}
	if len(p) == 0 {
		return Spectrum{}, false, nil
	}
	s, e := c.analyzer.Analyze(p, r)
	return s, e == nil, e
}
