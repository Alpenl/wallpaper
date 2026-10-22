package desktop

import (
	"image"
	"math"
	"testing"
)

func TestCanvasMixedGeometry(t *testing.T) {
	ds := []Display{{ID: "left", Bounds: image.Rect(-1920, -200, 0, 880)}, {ID: "main", Bounds: image.Rect(0, 0, 2560, 1440)}}
	c, err := CanvasBounds(ds)
	if err != nil || c != image.Rect(-1920, -200, 2560, 1440) {
		t.Fatalf("%v %v", c, err)
	}
	x, y, w, h, err := CanvasFrame(c, ds[1].Bounds, 1280, 720, true)
	if err != nil || x != -960 || y != -100 || w != 2240 || h != 820 {
		t.Fatalf("top-down: %v %v %v %v %v", x, y, w, h, err)
	}
	x, y, w, h, err = CanvasFrame(c, ds[0].Bounds, 1920, 1080, false)
	if err != nil || x != 0 || y != -560 || w != 4480 || h != 1640 {
		t.Fatalf("Cocoa: %v %v %v %v %v", x, y, w, h, err)
	}
}
func TestCanvasRejectsInvalidGeometry(t *testing.T) {
	for _, ds := range [][]Display{nil, {{ID: "a"}}, {{ID: "a", Bounds: image.Rect(0, 0, 10, 10)}, {ID: "a", Bounds: image.Rect(10, 0, 20, 10)}}, {{ID: "a", Bounds: image.Rect(-20000, 0, 20000, 10)}}} {
		if _, err := CanvasBounds(ds); err == nil {
			t.Fatalf("accepted %+v", ds)
		}
	}
	c := image.Rect(0, 0, 100, 100)
	for _, v := range []image.Rectangle{image.Rect(-1, 0, 20, 20), {}, image.Rect(0, 0, 200, 200)} {
		if _, _, _, _, err := CanvasFrame(c, v, 100, 100, true); err == nil {
			t.Fatal("accepted invalid viewport")
		}
	}
	if _, _, _, _, err := CanvasFrame(c, c, math.Inf(1), 100, true); err == nil {
		t.Fatal("accepted infinite dimension")
	}
}
func TestCanvasLeavesGapsAndOverlapsUnaltered(t *testing.T) {
	ds := []Display{{ID: "a", Bounds: image.Rect(-100, 0, 0, 100)}, {ID: "b", Bounds: image.Rect(100, 50, 200, 150)}}
	c, _ := CanvasBounds(ds)
	if c != image.Rect(-100, 0, 200, 150) {
		t.Fatal(c)
	}
	x, y, _, _, _ := CanvasFrame(c, ds[1].Bounds, 100, 100, true)
	if x != -200 || y != -50 {
		t.Fatalf("gap collapsed: %v %v", x, y)
	}
}

func TestCanvasRejectsCoordinateOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	rects := []image.Rectangle{{Min: image.Pt(-maxInt, 0), Max: image.Pt(maxInt, 1)}, {Min: image.Pt(maxInt-100, 0), Max: image.Pt(maxInt, 1)}}
	for _, r := range rects {
		if _, err := CanvasBounds([]Display{{ID: "x", Bounds: r}}); err == nil {
			t.Fatalf("accepted overflowing/native-incompatible %v", r)
		}
		if _, _, _, _, err := CanvasFrame(r, r, 100, 100, true); err == nil {
			t.Fatal("frame accepted impossible native bounds")
		}
	}
}
