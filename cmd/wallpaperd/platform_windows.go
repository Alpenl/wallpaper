//go:build windows

package main

import (
	"context"

	"github.com/egoist/mygo"

	"wpapp/internal/platform"
	"wpapp/internal/platform/win"
	"wpapp/internal/player/mf"
	"wpapp/internal/player/mpv"
)

func platformInit() error         { win.Main = mygo.RunOnMain; return win.SetupJob() }
func backendName(s string) string { return s }
func hostMain() int               { return mf.HostMain() }
func load(ctx context.Context, r platform.Request, mpvPath string) (platform.Loaded, error) {
	var resources platform.Loaded
	surface, err := win.New(ctx)
	if surface != nil {
		resources.Surface = surface
	}
	if err != nil {
		return resources, err
	}
	if r.Backend == "mpv" {
		p, err := mpv.Load(ctx, r.Path, mpvPath, surface)
		if p != nil {
			resources.Player = p
		}
		return resources, err
	}
	p, err := mf.Load(ctx, r.Path, surface)
	if p != nil {
		resources.Player = p
	}
	return resources, err
}
