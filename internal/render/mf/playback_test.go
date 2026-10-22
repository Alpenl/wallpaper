package mf

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"wpapp/internal/render"
)

func recordingAPI(calls *[]string) playbackAPI {
	record := func(s string) error { *calls = append(*calls, s); return nil }
	return playbackAPI{
		rate:     func(v float64) error { return record(fmt.Sprintf("rate:%g", v)) },
		mute:     func(v bool) error { return record(fmt.Sprintf("mute:%t", v)) },
		volume:   func(v float64) error { return record(fmt.Sprintf("volume:%g", v)) },
		fit:      func(v string) error { return record("fit:" + v) },
		seek:     func(v float64) error { return record(fmt.Sprintf("seek:%g", v)) },
		play:     func() error { return record("play") },
		pause:    func() error { return record("pause") },
		duration: func() float64 { return 60 },
	}
}

func TestPlaybackCapabilities(t *testing.T) {
	c := capabilities()
	if !c.Fit || !c.Speed || !c.Audio || !c.Resume || !c.FPS || c.Scale {
		t.Fatalf("wrong capabilities: %+v", c)
	}
	for _, fit := range []string{"fill", "fit", "stretch"} {
		for _, speed := range []float64{.25, 1, 4} {
			if _, err := playbackOptions(render.PlaybackOptions{Fit: fit, Speed: speed, Scale: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := playbackOptions(render.PlaybackOptions{Scale: .5}); !errors.Is(err, render.ErrUnsupported) {
		t.Fatalf("must reject unsupported scale, got %v", err)
	}
}

func TestInitialSeekWaitsBeforePlay(t *testing.T) {
	for _, paused := range []bool{false, true} {
		var calls []string
		api := recordingAPI(&calls)
		o := render.DefaultPlaybackOptions()
		o.Speed = 1.5
		s := playbackState{options: o, start: 75.5, paused: paused}
		if ready, err := s.startReady(api); ready || err != nil || len(calls) != 0 {
			t.Fatalf("started before metadata: %v, %v, %v", ready, err, calls)
		}
		if err := s.loaded(api); err != nil {
			t.Fatal(err)
		}
		s.canPlay = true
		if ready, err := s.startReady(api); ready || err != nil {
			t.Fatalf("started before asynchronous seek finished: %v, %v", ready, err)
		}
		if !reflect.DeepEqual(calls, []string{"rate:1.5", "seek:15.5"}) {
			t.Fatalf("wrong startup order: %v", calls)
		}
		// A repeated CANPLAY must neither reset speed nor seek a second time.
		if err := s.loaded(api); err != nil {
			t.Fatal(err)
		}
		s.seeking = false
		if ready, err := s.startReady(api); !ready || err != nil {
			t.Fatalf("not ready: %v, %v", ready, err)
		}
		last := "play"
		if paused {
			last = "pause"
		}
		if !reflect.DeepEqual(calls, []string{"rate:1.5", "seek:15.5", "fit:fill", last}) {
			t.Fatalf("wrong ready calls: %v", calls)
		}
		if ready, err := s.startReady(api); ready || err != nil || len(calls) != 4 {
			t.Fatal("repeated event restarted media")
		}
	}
}

func TestInitialPlaybackFailureDoesNotStart(t *testing.T) {
	for _, failing := range []string{"rate", "seek", "fit", "play"} {
		t.Run(failing, func(t *testing.T) {
			var calls []string
			api := recordingAPI(&calls)
			failure := errors.New("native failure")
			s := playbackState{options: render.DefaultPlaybackOptions(), start: 12, canPlay: true}
			switch failing {
			case "rate":
				api.rate = func(float64) error { return failure }
			case "seek":
				api.seek = func(float64) error { return failure }
			case "fit":
				api.fit = func(string) error { return failure }
			case "play":
				api.play = func() error { return failure }
			}
			err := s.loaded(api)
			if err == nil {
				s.seeking = false
				_, err = s.startReady(api)
			}
			if !errors.Is(err, failure) || s.ready {
				t.Fatalf("ignored startup failure: %v, %+v", err, s)
			}
		})
	}
}

func TestLivePlaybackPreservesPauseAndPosition(t *testing.T) {
	for _, paused := range []bool{false, true} {
		var calls []string
		api := recordingAPI(&calls)
		s := playbackState{options: render.DefaultPlaybackOptions(), ready: true, paused: paused, start: 23}
		next := render.PlaybackOptions{Fit: "stretch", Speed: 2, Muted: false, Volume: 40, Scale: 1}
		if err := s.update(api, next); err != nil {
			t.Fatal(err)
		}
		if s.paused != paused || s.start != 23 || s.options != next {
			t.Fatalf("lost playback state: %+v", s)
		}
		if !reflect.DeepEqual(calls, []string{"rate:2", "mute:false", "volume:0.4", "fit:stretch"}) {
			t.Fatalf("unexpected calls: %v", calls)
		}
		if err := s.update(api, next); err != nil || len(calls) != 4 {
			t.Fatal("identical update changed native state")
		}
	}
}

func TestLivePlaybackRollback(t *testing.T) {
	for _, failing := range []string{"rate", "mute", "fit"} {
		t.Run(failing, func(t *testing.T) {
			var calls []string
			api := recordingAPI(&calls)
			failure := errors.New("native failure")
			s := playbackState{options: render.DefaultPlaybackOptions(), ready: true, paused: true}
			old := s.options
			baseRate, baseMute, baseFit := api.rate, api.mute, api.fit
			api.rate = func(v float64) error {
				baseRate(v)
				if failing == "rate" && v == 2 {
					return failure
				}
				return nil
			}
			api.mute = func(v bool) error {
				baseMute(v)
				if failing == "mute" && !v {
					return failure
				}
				return nil
			}
			api.fit = func(v string) error {
				baseFit(v)
				if failing == "fit" && v == "fit" {
					return failure
				}
				return nil
			}
			err := s.update(api, render.PlaybackOptions{Fit: "fit", Speed: 2, Scale: 1})
			if !errors.Is(err, failure) || s.options != old || !s.paused {
				t.Fatalf("failed update committed: %v, %+v", err, s)
			}
			want := map[string][]string{
				"rate": {"rate:2", "rate:1"},
				"mute": {"rate:2", "mute:false", "mute:true", "rate:1"},
				"fit":  {"rate:2", "mute:false", "fit:fit", "fit:fill", "mute:true", "rate:1"},
			}[failing]
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("rollback %v, want %v", calls, want)
			}
		})
	}
}

func TestLivePlaybackReportsRollbackFailure(t *testing.T) {
	var calls []string
	api := recordingAPI(&calls)
	api.rate = func(float64) error { return errors.New("rate refused") }
	s := playbackState{options: render.DefaultPlaybackOptions(), ready: true}
	next := s.options
	next.Speed = 2
	if err := s.update(api, next); !errors.Is(err, errNativeStateUnknown) || !strings.Contains(err.Error(), "restore speed") || s.ready {
		t.Fatalf("rollback failure missing: %v", err)
	}
	if err := s.update(api, render.DefaultPlaybackOptions()); err == nil {
		t.Fatal("unknown native state falsely acknowledged old options without restoration")
	}
	if ready, err := s.startReady(api); ready || !errors.Is(err, errNativeStateUnknown) {
		t.Fatal("late media event restarted an invalid engine")
	}
	if err := s.setPause(api, false); !errors.Is(err, errNativeStateUnknown) {
		t.Fatal("invalid engine accepted an unpause")
	}
}

func TestLivePlaybackRejectsBeforeNativeMutation(t *testing.T) {
	for _, next := range []render.PlaybackOptions{{Scale: .5}, {Fit: "bad"}, {Speed: math.NaN()}, {Speed: 5}} {
		var calls []string
		s := playbackState{options: render.DefaultPlaybackOptions(), ready: true}
		if err := s.update(recordingAPI(&calls), next); err == nil || len(calls) > 0 {
			t.Fatalf("accepted invalid options %+v: %v, %v", next, err, calls)
		}
	}
	var calls []string
	s := playbackState{options: render.DefaultPlaybackOptions()}
	if err := s.update(recordingAPI(&calls), s.options); err == nil || len(calls) > 0 {
		t.Fatal("updated before ready")
	}
}

func TestPauseFailureAndSeekHold(t *testing.T) {
	var calls []string
	api := recordingAPI(&calls)
	s := playbackState{paused: true, seeking: true}
	if err := s.setPause(api, false); err != nil || !reflect.DeepEqual(calls, []string{"pause"}) {
		t.Fatal("unpaused during seek")
	}
	s.seeking, s.paused = false, true
	api.play = func() error { return errors.New("failed") }
	if err := s.setPause(api, false); err == nil || !s.paused {
		t.Fatal("committed failed pause transition")
	}
}

func TestResumePositionValidation(t *testing.T) {
	for _, seconds := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), 1e15} {
		if validPosition(seconds) {
			t.Fatalf("accepted %g", seconds)
		}
	}
	for _, seconds := range []float64{0, .5, 10000} {
		if !validPosition(seconds) {
			t.Fatalf("rejected %g", seconds)
		}
	}
	for _, c := range [][3]float64{{75, 60, 15}, {60, 60, 0}, {4.5, 10, 4.5}, {4.5, 0, 4.5}, {4.5, math.NaN(), 4.5}, {4.5, math.Inf(1), 4.5}} {
		if got := resumePosition(c[0], c[1]); got != c[2] {
			t.Fatalf("resume %g/%g = %g, want %g", c[0], c[1], got, c[2])
		}
	}
}

func TestResumeDuringSeekReappliesDesiredState(t *testing.T) {
	for _, paused := range []bool{false, true} {
		var calls []string
		api := recordingAPI(&calls)
		s := playbackState{ready: true, seeking: true, paused: true}
		if err := s.setPause(api, paused); err != nil {
			t.Fatal(err)
		}
		if calls[0] != "pause" {
			t.Fatal(calls)
		}
		if err := s.seeked(api); err != nil {
			t.Fatal(err)
		}
		want := "play"
		if paused {
			want = "pause"
		}
		if calls[1] != want || s.paused != paused {
			t.Fatal(calls, s)
		}
	}
}
