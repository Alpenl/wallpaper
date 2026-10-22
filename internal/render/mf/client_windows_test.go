//go:build windows

package mf

import (
	"encoding/json"
	"errors"
	"net"
	"testing"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func TestClientLivePlaybackAcknowledgment(t *testing.T) {
	a, b := net.Pipe()
	server := ipc.New(a, func(method string, raw json.RawMessage) (any, *ipc.Error) {
		if method != "setPlayback" {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: "unexpected method"}
		}
		o, err := decodePlayback(raw)
		if err != nil {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
		}
		if o.Volume != 37 {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: "volume did not round-trip"}
		}
		if o.Speed == 4 {
			return nil, &ipc.Error{Code: "E_STATE", Msg: "native rate refused"}
		}
		return struct{}{}, nil
	}, nil)
	r := New("", "")
	r.conn = ipc.New(b, nil, nil)
	defer r.conn.Close()
	defer server.Close()
	o := render.PlaybackOptions{Fit: "stretch", Speed: 2, Muted: false, Volume: 37, Scale: 1}
	if err := r.UpdatePlayback(o); err != nil || r.options != o {
		t.Fatalf("update: %v, %+v", err, r.options)
	}
	next := o
	next.Speed = 4
	if err := r.UpdatePlayback(next); err == nil || r.options != o {
		t.Fatal("client committed refused settings")
	}
	r.level = visibility.L3
	if err := r.UpdatePlayback(next); !errors.Is(err, render.ErrUnsupported) || r.options != o {
		t.Fatal("released engine should request restart fallback")
	}
}

func TestClientLevelFailureDoesNotCommit(t *testing.T) {
	a, b := net.Pipe()
	server := ipc.New(a, func(method string, raw json.RawMessage) (any, *ipc.Error) {
		level, err := decodePause(raw)
		if method != "setPause" || err != nil {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: "bad request"}
		}
		if level == 3 {
			return nil, &ipc.Error{Code: "E_STATE", Msg: "stop failed"}
		}
		return map[string]any{"pos": 42.5}, nil
	}, nil)
	r := New("", "")
	r.conn = ipc.New(b, nil, nil)
	defer r.conn.Close()
	defer server.Close()
	if err := r.SetLevel(visibility.L2); err != nil || r.level != visibility.L2 || r.Stats().Pos != 42.5 {
		t.Fatalf("level/position: %v, %d, %+v", err, r.level, r.Stats())
	}
	if err := r.SetLevel(visibility.L3); err == nil || r.level != visibility.L2 {
		t.Fatal("failed L3 transition committed")
	}
	if err := r.SetLevel(visibility.L4); err == nil || r.level != visibility.L2 {
		t.Fatal("invalid level accepted")
	}
	if err := r.SetFPSCap(-1); err == nil {
		t.Fatal("invalid FPS accepted")
	}
}
