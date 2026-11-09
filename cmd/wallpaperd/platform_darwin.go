//go:build darwin

package main

import (
	"context"

	"github.com/egoist/mygo"

	"wpapp/internal/platform"
	macos "wpapp/internal/platform/mac"
	"wpapp/internal/player/avf"
)

func platformInit() error {
	macos.Main = mygo.RunOnMain
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	return nil
}
func backendName(string) string { return "avf" }
func hostMain() int             { return 2 }
func load(ctx context.Context, r platform.Request, _ string) (platform.Loaded, error) {
	var resources platform.Loaded
	surface, err := macos.New(ctx)
	if surface != nil {
		resources.Surface = surface
	}
	if err != nil {
		return resources, err
	}
	player, err := avf.Load(ctx, r.Path, surface)
	if player != nil {
		resources.Player = player
	}
	return resources, err
}
