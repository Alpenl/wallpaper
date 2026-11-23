package mpv

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wpapp/internal/visibility"
)

func TestGetStringNullReturnsErrUnavailable(t *testing.T) {
	c := fakeIPC(t, func(cmd []json.RawMessage) (any, string) {
		return nil, "success"
	})
	if _, err := c.getString("time-pos"); !errors.Is(err, errUnavailable) {
		t.Fatalf("getString(null) err=%v want errUnavailable", err)
	}
	if _, err := c.getFloat("time-pos"); !errors.Is(err, errUnavailable) {
		t.Fatalf("getFloat(null) err=%v want errUnavailable", err)
	}
}

func TestSetLevelL3UpdatesLevelBeforeStop(t *testing.T) {
	r := New(Options{})
	r.level.Store(int32(visibility.L0))
	var levelDuringStop visibility.Level
	r.c = fakeIPC(t, func(cmd []json.RawMessage) (any, string) {
		if len(cmd) > 0 && string(cmd[0]) == `"stop"` {
			levelDuringStop = visibility.Level(r.level.Load())
		}
		if len(cmd) > 0 && string(cmd[0]) == `"get_property_string"` {
			return "4.2", "success"
		}
		return nil, "success"
	})
	if err := r.SetLevel(visibility.L3); err != nil {
		t.Fatal(err)
	}
	if levelDuringStop != visibility.L3 {
		t.Fatalf("level during stop was %v, want L3", levelDuringStop)
	}

	// Verify rollback if stop fails.
	r2 := New(Options{})
	r2.level.Store(int32(visibility.L0))
	r2.c = fakeIPC(t, func(cmd []json.RawMessage) (any, string) {
		if len(cmd) > 0 && string(cmd[0]) == `"stop"` {
			return nil, "command failed"
		}
		return nil, "success"
	})
	if err := r2.SetLevel(visibility.L3); err == nil {
		t.Fatal("expected stop failure")
	}
	if visibility.Level(r2.level.Load()) != visibility.L0 {
		t.Fatalf("failed stop did not restore previous level: %v", r2.level.Load())
	}
}

func TestIPCEndpointRandomAndCleanup(t *testing.T) {
	r := New(Options{Display: "disp-0"})
	a, cleanA, err := r.ipcEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanA()
	b, cleanB, err := r.ipcEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanB()
	if a == b {
		t.Fatalf("expected distinct random IPC names, got %q", a)
	}
	if runtime.GOOS != "windows" {
		dir := filepath.Dir(a)
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("ipc dir perm=%o want 0700", info.Mode().Perm())
		}
		cleanA()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("cleanup did not remove ipc dir: %v", err)
		}
	} else {
		if !strings.HasPrefix(a, `\\.\pipe\wpd-`) || len(a) < 40 {
			t.Fatalf("unexpected Windows pipe name: %q", a)
		}
	}
}

func TestLoadFileNamedArgsAndIgnoresStaleFileLoaded(t *testing.T) {
	a, b := net.Pipe()
	c := newClient(a)
	defer c.Close()
	defer b.Close()

	gotCmd := make(chan map[string]json.RawMessage, 1)
	allowRealLoaded := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(b)
		for sc.Scan() {
			var req struct {
				Command json.RawMessage `json:"command"`
				ID      int             `json:"request_id"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				return
			}
			args, obj := decodeCommand(req.Command)
			if len(args) > 0 && string(args[0]) == `"loadfile"` {
				gotCmd <- obj
				// Emit a stale file-loaded event BEFORE acknowledging loadfile.
				_ = json.NewEncoder(b).Encode(map[string]any{"event": "file-loaded", "playlist_entry_id": 1})
				// Acknowledge loadfile with playlist_entry_id = 2.
				_ = json.NewEncoder(b).Encode(map[string]any{
					"request_id": req.ID,
					"error":      "success",
					"data":       map[string]any{"playlist_entry_id": 2},
				})
				// Emit another stale event with mismatched playlist_entry_id = 1.
				_ = json.NewEncoder(b).Encode(map[string]any{"event": "file-loaded", "playlist_entry_id": 1})
				<-allowRealLoaded
				_ = json.NewEncoder(b).Encode(map[string]any{"event": "file-loaded", "playlist_entry_id": 2})
				continue
			}
			_ = json.NewEncoder(b).Encode(map[string]any{"request_id": req.ID, "error": "success"})
		}
	}()

	done := make(chan error, 1)
	go func() { done <- c.loadFile("video.mp4", map[string]string{"start": "1.000"}, false) }()

	obj := <-gotCmd
	if obj == nil || string(obj["name"]) != `"loadfile"` || string(obj["url"]) != `"video.mp4"` || string(obj["flags"]) != `"replace"` {
		t.Fatalf("expected named-argument loadfile object, got %v", obj)
	}
	select {
	case err := <-done:
		t.Fatalf("stale file-loaded unblocked loadFile early: %v", err)
	// sleep: observe that stale file-loaded events do not unblock loadFile before the matching event.
	case <-time.After(30 * time.Millisecond):
	}
	close(allowRealLoaded)
	if err := <-done; err != nil {
		t.Fatalf("loadFile failed: %v", err)
	}
}
