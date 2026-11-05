package importer

import (
	"fmt"
	"os"

	"wpapp/internal/wpk"
)

// The product supports image and video wallpapers only. Packages declaring a
// removed engine are rejected before extraction replaces an existing item.
func supportedManifest(m *wpk.Manifest) error {
	if m.Type != "video" && m.Type != "image" {
		return fmt.Errorf("%w: %s", ErrUnsupported, m.Type)
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
