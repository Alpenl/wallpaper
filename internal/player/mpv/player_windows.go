//go:build windows

// Package mpv is the temporary M1 comparison backend. The session owns all IPC
// calls; only cmd.Wait has a worker, joined by Close. No generic player framework.
package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"time"

	"wpapp/internal/platform"
	"wpapp/internal/platform/win"
)

var errUnavailable = errors.New("mpv: property unavailable")

type Player struct {
	cmd      *exec.Cmd
	pipe     *os.File
	reader   *bufio.Reader
	exited   chan struct{}
	log      *platform.BoundedLog
	next     int
	position *float64
	loops    uint64
}

func Load(ctx context.Context, path, executable string, s platform.Surface) (*Player, error) {
	if executable == "" {
		executable = "mpv"
	}
	exe, err := exec.LookPath(executable)
	if err != nil {
		return nil, err
	}
	logPath, err := platform.LogPath("mpv")
	if err != nil {
		return nil, err
	}
	if err = platform.RotateLog(logPath); err != nil {
		return nil, err
	}
	stderr, err := platform.NewBoundedLog("mpv-stderr")
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf(`\\.\pipe\wallpaper-m1-mpv-%d-%d`, os.Getpid(), time.Now().UnixNano())
	args := []string{"--no-config", "--idle=yes", "--force-window=yes", "--keep-open=no", "--loop-file=inf", "--no-audio", "--no-terminal", "--input-default-bindings=no", "--input-vo-keyboard=no", "--osc=no", "--osd-level=0", "--video-unscaled=no", "--panscan=1", "--wid=" + strconv.FormatUint(uint64(s.Native()), 10), "--input-ipc-server=" + name, "--log-file=" + logPath, "--msg-level=all=warn", "--", path}
	p := &Player{cmd: exec.Command(exe, args...), exited: make(chan struct{}), log: stderr}
	p.cmd.Stderr = stderr
	if err = ctx.Err(); err != nil {
		stderr.Close()
		return nil, err
	}
	if err = p.cmd.Start(); err != nil {
		stderr.Close()
		return nil, err
	}
	slog.Info("child_start", "backend", "mpv", "child_pid", p.cmd.Process.Pid)
	go func() { // Player owns Wait until Close joins exited.
		err := p.cmd.Wait()
		slog.Info("child_exit", "backend", "mpv", "child_pid", p.cmd.Process.Pid, "err", err)
		close(p.exited)
	}()
	p.pipe, err = win.DialPipe(name, time.Second)
	if err != nil {
		return p, err
	}
	p.reader = bufio.NewReaderSize(p.pipe, 64<<10)
	if err = ctx.Err(); err != nil {
		return p, err
	}
	_, err = p.call(ctx, []any{"observe_property", 1, "time-pos"})
	if err == nil {
		_, err = p.call(ctx, []any{"request_log_messages", "warn"})
	}
	return p, err
}
func (p *Player) call(ctx context.Context, command []any) (json.RawMessage, error) {
	deadline := time.Now().Add(750 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.pipe.SetDeadline(deadline); err != nil {
		return nil, err
	}
	p.next++
	payload, err := json.Marshal(map[string]any{"command": command, "request_id": p.next})
	if err != nil {
		return nil, err
	}
	if _, err = p.pipe.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("mpv IPC write: %w", err)
	}
	for {
		// ReadSlice gives a fixed upper bound; a long protocol line is a failure.
		line, err := p.reader.ReadSlice('\n')
		if err != nil {
			return nil, fmt.Errorf("mpv IPC response: %w", err)
		}
		var m struct {
			RequestID int             `json:"request_id"`
			Error     string          `json:"error"`
			Event     string          `json:"event"`
			Name      string          `json:"name"`
			Data      json.RawMessage `json:"data"`
			Text      string          `json:"text"`
			Level     string          `json:"level"`
		}
		if err = json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		if m.Event == "property-change" && m.Name == "time-pos" {
			previous := p.position
			p.position = nil
			if string(m.Data) != "null" {
				var pos float64
				if json.Unmarshal(m.Data, &pos) == nil {
					if previous != nil && pos < *previous {
						p.loops++
					}
					p.position = &pos
				}
			}
		}
		if m.Event == "log-message" {
			slog.Warn("mpv", "child_pid", p.cmd.Process.Pid, "level", m.Level, "message", m.Text)
		}
		if m.RequestID == p.next {
			if m.Error == "property unavailable" {
				return nil, errUnavailable
			}
			if m.Error != "success" {
				return nil, fmt.Errorf("mpv IPC response: %s", m.Error)
			}
			return m.Data, nil
		}
	}
}
func (p *Player) Evidence(ctx context.Context) (platform.PlayerEvidence, error) {
	e := platform.PlayerEvidence{Ready: platform.Unknown, ChildPID: p.cmd.Process.Pid}
	select {
	case <-p.exited:
		return e, errors.New("mpv: child exited")
	default:
	}
	// A freshly queried time-pos supplements the observed property; cached IPC
	// events alone must not confirm a hung decoder or an unresponsive child.
	pos, err := p.call(ctx, []any{"get_property", "time-pos"})
	if errors.Is(err, errUnavailable) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	var value float64
	if string(pos) != "null" && json.Unmarshal(pos, &value) == nil {
		e.Position = &value
	}
	ready, err := p.call(ctx, []any{"get_property", "vo-configured"})
	if errors.Is(err, errUnavailable) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	var configured bool
	if json.Unmarshal(ready, &configured) == nil {
		e.Ready = platform.Known(configured && e.Position != nil)
	}
	loops := p.loops
	e.Loops = &loops
	return e, nil
}
func (p *Player) Close() error {
	if p.cmd == nil {
		return nil
	}
	if p.pipe != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, _ = p.call(ctx, []any{"quit"})
		cancel()
		p.pipe.Close()
	}
	select {
	case <-p.exited:
	case <-time.After(500 * time.Millisecond):
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-p.exited:
		case <-time.After(time.Second):
			return errors.New("mpv: child did not exit after kill")
		}
	}
	p.cmd = nil
	return p.log.Close()
}
