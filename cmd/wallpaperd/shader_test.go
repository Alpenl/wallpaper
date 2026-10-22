package main

import (
	"testing"
	"wpapp/internal/config"
	"wpapp/internal/render/shader"
	webwall "wpapp/internal/render/web"
)

func TestWallpaperEngineRouting(t *testing.T) {
	e := &env{exe: "must-not-be-started"}
	cfg := config.Defaults()
	if _, ok := e.newRenderer("shader", cfg, "test").(*shader.Renderer); !ok {
		t.Fatal("shader routed to non-native renderer")
	}
	if _, ok := e.newRenderer("web", cfg, "test").(*webwall.Renderer); !ok {
		t.Fatal("web route missing")
	}
	if _, ok := e.newRenderer("video", cfg, "test").(*webwall.Renderer); ok {
		t.Fatal("video routed to web host")
	}
	if _, ok := e.newRenderer("image", cfg, "test").(*webwall.Renderer); ok {
		t.Fatal("image routed to web host")
	}
}
