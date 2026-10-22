package render

import (
	"errors"
	"math"
	"testing"
)

func TestPlaybackOptionsDefaults(t *testing.T) {
	d := DefaultPlaybackOptions()
	if d.Fit != "fill" || d.Speed != 1 || !d.Muted || d.Scale != 1 {
		t.Fatalf("unsafe defaults: %+v", d)
	}
	o, err := NormalizePlaybackOptions(PlaybackOptions{Muted: true})
	if err != nil || o != d {
		t.Fatalf("normalized=%+v err=%v", o, err)
	}
	o, err = NormalizePlaybackOptions(PlaybackOptions{})
	if err != nil || o.Muted {
		t.Fatalf("explicit false must enable audio: %+v %v", o, err)
	}
}

func TestPlaybackOptionsValidation(t *testing.T) {
	for _, fit := range []string{"fill", "fit", "stretch"} {
		for _, speed := range []float64{.25, 1, 4} {
			for _, scale := range []float64{.25, .5, 1} {
				if _, err := NormalizePlaybackOptions(PlaybackOptions{Fit: fit, Speed: speed, Scale: scale}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, o := range []PlaybackOptions{
		{Fit: "crop"}, {Speed: -.5}, {Speed: .1}, {Speed: 5}, {Speed: math.NaN()}, {Speed: math.Inf(1)},
		{Scale: -.5}, {Scale: .1}, {Scale: 1.1}, {Scale: math.NaN()}, {Scale: math.Inf(-1)},
	} {
		if _, err := NormalizePlaybackOptions(o); err == nil {
			t.Errorf("accepted invalid options: %+v", o)
		}
	}
}

func TestCapabilitiesRejectUnsupportedOptions(t *testing.T) {
	d := DefaultPlaybackOptions()
	if err := (Capabilities{}).ValidateOptions(d); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*PlaybackOptions){
		func(o *PlaybackOptions) { o.Fit = "fit" },
		func(o *PlaybackOptions) { o.Speed = 2 },
		func(o *PlaybackOptions) { o.Muted = false },
		func(o *PlaybackOptions) { o.Scale = .5 },
	} {
		o := d
		modify(&o)
		if err := (Capabilities{}).ValidateOptions(o); !errors.Is(err, ErrUnsupported) {
			t.Errorf("unsupported options accepted: %+v %v", o, err)
		}
		if err := (Capabilities{Fit: true, Speed: true, Audio: true, Scale: true}).ValidateOptions(o); err != nil {
			t.Fatal(err)
		}
	}
}
