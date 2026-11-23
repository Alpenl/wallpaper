package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/ui"
)

func TestInstallDirForPlatforms(t *testing.T) {
	macBundleExe := filepath.Join("/Applications", "Wallpaper.app", "Contents", "MacOS", "wallpaperd")
	wantContents := filepath.Join("/Applications", "Wallpaper.app", "Contents")
	if got := installDirFor("darwin", macBundleExe); got != wantContents {
		t.Fatalf("darwin bundle installDir = %q, want %q", got, wantContents)
	}

	// Non-bundle directory coincidentally named MacOS must not climb above itself.
	macStandalone := filepath.Join("/tmp", "MacOS", "wallpaperd")
	wantStandalone := filepath.Join("/tmp", "MacOS")
	if got := installDirFor("darwin", macStandalone); got != wantStandalone {
		t.Fatalf("darwin non-bundle installDir = %q, want %q", got, wantStandalone)
	}

	winExe := filepath.Join("C:", "Program Files", "Wallpaper", "wallpaperd.exe")
	wantWin := filepath.Join("C:", "Program Files", "Wallpaper")
	if got := installDirFor("windows", winExe); got != wantWin {
		t.Fatalf("windows installDir = %q, want %q", got, wantWin)
	}
}

func TestUpdateHooksEnglishErrors(t *testing.T) {
	origKey := updatePubKey
	t.Cleanup(func() { updatePubKey = origKey })

	e := &env{exe: "wallpaperd", initialConfig: config.Defaults()}
	var hooks ui.Hooks
	e.updateHooks(&hooks, func() {})

	updatePubKey = ""
	if _, err := hooks.CheckUpdate(); err == nil || err.Error() != "update: this build has no update public key" {
		t.Fatalf("expected English no-key error, got %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	updatePubKey = base64.StdEncoding.EncodeToString(pub)
	if _, err := hooks.CheckUpdate(); err == nil || err.Error() != "update: no feed configured" {
		t.Fatalf("expected English no-feed error, got %v", err)
	}

	if err := hooks.ApplyUpdate(nil); err == nil || err.Error() != "update: check for updates first" {
		t.Fatalf("expected English check-first error, got %v", err)
	}
}

func TestConfirmUpdateAfterGating(t *testing.T) {
	t.Run("healthy-calls-markGood", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		appDone := make(chan struct{})
		called := make(chan struct{}, 1)
		go confirmUpdateAfter(ctx, appDone, 5*time.Millisecond, func() bool { return false }, func() error {
			called <- struct{}{}
			return nil
		}, nil)
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("expected markGood to be called")
		}
	})

	t.Run("safe-mode-skips-markGood", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		appDone := make(chan struct{})
		var calls atomic.Int32
		done := make(chan struct{})
		go func() {
			defer close(done)
			confirmUpdateAfter(ctx, appDone, 5*time.Millisecond, func() bool { return true }, func() error {
				calls.Add(1)
				return nil
			}, nil)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("confirmUpdateAfter did not exit")
		}
		if calls.Load() != 0 {
			t.Fatal("markGood must not be called in safe mode")
		}
	})

	t.Run("app-exit-cancels-markGood", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		appDone := make(chan struct{})
		var calls atomic.Int32
		done := make(chan struct{})
		go func() {
			defer close(done)
			confirmUpdateAfter(ctx, appDone, 5*time.Second, func() bool { return false }, func() error {
				calls.Add(1)
				return nil
			}, nil)
		}()
		close(appDone)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("confirmUpdateAfter did not exit on appDone")
		}
		if calls.Load() != 0 {
			t.Fatal("markGood must not be called after app exit")
		}
	})
}
