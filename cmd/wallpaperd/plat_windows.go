//go:build windows

package main

import (
	"log/slog"
	"os/exec"
	"path/filepath"
	"syscall"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/desktop/win"
	"wpapp/internal/platform/win32"
	"wpapp/internal/render"
	"wpapp/internal/render/mf"
)

// platInit：Job Object（KILL_ON_JOB_CLOSE，主进程崩溃时 mpv/mfhost 一起结束）+ RegisterApplicationRestart。
func platInit(log *slog.Logger) {
	if err := win32.SetupJob(); err != nil {
		log.Warn("job object", "err", err)
	}
	if err := win32.RegisterRestart(""); err != nil {
		log.Warn("register restart", "err", err)
	}
}

func newHost(f *flags, log *slog.Logger) desktop.Host {
	return win.New(win.Options{Log: log, Standalone: f.standalone, FakeMonitors: f.fake})
}

func platformRenderer(e *env, cfg config.Config, d desktop.DisplayID) render.Renderer {
	if cfg.Render.WindowsBackend == "mf" {
		return mf.New(e.exe, filepath.Join(e.paths.Logs, "mfhost-"+safeName(string(d))+".log"))
	}
	return nil
}

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func headlessPre() error               { return nil }
func uiMainInit()                      {}
func macSnapshot(string, string) error { return render.ErrUnsupported }
