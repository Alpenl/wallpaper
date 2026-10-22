package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wpapp/internal/wpk"
)

func supportedManifest(m *wpk.Manifest) error {
	if m.Type != "video" && m.Type != "image" && m.Type != "web" {
		return fmt.Errorf("%w: %s", ErrUnsupported, m.Type)
	}
	if m.Web != nil {
		for _, p := range m.Web.Permissions {
			if p == "audio-reactive" {
				return fmt.Errorf("%w: audio-reactive web wallpapers", ErrUnsupported)
			}
		}
	}
	return nil
}
func importFolder(dir string, o Options) (*Result, error) {
	f, err := os.CreateTemp("", "wallpaper-folder-*.wpk")
	if err != nil {
		return nil, err
	}
	p := f.Name()
	f.Close()
	defer os.Remove(p)
	if err = wpk.ZipFolder(dir, p); err != nil {
		return nil, err
	}
	return importWPK(p, o)
}

// Foreign local pages retain their full relative asset tree. First run the
// source through SafeUnzip so executables, links, duplicate paths and size limits
// are rejected before creating a native package.
func importWeb(dir, entry, title, origin string, o Options) (*Result, error) {
	if _, err := within(dir, entry); err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(entry)); ext != ".html" && ext != ".htm" {
		return nil, fmt.Errorf("%w: local HTML entry required", ErrUnsupported)
	}
	tmp, err := os.MkdirTemp("", "wallpaper-web-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "source.zip")
	if err = wpk.ZipFolder(dir, archive); err != nil {
		return nil, err
	}
	pkg := filepath.Join(tmp, "package")
	content := filepath.Join(pkg, "content")
	if err = wpk.SafeUnzip(archive, content); err != nil {
		return nil, err
	}
	zero := 0
	m := &wpk.Manifest{Format: "wpk", Schema: &zero, ID: NewID(), Version: 1, Type: "web", Entry: "content/" + entry, Title: wpk.I18n{"en": title}, Web: &wpk.Web{FPSCap: 30}, Origin: &wpk.Origin{Kind: origin}}
	defs, warnings, err := foreignProperties(dir, origin)
	if err != nil {
		return nil, err
	}
	m.Properties = defs
	if title == "" {
		m.Title["en"] = filepath.Base(dir)
	}
	if err = wpk.WriteManifest(pkg, m); err != nil {
		return nil, err
	}
	// A neutral PNG is used when a foreign package has no native preview. Never
	// launch the page (or a media helper) in the importer to obtain a thumbnail.
	if err = os.WriteFile(filepath.Join(pkg, "preview.png"), webPreview, 0644); err != nil {
		return nil, err
	}
	res, err := importFolder(pkg, o)
	if err == nil {
		res.Warnings = append(res.Warnings, warnings...)
	}
	return res, err
}

var webPreview = []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137, 0, 0, 0, 13, 73, 68, 65, 84, 8, 215, 99, 248, 207, 192, 240, 31, 0, 5, 0, 1, 255, 137, 153, 61, 29, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130}
