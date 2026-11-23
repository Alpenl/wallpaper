package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	userDataPrefix   = "wpk-webview2-"
	userDataLockFile = "wpk.lock"
)

type userDataDir struct {
	path string
	lock *os.File
}

func userDataBase(cacheDir string) string {
	if strings.TrimSpace(cacheDir) != "" {
		return filepath.Join(cacheDir, "webview")
	}
	return os.TempDir()
}

// prepareUserDataDir creates a fresh WebView2 user-data directory under
// <cacheDir>/webview/ (falling back to os.TempDir()), acquires a PID lock file,
// and sweeps stale inactive directories from prior crashed runs.
func prepareUserDataDir(cacheDir string) (*userDataDir, error) {
	base := userDataBase(cacheDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(base, userDataPrefix+"*")
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, userDataLockFile)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	_ = f.Sync()
	sweepStaleUserDataDirs(base, filepath.Base(dir))
	return &userDataDir{path: dir, lock: f}, nil
}

func sweepStaleUserDataDirs(base, currentName string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, userDataPrefix) || name == currentName {
			continue
		}
		dir := filepath.Join(base, name)
		if isUserDataDirActive(dir) {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

func isUserDataDirActive(dir string) bool {
	lockPath := filepath.Join(dir, userDataLockFile)
	b, err := os.ReadFile(lockPath)
	if err != nil {
		// No lock file: treat old directories (>10s) as stale leftovers from pre-lock versions.
		if info, statErr := os.Stat(dir); statErr == nil && time.Since(info.ModTime()) < 10*time.Second {
			return true
		}
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	if pid == os.Getpid() {
		return true
	}
	if isPIDAlive(pid) {
		return true
	}
	// On Windows, also verify the lock file is not held open by another process.
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return true
	}
	return false
}

// Close releases the lock file and removes the directory with bounded retries
// so WebView2 browser subprocesses have time to exit and release file handles.
func (u *userDataDir) Close() {
	if u == nil {
		return
	}
	if u.lock != nil {
		_ = u.lock.Close()
		u.lock = nil
	}
	if u.path == "" {
		return
	}
	dir := u.path
	u.path = ""
	for attempt := 0; attempt < 10; attempt++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return
		}
		// sleep: bounded retry waiting for msedgewebview2 subprocesses to release file locks on shutdown.
		time.Sleep(50 * time.Millisecond)
	}
}
