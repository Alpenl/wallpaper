//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/desktop/mac"
	"wpapp/internal/platform/macos"
	"wpapp/internal/render"
	"wpapp/internal/render/avf"
)

func platInit(*slog.Logger) {}

func newHost(_ *flags, log *slog.Logger) desktop.Host { return mac.New(mac.Options{Log: log}) }

func platformRenderer(*env, config.Config, desktop.DisplayID) render.Renderer { return avf.New() }

func hideConsole(*exec.Cmd) {}

// mac 的 AppKit 调用必须在主线程：只有 UI 模式（MyGo 跑主线程事件循环）可用。
func headlessPre() error {
	return errors.New("headless mode is not supported on macOS (AppKit needs the MyGo main loop)")
}

func uiMainInit() { macos.Main = mygo.RunOnMain }

// macSnapshot：qlmanage 生成缩略图（未在真机验证）。
func macSnapshot(src, dst string) error {
	dir, err := os.MkdirTemp(filepath.Dir(dst), ".ql-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/qlmanage", "-t", "-s", "1920", "-o", dir, src)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("qlmanage timed out: %w", ctx.Err())
		}
		return errors.New("qlmanage: " + string(out))
	}
	return os.Rename(filepath.Join(dir, filepath.Base(src)+".png"), dst)
}
