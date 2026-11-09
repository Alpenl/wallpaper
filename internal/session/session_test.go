package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wpapp/internal/platform"
)

// No timing guesses: a manual ticker plus published snapshots acknowledge each
// event. Wall time is used only as a bounded test-failure guard.
type manualClock struct {
	mu    sync.Mutex
	now   time.Time
	ticks chan time.Time
}

func newClock() *manualClock {
	return &manualClock{now: time.Unix(100, 0), ticks: make(chan time.Time)}
}
func (c *manualClock) Now() time.Time                 { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualClock) NewTicker(time.Duration) Ticker { return manualTicker{c.ticks} }

type manualTicker struct{ ticks <-chan time.Time }

func (t manualTicker) C() <-chan time.Time { return t.ticks }
func (manualTicker) Stop()                 {}
func (c *manualClock) advance(t *testing.T, d time.Duration) {
	t.Helper()
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	select {
	case c.ticks <- now:
	case <-time.After(3 * time.Second):
		t.Fatal("ticker not consumed")
	}
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("event not received")
		var zero T
		return zero
	}
}

type fakeResources struct {
	mu                          sync.Mutex
	position                    float64
	unknownPosition             bool
	evidenceErr                 error
	occluded                    platform.Truth
	ready                       platform.Truth
	visible                     platform.Truth
	closedPlayer, closedSurface int
	order                       []string
}
type fakePlayer struct{ *fakeResources }
type fakeSurface struct{ *fakeResources }

func fake() (*fakeResources, platform.Loaded) {
	f := &fakeResources{ready: platform.Yes, visible: platform.Yes}
	return f, platform.Loaded{Player: fakePlayer{f}, Surface: fakeSurface{f}}
}
func (f fakePlayer) Evidence(context.Context) (platform.PlayerEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.position
	e := platform.PlayerEvidence{Ready: f.ready, Position: &p}
	if f.unknownPosition {
		e.Position = nil
	}
	return e, f.evidenceErr
}
func (f fakePlayer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedPlayer++
	f.order = append(f.order, "player")
	return nil
}
func (f fakeSurface) Native() uintptr  { return 1 }
func (f fakeSurface) Size() (int, int) { return 1920, 1080 }
func (f fakeSurface) Show() error      { return nil }
func (f fakeSurface) Evidence() (platform.SurfaceEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return platform.SurfaceEvidence{Visible: f.visible, OnScreen: platform.Yes, Attached: platform.Yes, Occluded: f.occluded}, nil
}
func (f fakeSurface) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedSurface++
	f.order = append(f.order, "surface")
	return nil
}
func (f *fakeResources) closed(t *testing.T, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedPlayer != want || f.closedSurface != want {
		t.Fatalf("close counts: player=%d surface=%d want=%d", f.closedPlayer, f.closedSurface, want)
	}
	if want == 1 && strings.Join(f.order, ",") != "player,surface" {
		t.Fatalf("destroy order: %v", f.order)
	}
}
func (f *fakeResources) move(pos float64) { f.mu.Lock(); f.position = pos; f.mu.Unlock() }

type loadCall struct {
	ctx      context.Context
	request  platform.Request
	complete chan platform.Loaded
}
type harness struct {
	s      *Session
	clock  *manualClock
	states chan State
	calls  chan loadCall
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{clock: newClock(), states: make(chan State, 64), calls: make(chan loadCall, 16)}
	h.s = New(func(ctx context.Context, r platform.Request) (platform.Loaded, error) {
		c := loadCall{ctx, r, make(chan platform.Loaded, 1)}
		h.calls <- c
		// Deliberately permits completion after cancellation, exercising native work.
		return <-c.complete, nil
	}, Options{Clock: h.clock, StartupTimeout: 10 * time.Second, Observe: func(st State) error { h.states <- st; return nil }})
	t.Cleanup(func() { h.s.Stop(); receive(t, h.s.Done()) })
	return h
}
func (h *harness) start(t *testing.T, path string) loadCall {
	h.s.Play(platform.Request{Path: path, Backend: "fake"})
	st := receive(t, h.states)
	if st.Phase != "starting" {
		t.Fatalf("start: %+v", st)
	}
	return receive(t, h.calls)
}
func (h *harness) loaded(t *testing.T, c loadCall, r platform.Loaded) {
	c.complete <- r
	receive(t, h.states)
}
func (h *harness) tick(t *testing.T, d time.Duration) State {
	h.clock.advance(t, d)
	return receive(t, h.states)
}
func TestEvidenceTimeout(t *testing.T) {
	h := setup(t)
	c := h.start(t, "video")
	f, r := fake()
	f.ready = platform.Unknown
	h.loaded(t, c, r)
	if st := h.tick(t, 2*time.Second); st.Phase != "starting" {
		t.Fatalf("unknown confirmed: %+v", st)
	}
	st := h.tick(t, 8*time.Second)
	if st.Phase != "failed" || !strings.Contains(st.Reason, "timeout") {
		t.Fatalf("timeout: %+v", st)
	}
	f.closed(t, 1)
}
func TestStopDuringLoadAndRepeatedStop(t *testing.T) {
	h := setup(t)
	c := h.start(t, "video")
	h.s.Stop()
	h.s.Stop()
	st := receive(t, h.states)
	if st.Phase != "stopped" {
		t.Fatal(st)
	}
	select {
	case <-c.ctx.Done():
	default:
		t.Fatal("load was not cancelled")
	}
	select {
	case <-h.s.Done():
		t.Fatal("Stop discarded pending side effects")
	default:
	}
	f, r := fake()
	h.loaded(t, c, r)
	receive(t, h.s.Done())
	f.closed(t, 1)
}
func TestStaleGenerationAndOutOfOrderCompletion(t *testing.T) {
	for _, newFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-first", true: "new-first"}[newFirst], func(t *testing.T) {
			h := setup(t)
			old := h.start(t, "old")
			newer := h.start(t, "new")
			a, ar := fake()
			b, br := fake()
			if newFirst {
				h.loaded(t, newer, br)
				h.loaded(t, old, ar)
			} else {
				h.loaded(t, old, ar)
				h.loaded(t, newer, br)
			}
			a.closed(t, 1)
			b.closed(t, 0)
			if st := h.tick(t, 2*time.Second); st.Phase != "starting" {
				t.Fatal(st)
			}
			b.move(1)
			st := h.tick(t, 2*time.Second)
			if st.Phase != "confirmed" || st.Generation != 2 || st.ConfirmedGeneration != 2 || st.Desired.Path != "new" {
				t.Fatalf("out of order: %+v", st)
			}
			h.s.Stop()
			receive(t, h.s.Done())
			b.closed(t, 1)
		})
	}
}
func TestConfirmedEvidenceLoss(t *testing.T) {
	for _, loss := range []string{"surface", "ready", "stalled"} {
		t.Run(loss, func(t *testing.T) {
			h := setup(t)
			c := h.start(t, "video")
			f, r := fake()
			h.loaded(t, c, r)
			h.tick(t, 2*time.Second)
			f.move(2)
			if st := h.tick(t, 2*time.Second); st.Phase != "confirmed" {
				t.Fatal(st)
			}
			f.mu.Lock()
			switch loss {
			case "surface":
				f.visible = platform.No
				f.position = 4
			case "ready":
				f.ready = platform.Unknown
				f.position = 4
			}
			f.mu.Unlock()
			for i := 0; i < 2; i++ {
				st := h.tick(t, 2*time.Second)
				if st.Phase != "confirmed" {
					t.Fatalf("failed before third miss: %+v", st)
				}
				f.closed(t, 0)
			}
			st := h.tick(t, 2*time.Second)
			if st.Phase != "failed" || !strings.Contains(st.Reason, "evidence lost") {
				t.Fatalf("lost evidence: %+v", st)
			}
			f.closed(t, 1)
		})
	}
}
func TestTimeoutPendingLoadClosesLateResult(t *testing.T) {
	h := setup(t)
	c := h.start(t, "video")
	if st := h.tick(t, 10*time.Second); st.Phase != "failed" {
		t.Fatal(st)
	}
	f, r := fake()
	h.loaded(t, c, r)
	f.closed(t, 1)
}
func TestStatusWriteFailureCleansResources(t *testing.T) {
	clock := newClock()
	f, r := fake()
	failed := make(chan struct{}, 1)
	s := New(func(context.Context, platform.Request) (platform.Loaded, error) { return r, nil }, Options{Clock: clock, Observe: func(st State) error {
		if st.Phase == "failed" {
			failed <- struct{}{}
		}
		if st.Phase == "starting" && len(st.Pending) == 0 {
			return errors.New("disk full")
		}
		return nil
	}})
	s.Play(platform.Request{Path: "video"})
	receive(t, failed)
	s.Stop()
	receive(t, s.Done())
	f.closed(t, 1)
}
func TestWriteStatusAtomicReplacementAndFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	for _, phase := range []string{"starting", "confirmed"} {
		if err := WriteStatus(path, State{Phase: phase}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"confirmed"`) {
		t.Fatalf("snapshot: %s %v", b, err)
	}
	// Rename onto a directory fails; the previous target remains intact and no
	// partial temporary snapshot survives.
	blocked := filepath.Join(dir, "blocked")
	if err = os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err = WriteStatus(blocked, State{}); err == nil {
		t.Fatal("expected rename failure")
	}
	if err = WriteStatus(filepath.Join(dir, "missing", "status"), State{}); err == nil {
		t.Fatal("expected create failure")
	}
	entries, err := filepath.Glob(filepath.Join(dir, ".wallpaper-status-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary files: %v %v", entries, err)
	}
}

func confirmedHarness(t *testing.T) (*harness, *fakeResources) {
	t.Helper()
	h := setup(t)
	c := h.start(t, "video")
	f, r := fake()
	h.loaded(t, c, r)
	h.tick(t, 2*time.Second)
	f.move(10)
	if st := h.tick(t, 2*time.Second); st.Phase != "confirmed" {
		t.Fatal(st)
	}
	return h, f
}
func TestLoopWrapCountsAsProgress(t *testing.T) {
	h, f := confirmedHarness(t)
	f.move(0.1) // No loop counter: it may lag behind the native position.
	if st := h.tick(t, 2*time.Second); st.Phase != "confirmed" || st.Progress != platform.Yes {
		t.Fatal(st)
	}
}
func TestSoftEvidenceRecoveryResetsMisses(t *testing.T) {
	for _, missing := range []string{"ready", "position", "surface"} {
		t.Run(missing, func(t *testing.T) {
			h, f := confirmedHarness(t)
			for round := 0; round < 2; round++ {
				f.mu.Lock()
				switch missing {
				case "ready":
					f.ready = platform.Unknown
				case "position":
					f.unknownPosition = true
				case "surface":
					f.visible = platform.No
				}
				f.mu.Unlock()
				for i := 0; i < 2; i++ {
					if st := h.tick(t, 2*time.Second); st.Phase != "confirmed" {
						t.Fatal(st)
					}
				}
				f.mu.Lock()
				f.ready = platform.Yes
				f.visible = platform.Yes
				f.unknownPosition = false
				f.position += 1
				f.mu.Unlock()
				if st := h.tick(t, 2*time.Second); st.Phase != "confirmed" || st.Progress != platform.Yes {
					t.Fatalf("recovery: %+v", st)
				}
			}
			f.closed(t, 0)
		})
	}
}
func TestFailedStopPreservesPhaseAndReason(t *testing.T) {
	h, f := confirmedHarness(t)
	f.mu.Lock()
	f.evidenceErr = errors.New("native player failed")
	f.mu.Unlock()
	failed := h.tick(t, 2*time.Second) // A hard error must fail on the first sample.
	if failed.Phase != "failed" || !strings.Contains(failed.Reason, "native player failed") {
		t.Fatal(failed)
	}
	h.s.Stop()
	st := receive(t, h.states)
	receive(t, h.s.Done())
	if st.Phase != "failed" || st.Reason != failed.Reason {
		t.Fatalf("Stop overwrote failure: %+v", st)
	}
	if st = h.s.Snapshot(); st.Phase != "failed" || st.Reason != failed.Reason {
		t.Fatalf("final snapshot: %+v", st)
	}
	f.closed(t, 1)
}
func TestOcclusionIsInformational(t *testing.T) {
	h, f := confirmedHarness(t)
	f.mu.Lock()
	f.occluded = platform.Yes
	f.mu.Unlock()
	for i := 0; i < 4; i++ {
		f.move(float64(i + 11))
		st := h.tick(t, 2*time.Second)
		if st.Phase != "confirmed" || st.Occluded != platform.Yes {
			t.Fatal(st)
		}
		data, err := json.Marshal(st)
		if err != nil || !strings.Contains(string(data), `"occluded":"yes"`) {
			t.Fatalf("occlusion JSON: %s %v", data, err)
		}
	}
}
