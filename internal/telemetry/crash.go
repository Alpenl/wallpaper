package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"time"
)

// InstallCrashOutput must run before helper dispatch. The runtime duplicates f.
func InstallCrashOutput(dir, role string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	name := fmt.Sprintf("crash-%s-%d-%s.log", role, os.Getpid(), time.Now().UTC().Format("20060102T150405.000000000"))
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = debug.SetCrashOutput(f, debug.CrashOptions{})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	files, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	type crashFile struct {
		path  string
		mtime time.Time
	}
	var crashes []crashFile
	for _, p := range files {
		if filepath.Base(p) == name {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			_ = os.Remove(p) // A live Windows helper may still hold this file.
			continue
		}
		crashes = append(crashes, crashFile{p, info.ModTime()})
	}
	sort.Slice(crashes, func(i, j int) bool {
		if crashes[i].mtime.Equal(crashes[j].mtime) {
			return crashes[i].path < crashes[j].path
		}
		return crashes[i].mtime.After(crashes[j].mtime)
	})
	for _, f := range crashes[min(5, len(crashes)):] {
		_ = os.Remove(f.path) // Pruning is best effort.
	}
	return nil
}
