package main

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, file, source, want string
	}{
		{"ports allowed", "internal/session/session.go", `package session; import _ "wpapp/internal/platform"`, ""},
		{"mac", "internal/session/session.go", `package session; import _ "wpapp/internal/platform/mac"`, "forbidden direct import"},
		{"win", "internal/session/x_windows.go", `package session; import _ "wpapp/internal/platform/win"`, "forbidden direct import"},
		{"player", "internal/session/sub/x.go", `package sub; import _ "wpapp/internal/player/mf"`, "forbidden direct import"},
		{"test boundary", "internal/session/session_test.go", `package session; import _ "wpapp/internal/player/avf"`, "forbidden direct import"},
		{"composition root", "cmd/wallpaperd/main.go", `package main; import _ "wpapp/internal/player/mf"`, ""},
		{"sleep", "cmd/tool/main_test.go", "package main\nimport \"time\"\nfunc f() { time.Sleep(1) }", "cmd/tool/main_test.go:3: time.Sleep requires"},
		{"alias", "internal/ui/view_test.go", "package ui\nimport clock \"time\"\nfunc f() { clock.Sleep(1) }", "time.Sleep requires"},
		{"dot", "internal/ui/view_test.go", "package ui\nimport . \"time\"\nfunc f() { Sleep(1) }", "time.Sleep requires"},
		{"above", "internal/ui/view_test.go", "package ui\nimport \"time\"\nfunc f() {\n// sleep: observation window\ntime.Sleep(1)\n}", ""},
		{"inline", "internal/ui/view_test.go", "package ui\nimport \"time\"\nfunc f() { time.Sleep(1) /* sleep: observation window */ }", ""},
		{"too far", "internal/ui/view_test.go", "package ui\nimport \"time\"\nfunc f() {\n// sleep: observation window\n\ntime.Sleep(1)\n}", "time.Sleep requires"},
		{"string is not comment", "internal/ui/view_test.go", "package ui\nimport \"time\"\nfunc f() { _ = \"sleep:\"; time.Sleep(1) }", "time.Sleep requires"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := check(tc.file, tc.source)
			if tc.want == "" && len(got) != 0 || tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)) {
				t.Fatalf("check() = %v; want %q", got, tc.want)
			}
		})
	}
}
