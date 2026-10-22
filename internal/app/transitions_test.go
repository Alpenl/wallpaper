package app

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/visibility"
)

type transitionSink chan map[string]any

func (s transitionSink) Write(b []byte) (int, error) {
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return 0, err
	}
	s <- v
	return len(b), nil
}

type heldLevel struct {
	*fakeR
	entered, release chan struct{}
}

func (r *heldLevel) SetLevel(visibility.Level) error { close(r.entered); <-r.release; return nil }
func (r *heldLevel) ChildPID() int                   { return 456 }
func TestTransitionBeginBeforeBlockingCall(t *testing.T) {
	sink := make(transitionSink, 8)
	a := &App{log: slog.New(slog.NewJSONHandler(sink, nil)), post: make(chan func(), 8), stopped: make(chan struct{})}
	r := &heldLevel{fakeR: &fakeR{}, entered: make(chan struct{}), release: make(chan struct{})}
	s := &screen{d: desktop.Display{ID: "A"}, item: &config.Item{ID: "item-x"}, r: r, gen: 7, worker: &screenWorker{}, ops: make(chan func(), 1)}
	a.applyLevel(s, visibility.L1)
	done := make(chan struct{})
	go func() { (<-s.ops)(); close(done) }()
	select {
	case <-r.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter")
	}
	var begin map[string]any
	select {
	case begin = <-sink:
	default:
		close(r.release)
		t.Fatal("begin missing before blocking call")
	}
	close(r.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	end := <-sink
	if begin["msg"] != "transition_begin" || end["msg"] != "transition_end" || len(sink) != 0 {
		t.Fatal(begin, end)
	}
	for _, key := range []string{"display", "item", "backend", "child_pid", "generation", "op", "from", "to"} {
		if begin[key] == nil || begin[key] != end[key] {
			t.Fatal(key, begin, end)
		}
	}
	if begin["child_pid"] != float64(456) || end["elapsed_ms"] == nil || end["queue_ms"] == nil {
		t.Fatal(begin, end)
	}
}
