package ipc

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLifecycleTimeoutPhases(t *testing.T) {
	for _, phase := range []string{"write", "response"} {
		t.Run(phase, func(t *testing.T) {
			a, b := net.Pipe()
			defer b.Close()
			c := New(a, nil, nil)
			defer c.Close()
			var mu sync.Mutex
			var events []string
			var timeoutPhase string
			begun := make(chan struct{})
			c.SetLifecycleLogger(func(msg string, fields ...any) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, msg)
				if msg == "write_begin" {
					close(begun)
				}
				if msg == "timeout" {
					for i := 0; i < len(fields); i += 2 {
						if fields[i] == "phase" {
							timeoutPhase = fields[i+1].(string)
						}
					}
				}
			})
			done := make(chan error, 1)
			go func() { done <- c.Call("setPause", nil, 100*time.Millisecond, nil) }()
			select {
			case <-begun:
			case <-time.After(time.Second):
				t.Fatal("missing begin")
			}
			if phase == "response" {
				if _, err := bufio.NewReader(b).ReadBytes('\n'); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "TIMEOUT") {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("call did not finish")
			}
			mu.Lock()
			defer mu.Unlock()
			if timeoutPhase != phase || len(events) != 3 || events[0] != "write_begin" || events[1] != "write_end" || events[2] != "timeout" {
				t.Fatal(events, timeoutPhase)
			}
		})
	}
}
