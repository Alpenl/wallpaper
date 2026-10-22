package main

import (
	"testing"
	"wpapp/internal/config"
)

func TestInitialConfigSnapshotIsDetached(t *testing.T) {
	cfg := config.Defaults()
	cfg.Pause.AppRules = []config.AppRule{{Executable: "game.exe", Action: "pause"}}
	e := &env{initialConfig: cfg}
	snapshot := e.configSnapshot()
	snapshot.Pause.AppRules[0].Action = "ignore"
	if e.initialConfig.Pause.AppRules[0].Action != "pause" {
		t.Fatal("snapshot aliases immutable startup config")
	}
}
