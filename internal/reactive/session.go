package reactive

import (
	"image"
	"time"
)

type PointerSample struct {
	X, Y    float64
	Buttons uint32
	Desktop bool
}
type Pointer interface {
	Start() error
	Stop()
	Sample() PointerSample
}

// Session lives on the renderer's native event thread. Only processed values
// are exposed. Permission failures disable a source until the host is replaced.
type InputFrame struct {
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Buttons uint32  `json:"buttons"`
}
type Session struct {
	InputFrame               *InputFrame
	Capture                  Capture
	Pointer                  Pointer
	Audio, Input             bool
	Blocked                  bool
	Viewport, Canvas         image.Rectangle
	mouse                    Mouse
	pointerActive            bool
	audioFailed, inputFailed bool
	Status                   string
	spectrum                 Spectrum
}

func NewSession() *Session {
	return &Session{Capture: Capture{Source: newAudio()}, Pointer: newPointer()}
}
func (s *Session) SetPaused(paused bool) {
	s.Blocked = paused
	if paused {
		s.stop()
	}
}
func (s *Session) stop() {
	_ = s.Capture.SetEnabled(false)
	if s.pointerActive {
		s.Pointer.Stop()
		s.pointerActive = false
	}
	s.mouse = Mouse{}
	s.InputFrame = nil
	s.spectrum = Spectrum{}
}
func (s *Session) Close() { s.stop() }
func (s *Session) Frame(now time.Time, fps int, raster image.Point) (Spectrum, [4]float32) {
	s.InputFrame = nil
	if s.Blocked {
		return Spectrum{}, [4]float32{}
	}
	if s.Audio && !s.audioFailed {
		if e := s.Capture.SetEnabled(true); e != nil {
			s.Status = e.Error()
			s.audioFailed = true
			s.spectrum = Spectrum{}
		}
		v, ok, e := s.Capture.Frame(now, fps)
		if e != nil {
			s.Status = e.Error()
			s.audioFailed = true
			s.spectrum = Spectrum{}
			_ = s.Capture.SetEnabled(false)
		}
		if ok {
			s.spectrum = v
		}
	}
	if s.Input && !s.inputFailed {
		if !s.pointerActive {
			if e := s.Pointer.Start(); e != nil {
				s.Status = e.Error()
				s.inputFailed = true
			} else {
				s.pointerActive = true
			}
		}
		if s.pointerActive {
			p := s.Pointer.Sample()
			x, y, ok := MapPointer(p.X, p.Y, s.Viewport, s.Canvas, raster)
			if ok && p.Desktop {
				s.InputFrame = &InputFrame{X: x, Y: float32(raster.Y) - y, Buttons: p.Buttons}
				// Store click coordinates in stable desktop units so adaptive
				// render-scale changes reproject the original click correctly.
				lx, ly, _ := MapPointer(p.X, p.Y, s.Viewport, s.Canvas, s.Canvas.Size())
				mouse := s.mouse.Step(lx, ly, p.Buttons&1 != 0)
				sx, sy := float32(raster.X)/float32(s.Canvas.Dx()), float32(raster.Y)/float32(s.Canvas.Dy())
				mouse[0] *= sx
				mouse[2] *= sx
				mouse[1] *= sy
				mouse[3] *= sy
				return s.spectrum, mouse
			}
			s.mouse = Mouse{}
		}
	}
	return s.spectrum, [4]float32{}
}
