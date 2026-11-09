// Package platform defines the single-screen ports. Unknown evidence is never
// affirmative. Native implementations marshal calls onto their owning thread.
package platform

import (
	"context"
	"errors"
)

type Truth string

const (
	Unknown Truth = "unknown"
	Yes     Truth = "yes"
	No      Truth = "no"
)

func Known(v bool) Truth {
	if v {
		return Yes
	}
	return No
}

type SurfaceEvidence struct {
	Visible  Truth   `json:"visible"`
	OnScreen Truth   `json:"on_screen"`
	Occluded Truth   `json:"occluded"` // Informational only; never gates confirmation.
	Attached Truth   `json:"attached"`
	Window   uintptr `json:"window"`
	Parent   uintptr `json:"parent"`
}
type PlayerEvidence struct {
	Ready    Truth    `json:"ready_for_display"`
	Position *float64 `json:"position"`
	Frames   *uint64  `json:"presented_frames"`
	Loops    *uint64  `json:"loops"`
	ChildPID int      `json:"child_pid"`
}

// Surface is initially hidden. The session reveals it only after Player reports
// a frame ready. Calls are serialized by the session, never from the UI thread.
// Close destroys the native window after Player.Close has joined its workers.
type Surface interface {
	Native() uintptr
	Size() (int, int)
	Show() error
	Evidence() (SurfaceEvidence, error)
	Close() error
}

// Player owns decoder objects or the child process and its IPC workers. Evidence
// must query native state or a fresh observation; Close joins owned workers.
// All methods after Load are serialized by the session owner goroutine.
type Player interface {
	Evidence(context.Context) (PlayerEvidence, error)
	Close() error
}

// Loaded transfers ownership even when Load also returns an error. A cancelled
// or stale result MUST still be closed: cancellation does not undo native work.
type Loaded struct {
	Surface Surface
	Player  Player
}

func (r Loaded) Close() error {
	var a, b error
	if r.Player != nil {
		a = r.Player.Close()
	}
	if r.Surface != nil {
		b = r.Surface.Close()
	}
	return errors.Join(a, b)
}

type Request struct {
	Path    string `json:"path"`
	Backend string `json:"backend"`
}

// Loader owns partial resources until return, must honor cancellation and finish
// within the application's shutdown budget. The receiver closes every result.
type Loader func(context.Context, Request) (Loaded, error)
