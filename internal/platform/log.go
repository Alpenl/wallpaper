package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// BoundedLog serializes writers and caps each launch at 2 MiB. It has no worker;
// Close therefore joins writes by taking the same mutex. One previous file is kept.
type BoundedLog struct {
	mu               sync.Mutex
	file             *os.File
	written, dropped int64
	first            error
	closed           bool
}

func LogPath(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "wallpaper-m1", "logs")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".log"), nil
}
func RotateLog(path string) error {
	if err := os.Remove(path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func NewBoundedLog(name string) (*BoundedLog, error) {
	path, err := LogPath(name)
	if err != nil {
		return nil, err
	}
	return OpenBoundedLog(path)
}
func OpenBoundedLog(path string) (*BoundedLog, error) {
	if err := RotateLog(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &BoundedLog{file: f}, nil
}
func (l *BoundedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	size := len(p)
	n := min(size, int((2<<20)-l.written))
	if l.first != nil {
		n = 0
	}
	written, err := l.file.Write(p[:n])
	l.written += int64(written)
	if err == nil && written != n {
		err = io.ErrShortWrite
	}
	if err != nil && l.first == nil {
		l.first = err
		fmt.Fprintf(os.Stderr, "child log write failed: %v\n", err)
	}
	l.dropped += int64(size - written)
	// Continue draining child output after the cap or an I/O failure.
	return size, nil
}
func (l *BoundedLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.first
	}
	l.closed = true
	l.first = errors.Join(l.first, l.file.Close())
	if l.first != nil || l.dropped > 0 {
		fmt.Fprintf(os.Stderr, "child log closed: error=%v dropped=%d\n", l.first, l.dropped)
	}
	return l.first
}
