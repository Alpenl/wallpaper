package mac

import (
	"image"
	"math"
	"testing"

	"wpapp/internal/desktop"
)

func TestTopologyDetectsChangesWithSameScreenCount(t *testing.T) {
	base := []desktop.Display{
		{ID: "left", Bounds: image.Rect(-1920, 0, 0, 1080), Scale: 1},
		{ID: "right", Bounds: image.Rect(0, 0, 2560, 1440), Pixels: image.Pt(5120, 2880), Scale: 2, Primary: true},
	}
	if !sameDisplayTopology(base, append([]desktop.Display(nil), base...)) {
		t.Fatal("equal screen snapshots must not emit topology changes")
	}
	changes := map[string]func(*desktop.Display){
		"rearranged":          func(d *desktop.Display) { d.Bounds = d.Bounds.Add(image.Pt(0, -500)) },
		"resized":             func(d *desktop.Display) { d.Bounds.Max.X += 640 },
		"scale":               func(d *desktop.Display) { d.Scale = 1.5 },
		"backing raster only": func(d *desktop.Display) { d.Pixels = image.Pt(3840, 2160) },
		"replaced":            func(d *desktop.Display) { d.ID = "replacement" },
		"primary":             func(d *desktop.Display) { d.Primary = !d.Primary },
		"renamed":             func(d *desktop.Display) { d.Name = "External monitor" },
	}
	for name, mutate := range changes {
		t.Run(name, func(t *testing.T) {
			next := append([]desktop.Display(nil), base...)
			mutate(&next[1])
			if sameDisplayTopology(base, next) {
				t.Fatal("changed display snapshot was ignored")
			}
		})
	}
	if sameDisplayTopology(base, base[:1]) || sameDisplayTopology(nil, base) || sameDisplayTopology(base, nil) {
		t.Fatal("screen addition/removal must change the topology")
	}
}

func TestDisplayPixelsUsesNativeModeAndCurrentOrientation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		logical, mode image.Point
		scale         float64
		want          image.Point
	}{
		{"Retina", image.Pt(1512, 982), image.Pt(3024, 1964), 2, image.Pt(3024, 1964)},
		{"native raster wins over scale", image.Pt(1920, 1080), image.Pt(5120, 2880), 2, image.Pt(5120, 2880)},
		{"portrait unrotated mode", image.Pt(1080, 1920), image.Pt(3840, 2160), 2, image.Pt(2160, 3840)},
		{"portrait already rotated mode", image.Pt(1080, 1920), image.Pt(2160, 3840), 2, image.Pt(2160, 3840)},
		{"landscape portrait mode", image.Pt(1920, 1080), image.Pt(2160, 3840), 2, image.Pt(3840, 2160)},
		{"square", image.Pt(1000, 1000), image.Pt(2000, 2000), 2, image.Pt(2000, 2000)},
		{"missing mode uses mac backing scale", image.Pt(1512, 982), image.Point{}, 2, image.Pt(3024, 1964)},
		{"partial mode falls back together", image.Pt(1512, 982), image.Pt(3024, 0), 2, image.Pt(3024, 1964)},
		{"missing scale", image.Pt(1920, 1080), image.Point{}, 0, image.Pt(1920, 1080)},
		{"invalid scale", image.Pt(1920, 1080), image.Point{}, math.NaN(), image.Pt(1920, 1080)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayPixels(tc.logical, tc.mode, tc.scale); got != tc.want {
				t.Fatalf("displayPixels = %v, want %v", got, tc.want)
			}
		})
	}
}
