package web

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"wpapp/internal/wpk"
)

func validateLoad(x loadRequest) (*wpk.Manifest, error) {
	if x.Item.FPSCap < 0 || x.Item.FPSCap > 240 {
		return nil, fmt.Errorf("web: invalid initial FPS cap")
	}
	if x.Item.Kind != "web" || !filepath.IsAbs(x.Item.Path) || x.Bounds.Dx() <= 0 || x.Bounds.Dy() <= 0 || x.Bounds.Dx() > 16384 || x.Bounds.Dy() > 16384 {
		return nil, fmt.Errorf("web: invalid load")
	}
	if !filepath.IsAbs(x.Item.PackageDir) {
		return nil, fmt.Errorf("web: absolute package directory required")
	}
	m, e := wpk.LoadDir(x.Item.PackageDir)
	if e != nil {
		return nil, e
	}
	if m.Type != "web" || m.ID != x.Item.ID || filepath.Clean(filepath.Join(x.Item.PackageDir, filepath.FromSlash(m.Entry))) != filepath.Clean(x.Item.Path) {
		return nil, fmt.Errorf("web: package identity mismatch")
	}
	declared := false
	for _, p := range m.Web.Permissions {
		if p == "audio-reactive" {
			return nil, fmt.Errorf("web: audio-reactive unsupported")
		}
		if p == "network" {
			declared = true
		}
	}
	if x.Item.NetworkGranted && !declared {
		return nil, fmt.Errorf("web: undeclared network permission")
	}
	if _, e = PropertyScript(m.Properties, x.Item.Properties); e != nil {
		return nil, e
	}
	return m, nil
}
func contentDir(x loadRequest, m *wpk.Manifest) string {
	return filepath.Join(x.Item.PackageDir, "content")
}
func entryURL(x loadRequest, m *wpk.Manifest, platform string) string {
	u, _ := url.Parse(Origin(m.ID, platform))
	u.Path = "/" + strings.TrimPrefix(m.Entry, "content/")
	return u.String()
}

// No TCP listener or page-to-host bridge. Closing inherited stdin terminates the
// host, including when the supervisor dies. Native loops service IPC on their
// owning event thread.
func HostMain() int {
	if e := runHost(pipeIO{os.Stdin, os.Stdout}); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	return 0
}

func checkResourcePath(name string) error { return wpk.CheckPath(name) }
