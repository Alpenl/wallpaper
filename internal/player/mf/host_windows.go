//go:build windows

package mf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"wpapp/internal/platform"
)

type transport struct {
	io.ReadCloser
	io.WriteCloser
}

func (t transport) Close() error { return errors.Join(t.ReadCloser.Close(), t.WriteCloser.Close()) }

type loadRequest struct {
	Path          string
	Window        uintptr
	Width, Height uint32
}
type observation struct {
	Evidence platform.PlayerEvidence
	Reason   string
}

// HostMain uses inherited anonymous pipes: no public listener or separate UI loop.
// The parent owns the process; EOF stops the engine, and Job termination handles
// abrupt parent death. Only the render thread touches COM/D3D.
func HostMain() int {
	var mu sync.Mutex
	var engine *mfEngine
	c := newConn(transport{os.Stdin, os.Stdout}, func(method string, p json.RawMessage) (any, *Error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "load":
			var r loadRequest
			if err := json.Unmarshal(p, &r); err != nil {
				return nil, &Error{Code: "E_PROTO", Msg: err.Error()}
			}
			if engine != nil || r.Path == "" || r.Window == 0 || r.Width == 0 || r.Height == 0 || r.Width > 16384 || r.Height > 16384 {
				return nil, &Error{Code: "E_LOAD", Msg: "invalid load request"}
			}
			engine = newEngine(r.Path, r.Window, r.Width, r.Height)
			return struct{}{}, nil
		case "evidence":
			if engine == nil {
				return nil, &Error{Code: "E_STATE", Msg: "engine not loaded"}
			}
			e, err := engine.sample()
			e.ChildPID = os.Getpid()
			r := observation{Evidence: e}
			if err != nil {
				r.Reason = err.Error()
			}
			return r, nil
		default:
			return nil, &Error{Code: "E_PROTO", Msg: "unknown method"}
		}
	}, nil)
	<-c.Done()
	mu.Lock()
	defer mu.Unlock()
	if engine != nil {
		if err := engine.close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	return 0
}

// Player owns cmd.Wait and the pipe reader; Close closes IPC, waits briefly for
// render teardown, then kills and joins the child. Methods are session-serialized.
type Player struct {
	cmd    *exec.Cmd
	conn   *Conn
	exited chan struct{}
	log    *platform.BoundedLog
}

func Load(ctx context.Context, path string, s platform.Surface) (*Player, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	p := &Player{cmd: exec.Command(exe, "-mfhost"), exited: make(chan struct{})}
	p.log, err = platform.NewBoundedLog("mf-stderr")
	if err != nil {
		return nil, err
	}
	p.cmd.Stderr = p.log
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		p.log.Close()
		return nil, err
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		p.log.Close()
		return nil, err
	}
	if err = p.cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		p.log.Close()
		return nil, err
	}
	slog.Info("child_start", "backend", "mf", "child_pid", p.cmd.Process.Pid)
	p.conn = newConn(transport{stdout, stdin}, nil, nil)
	p.conn.SetLifecycleLogger(func(msg string, fields ...any) {
		slog.Info(msg, append([]any{"backend", "mf", "child_pid", p.cmd.Process.Pid}, fields...)...)
	})
	go func() { // Player owns Wait; Close joins exited.
		err := p.cmd.Wait()
		p.conn.Close()
		slog.Info("child_exit", "backend", "mf", "child_pid", p.cmd.Process.Pid, "err", err)
		close(p.exited)
	}()
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { p.conn.Close(); p.cmd.Process.Kill(); close(cancelDone) })
	w, h := s.Size()
	err = p.conn.Call("load", loadRequest{path, s.Native(), uint32(w), uint32(h)}, 2*time.Second, nil)
	if !stop() {
		<-cancelDone
	}
	if err == nil {
		err = ctx.Err()
	}
	return p, err
}
func (p *Player) Evidence(ctx context.Context) (platform.PlayerEvidence, error) {
	e := platform.PlayerEvidence{Ready: platform.Unknown, ChildPID: p.cmd.Process.Pid}
	if err := ctx.Err(); err != nil {
		return e, err
	}
	var out observation
	if err := p.conn.Call("evidence", nil, 750*time.Millisecond, &out); err != nil {
		return e, err
	}
	if out.Reason != "" {
		return out.Evidence, errors.New(out.Reason)
	}
	return out.Evidence, nil
}
func (p *Player) Close() error {
	if p.cmd == nil {
		return nil
	}
	p.conn.Close()
	select {
	case <-p.exited:
	case <-time.After(1800 * time.Millisecond):
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-p.exited:
		case <-time.After(500 * time.Millisecond):
			return errors.New("mf: child did not exit after kill")
		}
	}
	<-p.conn.Done()
	p.cmd = nil
	return p.log.Close()
}
