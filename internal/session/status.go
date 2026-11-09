package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteStatus replaces a complete snapshot in the same directory. Readers see
// either the previous snapshot or this one, never a partially written document.
func WriteStatus(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".wallpaper-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
