// Package session owns one screen's desired, pending and confirmed state.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"wpapp/internal/platform"
)

type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
type wallClock struct{}
type wallTicker struct{ *time.Ticker }

func (wallClock) Now() time.Time                   { return time.Now() }
func (wallClock) NewTicker(d time.Duration) Ticker { return wallTicker{time.NewTicker(d)} }
func (t wallTicker) C() <-chan time.Time           { return t.Ticker.C }

type State struct {
	Session             string            `json:"session"`
	PID                 int               `json:"pid"`
	Phase               string            `json:"phase"`
	Reason              string            `json:"reason"`
	Backend             string            `json:"backend"`
	Generation          uint64            `json:"generation"`
	Pending             []uint64          `json:"pending"`
	Desired             *platform.Request `json:"desired"`
	ConfirmedGeneration uint64            `json:"confirmed_generation"`
	platform.SurfaceEvidence
	platform.PlayerEvidence
	Progress  platform.Truth `json:"progress"`
	Timestamp time.Time      `json:"timestamp"`
}
type Options struct {
	Clock          Clock
	StartupTimeout time.Duration
	SampleEvery    time.Duration
	Observe        func(State) error // synchronous, bounded; failure fails the session
}
type command struct {
	request *platform.Request
	reply   chan State
}
type result struct {
	generation uint64
	resources  platform.Loaded
	err        error
}
type Session struct {
	commands chan command
	results  chan result
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	final    State // Written by owner before Done closes; read only after Done.
}

// New starts the owner. Stop requests cancellation; Done closes only after all
// Load results (including obsolete results) have arrived and been destroyed.
func New(load platform.Loader, o Options) *Session {
	if o.Clock == nil {
		o.Clock = wallClock{}
	}
	if o.StartupTimeout <= 0 {
		o.StartupTimeout = 15 * time.Second
	}
	if o.SampleEvery <= 0 {
		o.SampleEvery = 2 * time.Second
	}
	s := &Session{commands: make(chan command), results: make(chan result), stop: make(chan struct{}), done: make(chan struct{})}
	go s.run(load, o) // Session owns this goroutine; it joins every load before Done.
	return s
}
func (s *Session) Play(r platform.Request) { s.command(&r) }
func (s *Session) Snapshot() State         { return s.command(nil) }
func (s *Session) command(r *platform.Request) State {
	reply := make(chan State, 1)
	select {
	case s.commands <- command{r, reply}:
		return <-reply
	case <-s.done:
		return s.final
	}
}
func (s *Session) Stop()                 { s.once.Do(func() { close(s.stop) }) }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) run(load platform.Loader, o Options) {
	ticker := o.Clock.NewTicker(o.SampleEvery)
	defer ticker.Stop()
	st := State{Session: "primary", PID: os.Getpid(), Phase: "stopped"}
	defer func() { s.final = st; close(s.done) }()
	pending := map[uint64]context.CancelFunc{}
	var current platform.Loaded
	var previous platform.PlayerEvidence
	var deadline time.Time
	shown, stopping := false, false
	softMisses := 0
	stop := s.stop
	resetEvidence := func() {
		st.SurfaceEvidence = platform.SurfaceEvidence{Visible: platform.Unknown, OnScreen: platform.Unknown, Attached: platform.Unknown, Occluded: platform.Unknown}
		st.PlayerEvidence = platform.PlayerEvidence{Ready: platform.Unknown}
		st.Progress = platform.Unknown
		previous = platform.PlayerEvidence{}
		shown = false
		softMisses = 0
		st.ConfirmedGeneration = 0
	}
	publish := func() error {
		st.Pending = make([]uint64, 0, len(pending))
		for gen := range pending {
			st.Pending = append(st.Pending, gen)
		}
		sort.Slice(st.Pending, func(i, j int) bool { return st.Pending[i] < st.Pending[j] })
		st.Timestamp = o.Clock.Now()
		if o.Observe != nil {
			return o.Observe(st)
		}
		return nil
	}
	cleanup := func() error {
		slog.Info("close_begin", "session", st.Session, "generation", st.Generation, "backend", st.Backend)
		err := current.Close()
		current = platform.Loaded{}
		slog.Info("close_end", "session", st.Session, "generation", st.Generation, "err", err)
		return err
	}
	fail := func(err error) {
		st.Phase, st.Reason = "failed", err.Error()
		st.ConfirmedGeneration = 0
		for _, cancel := range pending {
			cancel()
		}
		if err := cleanup(); err != nil {
			st.Reason += "; cleanup: " + err.Error()
			st.Visible, st.Attached = platform.Unknown, platform.Unknown
		} else {
			st.Visible, st.Attached = platform.No, platform.No
		}
		if err := publish(); err != nil {
			slog.Error("status_write", "err", err)
		}
	}
	resetEvidence()
	for {
		if stopping && len(pending) == 0 {
			return
		}
		select {
		case <-stop:
			stop = nil
			stopping = true
			for _, cancel := range pending {
				cancel()
			}
			st.Generation++
			st.Desired = nil
			st.ConfirmedGeneration = 0
			if err := cleanup(); err != nil {
				if st.Reason == "" {
					st.Reason = err.Error()
				} else {
					st.Reason += "; cleanup: " + err.Error()
				}
				st.Visible, st.Attached = platform.Unknown, platform.Unknown
			} else {
				st.Visible, st.Attached = platform.No, platform.No
			}
			if st.Phase != "failed" {
				st.Phase = "stopped"
			}
			if err := publish(); err != nil {
				slog.Error("status_write", "err", err)
			}
		case c := <-s.commands:
			if c.request != nil && !stopping {
				for _, cancel := range pending {
					cancel()
				}
				closeErr := cleanup()
				st.Generation++
				st.Desired = c.request
				st.Backend = c.request.Backend
				st.Phase, st.Reason = "starting", ""
				resetEvidence()
				deadline = o.Clock.Now().Add(o.StartupTimeout)
				if closeErr != nil {
					fail(closeErr)
				} else {
					ctx, cancel := context.WithCancel(context.Background())
					gen := st.Generation
					pending[gen] = cancel
					slog.Info("load_begin", "session", st.Session, "generation", gen, "backend", st.Backend)
					go func(r platform.Request) { // Session owns loader; result is always drained, including after Stop.
						resources, err := load(ctx, r)
						s.results <- result{gen, resources, err}
					}(*c.request)
					if err := publish(); err != nil {
						fail(fmt.Errorf("status write: %w", err))
					}
				}
			}
			c.reply <- st
		case r := <-s.results:
			if cancel := pending[r.generation]; cancel != nil {
				cancel()
				delete(pending, r.generation)
			}
			slog.Info("load_end", "session", st.Session, "generation", r.generation, "err", r.err)
			if stopping || r.generation != st.Generation || st.Phase != "starting" {
				if err := r.resources.Close(); err != nil {
					slog.Error("stale_close", "generation", r.generation, "err", err)
					if st.Reason == "" {
						st.Reason = err.Error()
					} else {
						st.Reason += "; cleanup: " + err.Error()
					}
				}
				if err := publish(); err != nil {
					slog.Error("status_write", "err", err)
				}
				continue
			}
			current = r.resources
			if r.err != nil {
				fail(r.err)
			} else if current.Player == nil || current.Surface == nil {
				fail(errors.New("load returned incomplete resources"))
			} else if err := publish(); err != nil {
				fail(fmt.Errorf("status write: %w", err))
			}
		case <-ticker.C():
			if stopping || st.Phase == "failed" || st.Phase == "stopped" {
				continue
			}
			if st.Phase == "starting" && !o.Clock.Now().Before(deadline) {
				fail(errors.New("evidence timeout: display and playback progress not confirmed"))
				continue
			}
			if current.Player == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			pe, err := current.Player.Evidence(ctx)
			cancel()
			st.PlayerEvidence = pe
			if err != nil {
				fail(fmt.Errorf("player evidence: %w", err))
				continue
			}
			if pe.Ready == platform.Yes && !shown {
				if err := current.Surface.Show(); err != nil {
					fail(err)
					continue
				}
				shown = true
			}
			se, err := current.Surface.Evidence()
			st.SurfaceEvidence = se
			if err != nil {
				fail(fmt.Errorf("surface evidence: %w", err))
				continue
			}
			st.Progress = progress(previous, pe)
			displayed := se.Visible == platform.Yes && se.OnScreen == platform.Yes && se.Attached == platform.Yes && pe.Ready == platform.Yes
			// Preserve the last known baseline through temporary missing evidence,
			// so the first recovered sample can demonstrate progress.
			if displayed && (pe.Position != nil || pe.Frames != nil) {
				previous = pe
			} else if st.Phase != "confirmed" {
				previous = platform.PlayerEvidence{}
			}
			if displayed && st.Progress == platform.Yes {
				softMisses = 0
				st.Phase = "confirmed"
				st.ConfirmedGeneration = st.Generation
			} else if st.Phase == "confirmed" {
				softMisses++
				if softMisses >= 3 {
					fail(fmt.Errorf("evidence lost: visible=%s on_screen=%s attached=%s ready=%s progress=%s", se.Visible, se.OnScreen, se.Attached, pe.Ready, st.Progress))
					continue
				}
			}
			if err := publish(); err != nil {
				fail(fmt.Errorf("status write: %w", err))
			}
		}
	}
}
func progress(a, b platform.PlayerEvidence) platform.Truth {
	if a.Frames != nil && b.Frames != nil && *b.Frames > *a.Frames {
		return platform.Yes
	}
	if a.Position != nil && b.Position != nil {
		if *b.Position != *a.Position {
			return platform.Yes
		}
		return platform.No
	}
	return platform.Unknown
}
