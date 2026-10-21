//go:build linux

package main

import (
	"log/slog"
	"os/exec"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/desktop/linux"
	"wpapp/internal/render"
)

func platInit(*slog.Logger) {}

func newHost(_ *flags, log *slog.Logger) desktop.Host { return linux.New(linux.Options{Log: log}) }

func platformRenderer(*env, config.Config, desktop.DisplayID) render.Renderer { return nil }

func hideConsole(*exec.Cmd)            {}
func headlessPre() error               { return nil }
func uiMainInit()                      {}
func macSnapshot(string, string) error { return render.ErrUnsupported }
