package mf

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
)

// Ordinary typed rejections preserve a usable session. Fatal lifecycle failures
// do not: shutdown may already have closed the render loop's quit channel.
// A timeout/disconnect is also unsafe because its request may apply later.
func uncertainControlError(err error) bool {
	if err == nil {
		return false
	}
	var response *ipc.Error
	return !errors.As(err, &response) || response.Fatal
}

func lifecycleFailure(operation string, err error) *ipc.Error {
	return &ipc.Error{Code: "E_STATE", Msg: fmt.Sprintf("mf: %s: %v", operation, err), Fatal: true}
}

type loadRequest struct {
	Entry   string                  `json:"entry"`
	Wid     uint64                  `json:"wid"`
	Bounds  struct{ W, H int32 }    `json:"bounds"`
	Paused  bool                    `json:"paused"`
	Start   float64                 `json:"start"`
	Options *render.PlaybackOptions `json:"options"`
}

func decodeLoad(p json.RawMessage) (loadRequest, render.PlaybackOptions, error) {
	var x loadRequest
	o := render.DefaultPlaybackOptions()
	if err := json.Unmarshal(p, &x); err != nil {
		return x, o, err
	}
	if strings.TrimSpace(x.Entry) == "" || strings.ContainsRune(x.Entry, 0) || x.Wid == 0 {
		return x, o, fmt.Errorf("entry and nonzero window handle are required")
	}
	// D3D11's maximum 2D texture dimension. Do not wrap signed bounds to huge
	// unsigned allocations or silently clip an unsupported desktop canvas.
	if x.Bounds.W <= 0 || x.Bounds.H <= 0 || x.Bounds.W > 16384 || x.Bounds.H > 16384 {
		return x, o, fmt.Errorf("bounds must be between 1 and 16384 pixels")
	}
	if !validPosition(x.Start) {
		return x, o, fmt.Errorf("invalid start position")
	}
	if x.Options != nil {
		var err error
		o, err = playbackOptions(*x.Options)
		if err != nil {
			return x, o, err
		}
	}
	return x, o, nil
}

func decodePause(p json.RawMessage) (int, error) {
	var x struct {
		Level *int `json:"level"`
	}
	if err := json.Unmarshal(p, &x); err != nil {
		return 0, err
	}
	if x.Level == nil || *x.Level < 0 || *x.Level > 3 {
		return 0, fmt.Errorf("level must be between 0 and 3")
	}
	return *x.Level, nil
}

func decodeFPS(p json.RawMessage) (int, error) {
	var x struct {
		FPS *int `json:"fps"`
	}
	if err := json.Unmarshal(p, &x); err != nil {
		return 0, err
	}
	if x.FPS == nil || *x.FPS < 0 || *x.FPS > 240 {
		return 0, fmt.Errorf("fps must be between 0 and 240")
	}
	return *x.FPS, nil
}

func decodePlayback(p json.RawMessage) (render.PlaybackOptions, error) {
	var x struct {
		Options *render.PlaybackOptions `json:"options"`
	}
	if err := json.Unmarshal(p, &x); err != nil {
		return render.PlaybackOptions{}, err
	}
	if x.Options == nil {
		return render.PlaybackOptions{}, fmt.Errorf("playback options are required")
	}
	return playbackOptions(*x.Options)
}

func helloReply(inJob bool) map[string]any {
	identity := render.Identity()
	return map[string]any{"pid": identity.PID, "build": identity.Build, "caps": []string{"video", "mf", "fit", "speed", "audio", "resume", "fps", "live-playback"}, "inJob": inJob}
}
