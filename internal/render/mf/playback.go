package mf

import (
	"errors"
	"fmt"
	"math"

	"wpapp/internal/render"
)

func capabilities() render.Capabilities {
	return render.Capabilities{Fit: true, Speed: true, Audio: true, Resume: true, FPS: true}
}

func playbackOptions(o render.PlaybackOptions) (render.PlaybackOptions, error) {
	o, err := render.NormalizePlaybackOptions(o)
	if err == nil {
		err = capabilities().ValidateOptions(o)
	}
	return o, err
}

func validPosition(seconds float64) bool {
	return !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0 && seconds <= float64(math.MaxInt64)/1e7
}

// A looping wallpaper resumes within its current duration, including after the
// file was replaced with a shorter version. Never seek exactly onto its end.
func resumePosition(seconds, duration float64) float64 {
	if duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
		return math.Mod(seconds, duration)
	}
	return seconds
}

// These functions are bound to native COM methods once per engine. The state
// machine and its failure/rollback behavior can also be tested without Windows.
type playbackAPI struct {
	rate     func(float64) error
	mute     func(bool) error
	volume   func(float64) error // 0–1
	fit      func(string) error
	seek     func(float64) error
	play     func() error
	pause    func() error
	duration func() float64
}

type playbackState struct {
	options  render.PlaybackOptions
	paused   bool
	start    float64
	metadata bool
	canPlay  bool
	seeking  bool
	ready    bool
	invalid  bool
}

var errNativeStateUnknown = errors.New("mf: native playback state is unknown after failed rollback")

// Frame-server startup seeks need frame servicing until SEEKED, even when the
// desired playback state is paused. Only SEEKED releases the readiness gate.
func (s *playbackState) shouldServiceFrames() bool {
	if s.invalid {
		return false
	}
	if !s.ready {
		return s.metadata && s.seeking
	}
	return !s.paused
}

func (s *playbackState) loaded(api playbackAPI) error {
	if s.metadata { // LOADEDMETADATA/CANPLAY can repeat after a seek or loop.
		return nil
	}
	if err := api.rate(s.options.Speed); err != nil {
		return err
	}
	s.start = resumePosition(s.start, api.duration())
	if s.start > 0 {
		if err := api.seek(s.start); err != nil {
			return err
		}
		s.seeking = true
	}
	s.metadata = true
	return nil
}

func (s *playbackState) startReady(api playbackAPI) (bool, error) {
	if s.invalid {
		return false, errNativeStateUnknown
	}
	if s.ready || !s.metadata || !s.canPlay || s.seeking {
		return false, nil
	}
	if err := api.fit(s.options.Fit); err != nil {
		return false, err
	}
	if err := s.setPause(api, s.paused); err != nil {
		return false, err
	}
	s.ready = true
	return true, nil
}

func (s *playbackState) setPause(api playbackAPI, paused bool) error {
	if s.invalid {
		return errNativeStateUnknown
	}
	var err error
	if paused || s.seeking {
		err = api.pause()
	} else {
		err = api.play()
	}
	if err == nil {
		s.paused = paused
	}
	return err
}

// No Play/seek calls: live changes must not unpause, restart, or lose position.
// Restore every attempted native property after failure, including the failed
// call itself, since a native failure can still have changed part of its state.
func (s *playbackState) update(api playbackAPI, o render.PlaybackOptions) error {
	var err error
	if o, err = playbackOptions(o); err != nil {
		return err
	}
	if !s.ready {
		return fmt.Errorf("mf: media is not ready")
	}
	type change struct {
		name        string
		apply, undo func() error
	}
	old := s.options
	var changes []change
	if o.Speed != old.Speed {
		changes = append(changes, change{"speed", func() error { return api.rate(o.Speed) }, func() error { return api.rate(old.Speed) }})
	}
	if o.Muted != old.Muted {
		changes = append(changes, change{"mute", func() error { return api.mute(o.Muted) }, func() error { return api.mute(old.Muted) }})
	}
	if o.Volume != old.Volume {
		changes = append(changes, change{"volume", func() error { return api.volume(float64(o.Volume) / 100) }, func() error { return api.volume(float64(old.Volume) / 100) }})
	}
	if o.Fit != old.Fit {
		changes = append(changes, change{"fit", func() error { return api.fit(o.Fit) }, func() error { return api.fit(old.Fit) }})
	}
	for i, change := range changes {
		if err := change.apply(); err != nil {
			result := fmt.Errorf("mf: update %s: %w", change.name, err)
			for j := i; j >= 0; j-- {
				if restore := changes[j].undo(); restore != nil {
					result = errors.Join(result, fmt.Errorf("restore %s: %w", changes[j].name, restore))
					// Do not let a later request for the old options return a
					// false no-op success. The owning engine must be restarted.
					s.ready = false
					s.invalid = true
				}
			}
			if !s.ready {
				result = errors.Join(result, errNativeStateUnknown)
			}
			return result
		}
	}
	s.options = o
	return nil
}

// seeked reconciles the desired state even when startup already completed.
func (s *playbackState) seeked(api playbackAPI) error {
	s.seeking = false
	if s.ready {
		return s.setPause(api, s.paused)
	}
	return nil
}
