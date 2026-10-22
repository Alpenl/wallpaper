package main

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, file, source, want string
	}{
		{"root render allowed", "internal/ui/view.go", "package ui\nimport _ \"wpapp/internal/render\"", ""},
		{"concrete renderer", "internal/ui/view.go", "package ui\nimport _ \"wpapp/internal/render/mpv\"", "forbidden direct import"},
		{"renderer", "internal/ui/view_windows.go", "package ui\nimport _ \"wpapp/internal/render/mf\"", "internal/ui/view_windows.go:2: forbidden direct import"},
		{"platform", "internal/ui/sub/view.go", "package sub\nimport _ \"wpapp/internal/platform/mac\"", "forbidden direct import"},
		{"desktop", "internal/ui/view.go", "package ui\nimport _ \"wpapp/internal/desktop/linux\"", "forbidden direct import"},
		{"config", "internal/config/sub/config.go", "package sub\nimport _ \"wpapp/internal/ui/pages\"", "forbidden direct import"},
		{"app", "internal/config/config.go", "package config\nimport _ \"wpapp/internal/app\"", "forbidden direct import"},
		{"test import allowed", "internal/ui/view_test.go", "package ui\nimport _ \"wpapp/internal/render\"", ""},
		{"clean imports", "internal/ui/view.go", "package ui\nimport _ \"wpapp/internal/desktop\"\nimport _ \"wpapp/internal/renderer\"", ""},
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
