//go:build darwin

package mac

import (
	"image"
	"testing"

	"wpapp/internal/desktop"
	"wpapp/internal/platform/macos"
)

func TestCanvasNativeHandlePreservesWindowOwner(t *testing.T) {
	bounds := image.Rect(-1920, -1080, 2560, 1440)
	s := &surface{win: 11, view: 22, b: bounds}
	if s.Native() != 22 || s.win != 11 || s.Bounds() != bounds {
		t.Fatalf("canvas surface lost renderer geometry or ownership: %+v", s)
	}
	if !New(Options{}).Caps().Spanning {
		t.Fatal("native desktop host must advertise its canvas implementation")
	}
}

func TestCanvasRejectsGeometryBeforeCreatingViews(t *testing.T) {
	h := New(Options{})
	h.scrs = []*scr{{d: desktop.Display{ID: "display", Bounds: image.Rect(0, 0, 1920, 1080)}}}
	// No AppKit setup is needed: validation must happen before native calls.
	for _, canvas := range []image.Rectangle{
		{}, image.Rect(0, 0, 640, 480), image.Rect(-1, 0, 32767, 1080),
	} {
		if s, err := h.CreateCanvasSurface("display", canvas); err == nil || s != nil {
			t.Fatalf("invalid canvas %v = %v, %v", canvas, s, err)
		}
	}
	if s, err := h.CreateCanvasSurface("missing", image.Rect(0, 0, 1920, 1080)); err == nil || s != nil {
		t.Fatalf("missing display = %v, %v", s, err)
	}
	if len(h.surfs) != 0 {
		t.Fatal("rejected geometry allocated native windows")
	}
}

func TestClosedCanvasDoesNotTouchReleasedNativeObjects(t *testing.T) {
	oldMain := macos.Main
	macos.Main = func(fn func()) { fn() }
	t.Cleanup(func() { macos.Main = oldMain })
	h := New(Options{})
	s := &surface{h: h, win: 11, closed: true}
	replacement := &surface{h: h, win: 11}
	h.surfs[s.win] = replacement
	if err := s.SetVisible(true); err == nil {
		t.Fatal("closed surface cannot be shown again")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeat close must be harmless: %v", err)
	}
	if h.surfs[s.win] != replacement {
		t.Fatal("late close removed a newer surface with a recycled native address")
	}
}

func TestScreenRefreshPreservesDebouncedSignals(t *testing.T) {
	old := &scr{d: desktop.Display{ID: "stable", Bounds: image.Rect(0, 0, 1920, 1080)}}
	for i := 0; i < 3; i++ {
		old.fs.Push(true)
	}
	// Retain a partially accumulated coverage transition too.
	old.cov.Push(true)
	old.cov.Push(true)
	for _, changedGeometry := range []bool{false, true} {
		next := &scr{d: old.d}
		if changedGeometry {
			next.d.Bounds = image.Rect(-2560, 0, 0, 1440)
		}
		added := &scr{d: desktop.Display{ID: "new"}}
		preserveScreenSignals([]*scr{old}, []*scr{added, next})
		if next.fs != old.fs || next.cov != old.cov {
			t.Fatal("enumeration reset remembered visibility state or debounce count")
		}
		if added.fs.State || added.cov.State {
			t.Fatal("a new output inherited another screen's state")
		}
		if !next.cov.Push(true) {
			t.Fatal("pending coverage transition did not survive enumeration")
		}
		if next.fs.Push(false) || next.fs.Push(false) || !next.fs.Push(false) || next.fs.State {
			t.Fatal("previous fullscreen signal must emit false after three clear samples")
		}
	}
}
