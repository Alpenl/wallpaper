//go:build windows

package win

import (
	"image"
	"testing"

	"wpapp/internal/desktop"
)

func TestCanvasNativeHandlePreservesOuterOwner(t *testing.T) {
	bounds := image.Rect(-1920, -1080, 2560, 1440)
	s := &surface{hwnd: 11, canvasHwnd: 22, b: bounds}
	if s.Native() != 22 || s.hwnd != 11 || s.Bounds() != bounds {
		t.Fatalf("canvas surface lost renderer geometry or ownership: %+v", s)
	}
	s.canvasHwnd = 0
	if s.Native() != 11 {
		t.Fatal("ordinary surface must expose its original HWND")
	}
}

func TestStandaloneDoesNotAdvertiseSpanning(t *testing.T) {
	h := New(Options{Standalone: true})
	if h.Caps().Spanning {
		t.Fatal("resized standalone windows cannot claim physical desktop spanning")
	}
	// Must reject before posting to a UI thread that has not been started.
	if s, err := h.CreateCanvasSurface("display", image.Rect(0, 0, 1920, 1080)); err == nil || s != nil {
		t.Fatalf("standalone spanning = %v, %v", s, err)
	}
	if !New(Options{}).Caps().Spanning {
		t.Fatal("native desktop host must advertise its canvas implementation")
	}
}

func TestCanvasRejectsGeometryBeforeCreatingWindows(t *testing.T) {
	h := New(Options{})
	h.mons = []*mon{{d: desktop.Display{ID: "display", Bounds: image.Rect(0, 0, 1920, 1080)}}}
	for _, canvas := range []image.Rectangle{
		{}, image.Rect(0, 0, 640, 480), image.Rect(-1, 0, 32767, 1080),
	} {
		if s, err := h.createCanvasSurface("display", canvas); err == nil || s != nil {
			t.Fatalf("invalid canvas %v = %v, %v", canvas, s, err)
		}
	}
	if s, err := h.createCanvasSurface("missing", image.Rect(0, 0, 1920, 1080)); err == nil || s != nil {
		t.Fatalf("missing display = %v, %v", s, err)
	}
	if len(h.surfaces) != 0 {
		t.Fatal("rejected geometry allocated native windows")
	}
}
