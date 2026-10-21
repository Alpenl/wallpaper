// Package config 实现 16 章：目录布局、config/library/assignments/state 四个 JSON 文件、原子写、.bak 回退、迁移和写节流。
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// AppName 用于目录名。
const AppName = "WallpaperApp"

// BundleID 用于 macOS 目录名。
const BundleID = "io.github.wpd.wallpaper"

// Paths 是 16.1 的目录布局。
type Paths struct {
	Config  string // config.json / library.json / assignments.json / state.json
	Library string // library/<id>/
	Cache   string // cache/<id>/（截图、转码产物）
	Logs    string
}

// DefaultPaths 按平台给出目录；设置 WPAPP_HOME 时全部放在该目录下（测试与便携模式）。
func DefaultPaths() (Paths, error) {
	if h := os.Getenv("WPAPP_HOME"); h != "" {
		return under(h), nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows": // Config=%APPDATA%，Cache=%LOCALAPPDATA%
		local := filepath.Join(cache, AppName)
		return Paths{Config: filepath.Join(cfg, AppName), Library: filepath.Join(local, "library"),
			Cache: filepath.Join(local, "cache"), Logs: filepath.Join(local, "logs")}, nil
	case "darwin":
		base := filepath.Join(cfg, BundleID)
		return Paths{Config: base, Library: filepath.Join(base, "library"),
			Cache: filepath.Join(cache, BundleID), Logs: filepath.Join(home, "Library", "Logs", BundleID)}, nil
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			state = filepath.Join(home, ".local", "state")
		}
		return Paths{Config: filepath.Join(cfg, "wallpaper-app"), Library: filepath.Join(data, "wallpaper-app", "library"),
			Cache: filepath.Join(cache, "wallpaper-app"), Logs: filepath.Join(state, "wallpaper-app", "logs")}, nil
	}
}

func under(h string) Paths {
	return Paths{Config: filepath.Join(h, "config"), Library: filepath.Join(h, "library"),
		Cache: filepath.Join(h, "cache"), Logs: filepath.Join(h, "logs")}
}

// Ensure 创建所有目录。
func (p Paths) Ensure() error {
	for _, d := range []string{p.Config, p.Library, p.Cache, p.Logs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
