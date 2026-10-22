package app

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPersistenceErrorUsesExistingLastError(t *testing.T) {
	var logs bytes.Buffer
	a := &App{log: slog.New(slog.NewTextHandler(&logs, nil)), post: make(chan func(), 1)}
	a.post <- func() {} // A full event queue must not deadlock a shutdown flush.
	finished := make(chan struct{})
	go func() { a.ReportPersistenceError(errors.New("config: save failed")); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("error reporting blocked")
	}
	a.consumePersistenceError()
	if a.lastErr != "config: save failed" {
		t.Fatal(a.lastErr)
	}
	if !strings.Contains(logs.String(), "config persistence failed") {
		t.Fatal(logs.String())
	}
	a.consumePersistenceError()
	if a.lastErr != "config: save failed" {
		t.Fatal("error lost")
	}
}
