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

func TestRefreshMonitorsPreservesSignals(t *testing.T) {
	prev := []*mon{
		{d: desktop.Display{ID: "mon-a", Bounds: image.Rect(0, 0, 1920, 1080)}, lastFS: true, lastCov: true},
		{d: desktop.Display{ID: "mon-b", Bounds: image.Rect(1920, 0, 3840, 1080)}, lastFS: false, lastCov: true},
	}
	next := []*mon{
		{d: desktop.Display{ID: "mon-a", Bounds: image.Rect(0, 0, 2560, 1440)}},
		{d: desktop.Display{ID: "mon-c", Bounds: image.Rect(2560, 0, 4480, 1080)}},
	}
	preserveMonSignals(prev, next)
	if !next[0].lastFS || !next[0].lastCov {
		t.Fatalf("expected mon-a signals preserved, got fs=%v cov=%v", next[0].lastFS, next[0].lastCov)
	}
	if next[1].lastFS || next[1].lastCov {
		t.Fatalf("new display inherited stale signals: fs=%v cov=%v", next[1].lastFS, next[1].lastCov)
	}
}

func TestCloseBeforeStartIsSafe(t *testing.T) {
	h := New(Options{})
	if err := h.Close(); err != nil {
		t.Fatalf("Close before Start: %v", err)
	}
}

func TestShouldIgnoreClickThroughLayeredOverlay(t *testing.T) {
	if !shouldIgnoreCoverageWindow(wsExLayered|wsExTransparent, "OverlayClass", false) {
		t.Fatal("click-through layered overlay must be ignored")
	}
	if shouldIgnoreCoverageWindow(wsExLayered, "AppClass", false) {
		t.Fatal("opaque layered window without WS_EX_TRANSPARENT should still count")
	}
	if shouldIgnoreCoverageWindow(wsExTransparent, "AppClass", false) {
		t.Fatal("non-layered transparent window should still count")
	}
	if !shouldIgnoreCoverageWindow(wsExToolWindow, "AppClass", false) {
		t.Fatal("tool window must be ignored")
	}
}

func TestShellRestartCommitsOnlyAfterEmit(t *testing.T) {
	h := New(Options{})
	h.events = make(chan desktop.Event, 1)
	h.events <- desktop.Event{Kind: desktop.EventDisplaysChanged} // fill queue
	h.surfaces[101] = &surface{hwnd: 101}
	h.progman, h.workerw, h.defView = 1, 2, 3

	destroyed := 0
	if h.commitShellRestart("health", func(uintptr) { destroyed++ }) {
		t.Fatal("commitShellRestart must fail when event queue is full")
	}
	if destroyed != 0 || len(h.surfaces) != 1 || h.defView != 3 || h.pendingShellRestart != "health" {
		t.Fatalf("state cleared before emit succeeded: destroyed=%d surfs=%d defView=%d pending=%q",
			destroyed, len(h.surfaces), h.defView, h.pendingShellRestart)
	}

	<-h.events // drain queue
	if !h.commitShellRestart(h.pendingShellRestart, func(uintptr) { destroyed++ }) {
		t.Fatal("retry commitShellRestart failed with open queue")
	}
	if destroyed != 1 || len(h.surfaces) != 0 || h.progman != 0 || h.workerw != 0 || h.defView != 0 || h.pendingShellRestart != "" {
		t.Fatalf("state not cleared after emit succeeded: destroyed=%d surfs=%d progman=%d workerw=%d defView=%d pending=%q",
			destroyed, len(h.surfaces), h.progman, h.workerw, h.defView, h.pendingShellRestart)
	}
	ev := <-h.events
	if ev.Kind != desktop.EventShellRestarted || ev.Source != "health" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestSetSystemWallpaperRejectsNULPath(t *testing.T) {
	h := New(Options{})
	if err := h.SetSystemWallpaper("display", "bad\x00path.png"); err == nil {
		t.Fatal("SetSystemWallpaper accepted NUL byte")
	}
}
