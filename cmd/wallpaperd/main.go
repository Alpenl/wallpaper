package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egoist/mygo"

	"wpapp/internal/platform"
	"wpapp/internal/session"
)

func main() { os.Exit(run()) }
func run() int {
	play := flag.String("play", "", "video path")
	backend := flag.String("backend", "mf", "Windows backend: mf or mpv")
	mpv := flag.String("mpv", "", "mpv executable path")
	status := flag.String("status", "", "atomic JSON status path")
	host := flag.Bool("mfhost", false, "internal Media Foundation child")
	flag.Parse()
	// Both parent and child configure a per-process crash sink before dispatch.
	crashPath, err := platform.LogPath(fmt.Sprintf("crash-%d", os.Getpid()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	crash, err := os.OpenFile(crashPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err = debug.SetCrashOutput(crash, debug.CrashOptions{}); err != nil {
		crash.Close()
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	crash.Close()
	if *host {
		return hostMain()
	}
	if *play == "" || (*backend != "mf" && *backend != "mpv") {
		fmt.Fprintln(os.Stderr, "usage: wallpaperd -play <video> [-backend mf|mpv] [-mpv <path>] [-status <json>]")
		return 2
	}
	path, err := filepath.Abs(*play)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	mygo.App.SetName("Wallpaper M1")
	if !mygo.App.RequestSingleInstanceLock() {
		fmt.Fprintln(os.Stderr, "wallpaperd: another instance is running")
		return 2
	}
	if err = platformInit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var once sync.Once
	var drained atomic.Bool
	var code atomic.Int32
	quitting := false      // main-thread owned
	var s *session.Session // read/written only on MyGo main thread
	quit := func(reason string) {
		once.Do(func() {
			quitting = true
			slog.Info("shutdown_begin", "reason", reason)
			current := s
			go func() { // App owns shutdown; main thread stays free to service teardown.
				if current != nil {
					current.Stop()
					select {
					case <-current.Done():
					case <-time.After(4500 * time.Millisecond):
						slog.Error("shutdown_timeout")
						os.Exit(1) // Job kills Windows descendants.
					}
				}
				slog.Info("shutdown_end")
				drained.Store(true)
				mygo.App.Quit()
			}()
		})
	}
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if !drained.Load() {
			e.PreventDefault()
			quit("app")
		}
	})
	quitRequested := make(chan struct{}, 1)
	mygo.App.WhenReady(func() {
		if quitting {
			return
		}
		s = session.New(func(ctx context.Context, r platform.Request) (platform.Loaded, error) {
			if st, err := os.Stat(r.Path); err != nil {
				return platform.Loaded{}, err
			} else if !st.Mode().IsRegular() {
				return platform.Loaded{}, fmt.Errorf("video must be a regular file")
			}
			return load(ctx, r, *mpv)
		}, session.Options{Observe: func(st session.State) error {
			var err error
			if *status != "" {
				err = session.WriteStatus(*status, st)
			}
			if err != nil || st.Phase == "failed" {
				code.Store(1)
				select {
				case quitRequested <- struct{}{}:
				default:
				}
			}
			return err
		}})
		// Starting a session can publish status; never block the native callback.
		current := s
		go current.Play(platform.Request{Path: path, Backend: backendName(*backend)})
	})
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	joined := make(chan struct{})
	go func() { // App owns signal/failure monitor; Run return closes done and joins it.
		defer close(joined)
		select {
		case <-signals:
			mygo.App.Quit()
		case <-quitRequested:
			mygo.App.Quit()
		case <-done:
		}
	}()
	err = mygo.App.Run()
	close(done)
	<-joined
	if err != nil {
		slog.Error("app_run", "err", err)
		return 1
	}
	return int(code.Load())
}
