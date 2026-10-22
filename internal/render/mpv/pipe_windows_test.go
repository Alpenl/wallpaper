//go:build windows

package mpv

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"wpapp/internal/platform/win32"
	"wpapp/internal/visibility"
)

func TestSilentPipePauseResume(t *testing.T) {
	ps, err := win32.NewPipeServer()
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	type result struct {
		f   *os.File
		err error
	}
	dial := make(chan result, 1)
	go func() { f, err := win32.DialPipe(ps.Name, 10*time.Second); dial <- result{f, err} }()
	server, err := ps.Accept(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	d := <-dial
	if d.err != nil {
		t.Fatal(d.err)
	}
	c := newClient(d.f)
	defer c.Close()
	commands := make(chan []any, 2)
	go func() {
		sc := bufio.NewScanner(server)
		for sc.Scan() {
			var req struct {
				Command   []any `json:"command"`
				RequestID int   `json:"request_id"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				return
			}
			commands <- req.Command
			if json.NewEncoder(server).Encode(map[string]any{"request_id": req.RequestID, "error": "success"}) != nil {
				return
			}
		}
	}()
	r := New(Options{})
	r.c = c
	for _, level := range []visibility.Level{visibility.L1, visibility.L0} {
		// SetLevel waits for the peer reply before the next command.
		done := make(chan error, 1)
		go func() { done <- r.SetLevel(level) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("SetLevel blocked on silent pipe")
		}
		select {
		case got := <-commands:
			want := []any{"set_property", "pause", level != visibility.L0}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("command=%v want=%v", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("missing pause command")
		}
	}
}
