package mpv

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
	"wpapp/internal/render"
)

func TestLifecycleLogAndWarningIdentity(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	var mu sync.Mutex
	var events []string
	var warning map[string]any
	begun := make(chan struct{})
	log := render.LogFunc(func(msg string, fields ...any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, msg)
		if msg == "write_begin" {
			close(begun)
		}
		if msg == "mpv_log" {
			warning = map[string]any{}
			for i := 0; i < len(fields); i += 2 {
				warning[fields[i].(string)] = fields[i+1]
			}
		}
	})
	renderer := New(Options{Display: "A", Log: log})
	c := newClient(a, renderer.childLogger(123))
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.cmd("stop"); done <- err }()
	select {
	case <-begun:
	case <-time.After(10 * time.Second):
		t.Fatal("missing begin before blocked write")
	}
	// Read is deliberately held until the begin record has been observed.
	r := bufio.NewReader(b)
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		ID int `json:"request_id"`
	}
	json.Unmarshal(line, &req)
	enc := json.NewEncoder(b)
	enc.Encode(map[string]any{"event": "log-message", "level": "warn", "prefix": "ffmpeg", "text": "decoder warning"})
	enc.Encode(map[string]any{"request_id": req.ID, "error": "success"})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	ends := 0
	for _, e := range events {
		if e == "response" || e == "timeout" {
			ends++
		}
	}
	if len(events) < 3 || events[0] != "write_begin" || ends != 1 || warning["display"] != "A" || warning["child_pid"] != 123 || warning["text"] != "decoder warning" {
		t.Fatal(events, warning)
	}
}
