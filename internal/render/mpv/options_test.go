package mpv

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func TestPlaybackArgs(t *testing.T) {
	for _, tc := range []struct{ fit, keep, pan string }{
		{"fill", "yes", "1"}, {"fit", "yes", "0"}, {"stretch", "no", "0"},
	} {
		t.Run(tc.fit, func(t *testing.T) {
			p := render.PlaybackOptions{Fit: tc.fit, Speed: 1.5, Muted: false, Scale: .5}
			a := Args(Options{Hwdec: "d3d11va", Playback: &p}, 42, "ipc", "movie with spaces.mp4", false, 0)
			for _, want := range []string{"--keepaspect=" + tc.keep, "--panscan=" + tc.pan, "--speed=1.5", "--mute=no", "--hwdec=d3d11va-copy", "--vf=" + scaleFilter(.5)} {
				if !slices.Contains(a, want) {
					t.Errorf("missing %q in %v", want, a)
				}
			}
			if slices.Contains(a, "--no-audio") {
				t.Error("audio enabled but --no-audio still present")
			}
			if !reflect.DeepEqual(a[len(a)-2:], []string{"--", "movie with spaces.mp4"}) {
				t.Fatal("filename is not protected from option parsing")
			}
		})
	}
	defaults := Args(Options{Hwdec: "vaapi"}, 42, "ipc", "v.mp4", true, 0)
	for _, want := range []string{"--no-audio", "--mute=yes", "--speed=1", "--panscan=1", "--keepaspect=yes", "--hwdec=vaapi"} {
		if !slices.Contains(defaults, want) {
			t.Errorf("missing safe default %q", want)
		}
	}
	for _, arg := range defaults {
		if strings.HasPrefix(arg, "--vf=") {
			t.Error("100% should not install a software scaling filter")
		}
	}
}

func TestConfigure(t *testing.T) {
	r := New(Options{})
	p := render.PlaybackOptions{Fit: "fit", Speed: 2, Muted: false, Volume: 60, Scale: .75}
	if err := r.Configure(p); err != nil {
		t.Fatal(err)
	}
	if got := playbackOptions(r.o); got != p {
		t.Fatal(got)
	}
	p.Speed = -1
	if err := r.Configure(p); err == nil {
		t.Fatal("accepted invalid speed")
	}
	if playbackOptions(r.o).Speed != 2 {
		t.Fatal("invalid configuration overwrote valid one")
	}
	r.cmd = &exec.Cmd{}
	if err := r.Configure(render.DefaultPlaybackOptions()); err == nil {
		t.Fatal("allowed reconfiguration during playback")
	}
}

func TestResumeOptionsPreservePlaybackAndFilters(t *testing.T) {
	p := render.PlaybackOptions{Fit: "stretch", Speed: .5, Muted: true, Volume: 40, Scale: .75}
	o := Options{Hwdec: "vaapi", Playback: &p}
	got := resumeOptions(o, 3.25, 15)
	want := map[string]string{"start": "3.250", "speed": "0.5", "mute": "yes", "aid": "no", "keepaspect": "no", "panscan": "0", "hwdec": "vaapi-copy", "volume": "40", "vf": scaleFilter(.75) + ",@wpcap:fps=15"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume options=%v want=%v", got, want)
	}
	if got := resumeOptions(o, 0, 0); got["hwdec"] != "vaapi-copy" || got["vf"] != scaleFilter(.75) {
		t.Fatalf("uncapping removed scale support: %v", got)
	}
	p = render.DefaultPlaybackOptions()
	p.Muted = false
	got = resumeOptions(o, 0, 0)
	if got["aid"] != "auto" || got["vf"] != "" || got["hwdec"] != "vaapi" {
		t.Fatalf("default resume retained stale state: %v", got)
	}
}

// Respond to real JSON IPC messages without a running mpv or a display server.
func fakeIPC(t *testing.T, respond func([]json.RawMessage) (any, string)) *client {
	t.Helper()
	a, b := net.Pipe()
	c := newClient(a)
	t.Cleanup(func() { c.Close(); b.Close() })
	go func() {
		sc := bufio.NewScanner(b)
		for sc.Scan() {
			var request struct {
				Command []json.RawMessage `json:"command"`
				ID      int               `json:"request_id"`
			}
			if err := json.Unmarshal(sc.Bytes(), &request); err != nil {
				return
			}
			data, e := respond(request.Command)
			if e == "" {
				e = "success"
			}
			_ = json.NewEncoder(b).Encode(map[string]any{"request_id": request.ID, "error": e, "data": data})
			if string(request.Command[0]) == `"loadfile"` && e == "success" {
				_ = json.NewEncoder(b).Encode(map[string]any{"event": "file-loaded"})
			}
		}
	}()
	return c
}

func TestL3ResumeSendsPlaybackOptions(t *testing.T) {
	p := render.PlaybackOptions{Fit: "fit", Speed: 2, Scale: .5, Muted: false}
	r := New(Options{Hwdec: "no", Playback: &p})
	r.item = render.Item{Path: "movie.mp4"}
	r.pos = 8.5
	r.cap.Store(15)
	r.level.Store(int32(visibility.L3))
	commands := make(chan []json.RawMessage, 8)
	r.c = fakeIPC(t, func(command []json.RawMessage) (any, string) {
		commands <- command
		return nil, ""
	})
	if err := r.SetLevel(visibility.L1); err != nil {
		t.Fatal(err)
	}
	load := <-commands
	var name string
	_ = json.Unmarshal(load[0], &name)
	if name != "loadfile" || len(load) != 5 {
		t.Fatalf("unexpected load: %s", load)
	}
	var options map[string]string
	if err := json.Unmarshal(load[4], &options); err != nil {
		t.Fatal(err)
	}
	want := resumeOptions(r.o, 8.5, 15)
	want["pause"] = "yes"
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("%v != %v", options, want)
	}
	pause := <-commands
	if string(pause[1]) != `"pause"` || string(pause[2]) != "true" {
		t.Fatalf("did not preserve pause: %s", pause)
	}
	if visibility.Level(r.level.Load()) != visibility.L1 {
		t.Fatal("level not updated")
	}
}

func TestL3ResumeFailureKeepsLevel(t *testing.T) {
	r := New(Options{})
	r.level.Store(int32(visibility.L3))
	r.c = fakeIPC(t, func([]json.RawMessage) (any, string) { return nil, "invalid parameter" })
	if err := r.SetLevel(visibility.L0); err == nil {
		t.Fatal("expected failure")
	}
	if visibility.Level(r.level.Load()) != visibility.L3 {
		t.Fatal("failed resume marked as playing")
	}
}

func TestUncapPreservesScalingDecoder(t *testing.T) {
	p := render.DefaultPlaybackOptions()
	p.Scale = .5
	r := New(Options{Hwdec: "vaapi", Playback: &p})
	r.cap.Store(15)
	commands := make(chan []json.RawMessage, 8)
	r.c = fakeIPC(t, func(command []json.RawMessage) (any, string) { commands <- command; return nil, "" })
	if err := r.SetFPSCap(0); err != nil {
		t.Fatal(err)
	}
	remove, decoder := <-commands, <-commands
	if string(remove[2]) != `"@wpcap"` {
		t.Fatalf("wrong filter removed: %s", remove)
	}
	if string(decoder[2]) != `"vaapi-copy"` {
		t.Fatalf("scale requires copy decoder: %s", decoder)
	}
	if r.FPSCap() != 0 {
		t.Fatal("cap not cleared")
	}
}

func TestScaleFilterWithFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	// Execute the exact lavfi expression used by mpv. This tests real decoded
	// frame dimensions, even on machines without an mpv executable.
	f := strings.TrimSuffix(strings.TrimPrefix(scaleFilter(.5), "@wpscale:lavfi=["), "]")
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "info", "-f", "lavfi", "-i", "color=size=160x90:duration=0.1", "-vf", f+",showinfo", "-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("scale filter failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "s:80x44") {
		t.Fatalf("no downscaled frame: %s", out)
	}
}

func TestLivePlaybackDoesNotReloadOrUnpause(t *testing.T) {
	r := New(Options{})
	r.level.Store(int32(visibility.L1))
	r.pos = 12
	commands := make(chan []json.RawMessage, 16)
	r.c = fakeIPC(t, func(c []json.RawMessage) (any, string) { commands <- c; return nil, "" })
	p := render.DefaultPlaybackOptions()
	p.Fit, p.Speed = "stretch", 2
	if err := r.UpdatePlayback(p); err != nil {
		t.Fatal(err)
	}
	want := []string{`["set_property","keepaspect",false]`, `["set_property","panscan",0]`, `["set_property","speed",2]`}
	for _, expected := range want {
		b, _ := json.Marshal(<-commands)
		if string(b) != expected {
			t.Fatalf("command=%s want=%s", b, expected)
		}
	}
	if len(commands) != 0 {
		t.Fatal("unexpected loadfile/pause/extra command")
	}
	if visibility.Level(r.level.Load()) != visibility.L1 || r.pos != 12 {
		t.Fatal("live update changed playback state")
	}
	if got := playbackOptions(r.o); got != p {
		t.Fatalf("options not retained: %+v", got)
	}
	if got := resumeOptions(r.o, 12, 0); got["speed"] != "2" || got["keepaspect"] != "no" {
		t.Fatal("L3 resume would lose live settings", got)
	}
	if err := r.UpdatePlayback(p); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatal("unchanged update should be a no-op")
	}
}

func TestLivePlaybackUnsupportedBeforeWrites(t *testing.T) {
	for _, field := range []string{"audio", "scale"} {
		t.Run(field, func(t *testing.T) {
			r := New(Options{})
			commands := make(chan []json.RawMessage, 16)
			r.c = fakeIPC(t, func(c []json.RawMessage) (any, string) { commands <- c; return nil, "" })
			p := render.DefaultPlaybackOptions()
			p.Fit, p.Speed = "fit", 2
			if field == "audio" {
				p.Muted = false
			} else {
				p.Scale = .5
			}
			if err := r.UpdatePlayback(p); !errors.Is(err, render.ErrUnsupported) {
				t.Fatalf("not a restart fallback: %v", err)
			}
			if len(commands) != 0 {
				t.Fatal("partial update before unsupported rejection")
			}
			if playbackOptions(r.o) != render.DefaultPlaybackOptions() {
				t.Fatal("rejected options retained")
			}
		})
	}
}

func TestLivePlaybackFailureRestoresPreviousProperties(t *testing.T) {
	r := New(Options{})
	commands := make(chan []json.RawMessage, 16)
	r.c = fakeIPC(t, func(c []json.RawMessage) (any, string) {
		commands <- c
		if string(c[1]) == `"speed"` && string(c[2]) == "2" {
			return nil, "property unavailable"
		}
		return nil, ""
	})
	p := render.DefaultPlaybackOptions()
	p.Fit, p.Speed = "stretch", 2
	err := r.UpdatePlayback(p)
	if err == nil || errors.Is(err, render.ErrUnsupported) {
		t.Fatalf("IPC failure must be surfaced: %v", err)
	}
	if got := playbackOptions(r.o); got != render.DefaultPlaybackOptions() {
		t.Fatal("failed update changed stored settings", got)
	}
	for _, expected := range []string{
		`["set_property","keepaspect",false]`, `["set_property","panscan",0]`, `["set_property","speed",2]`,
		`["set_property","speed",1]`, `["set_property","panscan",1]`, `["set_property","keepaspect",true]`,
	} {
		b, _ := json.Marshal(<-commands)
		if string(b) != expected {
			t.Fatalf("command=%s want=%s", b, expected)
		}
	}
	if len(commands) != 0 {
		t.Fatal("unexpected reload or state changes")
	}
}

func TestLivePlaybackValidationAndStoppedPlayer(t *testing.T) {
	r := New(Options{})
	for _, p := range []render.PlaybackOptions{render.DefaultPlaybackOptions(), {Speed: -1}} {
		if err := r.UpdatePlayback(p); err == nil || errors.Is(err, render.ErrUnsupported) {
			t.Fatalf("wrong error: %v", err)
		}
	}
}

func TestSourceSwapPreservesPausePositionAndCap(t *testing.T) {
	r := New(Options{})
	r.level.Store(int32(visibility.L1))
	r.cap.Store(15)
	commands := make(chan []json.RawMessage, 16)
	r.c = fakeIPC(t, func(c []json.RawMessage) (any, string) {
		commands <- c
		if string(c[0]) == `"get_property_string"` {
			return "12.5", ""
		}
		return nil, ""
	})
	if err := r.UpdateSource(render.Item{Path: "cover.mp4"}, visibility.L1); err != nil {
		t.Fatal(err)
	}
	<-commands
	load := <-commands
	if string(load[0]) != `"loadfile"` || string(load[1]) != `"cover.mp4"` {
		t.Fatal(load)
	}
	var opts map[string]string
	if err := json.Unmarshal(load[4], &opts); err != nil {
		t.Fatal(err)
	}
	if opts["start"] != "12.500" || opts["pause"] != "yes" || !strings.Contains(opts["vf"], "fps=15") {
		t.Fatal(opts)
	}
	pause := <-commands
	if string(pause[1]) != `"pause"` || string(pause[2]) != "true" {
		t.Fatal(pause)
	}
	if err := r.UpdateSource(render.Item{Path: "later.mp4", Start: 12.5}, visibility.L3); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 || r.item.Path != "later.mp4" {
		t.Fatal("L3 should defer load")
	}
}

func TestL3ResumeUsesUpdatedSource(t *testing.T) {
	r := New(Options{})
	r.item = render.Item{Path: "old.mp4"}
	r.level.Store(int32(visibility.L3))
	var path string
	r.c = fakeIPC(t, func(c []json.RawMessage) (any, string) {
		if string(c[0]) == `"loadfile"` {
			_ = json.Unmarshal(c[1], &path)
		}
		return nil, ""
	})
	if err := r.UpdateSource(render.Item{Path: "new.mp4", Start: 7}, visibility.L3); err != nil {
		t.Fatal(err)
	}
	if err := r.SetLevel(visibility.L0); err != nil {
		t.Fatal(err)
	}
	if path != "new.mp4" || r.level.Load() != 0 {
		t.Fatal(path, r.level.Load())
	}
}

func TestLoadWaitsForFileLoadedBeforePause(t *testing.T) {
	a, b := net.Pipe()
	c := newClient(a)
	defer c.Close()
	defer b.Close()
	loaded := make(chan struct{})
	commands := make(chan []json.RawMessage, 2)
	go func() {
		sc := bufio.NewScanner(b)
		for sc.Scan() {
			var req struct {
				Command []json.RawMessage `json:"command"`
				ID      int               `json:"request_id"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				return
			}
			commands <- req.Command
			_ = json.NewEncoder(b).Encode(map[string]any{"request_id": req.ID, "error": "success"})
			if string(req.Command[0]) == `"loadfile"` {
				<-loaded
				_ = json.NewEncoder(b).Encode(map[string]any{"event": "file-loaded"})
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- c.loadFile("new.mp4", map[string]string{"pause": "no"}, false) }()
	<-commands
	select {
	case err := <-done:
		t.Fatalf("returned before load: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(loaded)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cmd := <-commands
	if string(cmd[0]) != `"set_property"` || string(cmd[2]) != "false" {
		t.Fatal(cmd)
	}
}

func TestIPCWriteDeadlineUnblocksQueue(t *testing.T) {
	a, b := net.Pipe() // Peer deliberately never reads the command.
	c := newClient(a)
	defer c.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() { _, err := c.cmd("set_property", "pause", false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write deadline did not unblock the renderer operation")
	}
}

// Reject deadlines to exercise the guard used by non-pollable transports.
type noDeadlineIPC struct{ net.Conn }

func (p noDeadlineIPC) SetWriteDeadline(time.Time) error { return errors.New("unsupported") }

func TestIPCWriteFallbackClosesBlockedTransport(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newClient(noDeadlineIPC{a})
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.cmd("set_property", "pause", false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close guard failed")
	}
	select {
	case <-c.closed:
	case <-time.After(time.Second):
		t.Fatal("fallback did not close transport")
	}
}

func TestIPCWriteDeadlineKeepsConnectionUsable(t *testing.T) {
	a, b := net.Pipe()
	c := newClient(a)
	defer c.Close()
	defer b.Close()
	// No peer reader: only the write deadline should expire, not the connection.
	if _, err := c.cmd("set_property", "pause", true); err == nil {
		t.Fatal("blocked write succeeded")
	}
	go func() {
		sc := bufio.NewScanner(b)
		for sc.Scan() {
			var req struct {
				RequestID int `json:"request_id"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				return
			}
			if json.NewEncoder(b).Encode(map[string]any{"request_id": req.RequestID, "error": "success"}) != nil {
				return
			}
		}
	}()
	if err := c.setPause(false); err != nil {
		t.Fatalf("connection unusable after write timeout: %v", err)
	}
}
