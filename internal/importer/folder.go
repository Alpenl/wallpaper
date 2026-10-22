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
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || packageDir(src) {
		return []string{src}, nil
	}
	var paths []string
	appendSource := func(path string) error {
		if len(paths) >= limit {
			return fmt.Errorf("import: 文件夹超过 %d 项，请分批导入", limit)
		}
		paths = append(paths, path)
		return nil
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != src && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path == src {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
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
		return nil, fmt.Errorf("import: 文件夹中没有支持的视频、图片或壁纸包")
	}
	return paths, nil
}

func packageDir(path string) bool {
	return exists(filepath.Join(path, "LivelyInfo.json")) || exists(filepath.Join(path, "project.json")) || exists(filepath.Join(path, "manifest.json"))
}
