package importer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CollectSources expands a plain directory into deterministic, supported import
// sources. Package directories are atomic; symlinks and hidden folders are not
// traversed. A malformed package is reported by Import rather than silently
// importing its contents as unrelated media.
func CollectSources(src string) ([]string, error) {
	return collectSources(src, 10000)
}

func collectSources(src string, limit int) ([]string, error) {
	if err := rejectUnsafeHostPath(src); err != nil {
		return nil, err
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{src}, nil
	}
	walkRoot := src
	if lst, lerr := os.Lstat(src); lerr == nil && lst.Mode()&os.ModeSymlink != 0 {
		resolved, rerr := filepath.EvalSymlinks(src)
		if rerr != nil {
			return nil, rerr
		}
		if err := rejectUnsafeHostPath(resolved); err != nil {
			return nil, err
		}
		walkRoot = resolved
	}
	if packageDir(walkRoot) {
		return []string{walkRoot}, nil
	}
	var paths []string
	appendSource := func(path string) error {
		if len(paths) >= limit {
			return fmt.Errorf("import: folder exceeds %d items; import in smaller batches", limit)
		}
		paths = append(paths, path)
		return nil
	}
	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != walkRoot && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != walkRoot && d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path == walkRoot {
				return nil
			}
			if packageDir(path) {
				if err := appendSource(path); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, supported := range SupportedExts {
			if ext == supported {
				if err := appendSource(path); err != nil {
					return err
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("import: folder contains no supported videos, images, or wallpaper packages")
	}
	return paths, nil
}

func packageDir(path string) bool {
	if _, ok := findFileFold(path, "LivelyInfo.json"); ok {
		return true
	}
	if _, ok := findFileFold(path, "project.json"); ok {
		return true
	}
	return existsExact(path, "manifest.json")
}
