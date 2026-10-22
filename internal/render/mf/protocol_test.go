package mf

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
)

func TestUncertainControlErrorClassification(t *testing.T) {
	for _, err := range []error{nil, &ipc.Error{Code: "E_STATE", Msg: "native rate refused"}, fmt.Errorf("wrapped: %w", &ipc.Error{Code: "E_PROTO"})} {
		if uncertainControlError(err) {
			t.Fatalf("acknowledged rejection marked uncertain: %v", err)
		}
	}
	for _, err := range []error{errors.New("E_TIMEOUT: setPlayback"), errors.New("E_PROTO: connection closed"), net.ErrClosed, errors.New("bad response")} {
		if !uncertainControlError(err) {
			t.Fatalf("uncertain outcome allowed to continue: %v", err)
		}
	}
	for _, err := range []error{&ipc.Error{Code: "E_STATE", Fatal: true}, fmt.Errorf("wrapped: %w", &ipc.Error{Code: "E_LOAD", Fatal: true})} {
		if !uncertainControlError(err) {
			t.Fatalf("fatal lifecycle response kept unsafe host alive: %v", err)
		}
	}
}

func TestDecodeLoad(t *testing.T) {
	x, options, err := decodeLoad(json.RawMessage(`{"entry":"C:\\media\\video.mp4","wid":123,"bounds":{"w":3840,"h":1080},"paused":true,"start":12.5,"options":{"fit":"stretch","speed":1.5,"muted":true,"scale":1}}`))
	if err != nil || x.Start != 12.5 || !x.Paused || options.Fit != "stretch" || options.Speed != 1.5 || !options.Muted {
		t.Fatalf("load: %+v, %+v, %v", x, options, err)
	}
	_, options, err = decodeLoad(json.RawMessage(`{"entry":"movie.mp4","wid":1,"bounds":{"w":1,"h":1}}`))
	if err != nil || options != render.DefaultPlaybackOptions() {
		t.Fatalf("legacy load defaults: %+v, %v", options, err)
	}
}

func TestDecodeLoadRejectsInvalidBeforeStart(t *testing.T) {
	for _, p := range []string{
		`null`, `{}`, `[]`, `{"entry":`,
		`{"entry":"x","wid":0,"bounds":{"w":1,"h":1}}`,
		`{"entry":"x\u0000y","wid":1,"bounds":{"w":1,"h":1}}`,
		`{"entry":"  ","wid":1,"bounds":{"w":1,"h":1}}`,
		`{"entry":"x","wid":1,"bounds":{"w":-1,"h":1}}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":0}}`,
		`{"entry":"x","wid":1,"bounds":{"w":16385,"h":1}}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":1},"start":-1}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":1},"start":1e100}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":1},"start":"10"}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":1},"options":{"speed":5}}`,
		`{"entry":"x","wid":1,"bounds":{"w":1,"h":1},"options":{"scale":0.5}}`,
	} {
		if _, _, err := decodeLoad(json.RawMessage(p)); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
}

func TestDecodePauseAndFPS(t *testing.T) {
	for name, decode := range map[string]func(json.RawMessage) (int, error){"level": decodePause, "fps": decodeFPS} {
		for _, p := range []string{`null`, `{}`, `[]`, `{"` + name + `":null}`, `{"` + name + `":-1}`, `{"` + name + `":1000}`, `{"` + name + `":"1"}`, `{"` + name + `":1.5}`} {
			if _, err := decode(json.RawMessage(p)); err == nil {
				t.Fatalf("accepted %s", p)
			}
		}
		for _, v := range []int{0, 1, 3} {
			p, _ := json.Marshal(map[string]int{name: v})
			if got, err := decode(p); err != nil || got != v {
				t.Fatalf("%s: %d, %v", p, got, err)
			}
		}
	}
	if got, err := decodeFPS(json.RawMessage(`{"fps":240}`)); err != nil || got != 240 {
		t.Fatal(got, err)
	}
}

func TestDecodeLiveOptions(t *testing.T) {
	for _, p := range []string{`null`, `{}`, `[]`, `{"options":null}`, `{"options":{"fit":"bad"}}`, `{"options":{"scale":0.5}}`, `{"options":{"speed":-1}}`, `{"options":{"speed":"2"}}`} {
		if _, err := decodePlayback(json.RawMessage(p)); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	o, err := decodePlayback(json.RawMessage(`{"options":{"fit":"fit","speed":2,"muted":false,"scale":1}}`))
	if err != nil || o.Fit != "fit" || o.Speed != 2 || o.Muted {
		t.Fatalf("%+v: %v", o, err)
	}
}

func TestLivePlaybackIPCValidatesAndAcknowledgesAppliedState(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	var calls []string
	s := playbackState{options: render.DefaultPlaybackOptions(), ready: true, paused: true, start: 12}
	api := recordingAPI(&calls)
	server := ipc.New(serverSide, func(method string, raw json.RawMessage) (any, *ipc.Error) {
		if method != "setPlayback" {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: "unknown method"}
		}
		o, err := decodePlayback(raw)
		if err != nil {
			return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
		}
		if err := s.update(api, o); err != nil {
			return nil, &ipc.Error{Code: "E_STATE", Msg: err.Error()}
		}
		return struct {
			Options render.PlaybackOptions
			Paused  bool
			Start   float64
			Calls   int
		}{s.options, s.paused, s.start, len(calls)}, nil
	}, nil)
	client := ipc.New(clientSide, nil, nil)
	defer server.Close()
	defer client.Close()
	if err := client.Call("setPlayback", map[string]any{}, time.Second, nil); err == nil {
		t.Fatal("malformed request succeeded")
	}
	if err := client.Call("setPlayback", map[string]any{"options": render.PlaybackOptions{Scale: .5}}, time.Second, nil); err == nil {
		t.Fatal("unsupported scale succeeded")
	}
	o := render.PlaybackOptions{Fit: "fit", Speed: 2, Muted: false, Scale: 1}
	var result struct {
		Options render.PlaybackOptions
		Paused  bool
		Start   float64
		Calls   int
	}
	if err := client.Call("setPlayback", map[string]any{"options": o}, time.Second, &result); err != nil {
		t.Fatal(err)
	}
	if result.Options != o || !result.Paused || result.Start != 12 || result.Calls != 3 {
		t.Fatalf("unexpected applied state: %+v", result)
	}
}
