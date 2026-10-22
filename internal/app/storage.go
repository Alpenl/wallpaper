package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StorageView reports on-disk usage, not synthetic library estimates.
type StorageView struct {
	LibraryBytes int64
	CacheBytes   int64
	Scanning     bool
	Error        string
}

func diskUsage(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			st, e := d.Info()
			if e != nil {
				return e
			}
			if st.Mode().IsRegular() {
				size += st.Size()
			}
		}
		return nil
	})
	return size, err
}

// RefreshStorage recalculates library/cache usage off the event loop.
func (a *App) RefreshStorage() { a.Do(a.refreshStorage) }
func (a *App) refreshStorage() {
	if a.storage.Scanning {
		a.storageDirty = true
		return
	}
	a.storage.Scanning = true
	paths := a.d.Store.Paths
	go func() {
		lib, e1 := diskUsage(paths.Library)
		cache, e2 := diskUsage(paths.Cache)
		msg := ""
		if e1 != nil {
			msg = e1.Error()
		}
		if e2 != nil {
			if msg != "" {
				msg += "; "
			}
			msg += e2.Error()
		}
		a.Do(func() {
			a.storage = StorageView{LibraryBytes: lib, CacheBytes: cache, Error: msg}
			if a.storageDirty {
				a.storageDirty = false
				a.refreshStorage()
			}
		})
	}()
}

// ClearCache removes only orphaned cache directories. Referenced snapshots remain
// available for paused wallpapers, safe mode and system-wallpaper fallback.
func (a *App) ClearCache() {
	a.Do(func() {
		if a.cleaning || a.importing {
			a.lastErr = "请等待导入或清理完成后再清理缓存"
			return
		}
		if err := a.cleanRenditions(); err != nil {
			a.lastErr = err.Error()
			return
		}
		a.cleaning = true
		keep := map[string]bool{}
		for _, it := range a.d.Store.Library.Items {
			keep[it.ID] = true
			if rel, err := filepath.Rel(a.d.Store.Paths.Cache, it.Snapshot); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				keep[strings.Split(rel, string(filepath.Separator))[0]] = true
			}
		}
		root := a.d.Store.Paths.Cache
		go func() {
			entries, err := os.ReadDir(root)
			removed := 0
			if err == nil {
				for _, e := range entries {
					if keep[e.Name()] {
						continue
					}
					if er := os.RemoveAll(filepath.Join(root, e.Name())); er != nil {
						err = er
						break
					}
					removed++
				}
			}
			a.Do(func() {
				a.cleaning = false
				defer a.advanceJobs()
				if err != nil {
					a.lastErr = "清理缓存失败：" + err.Error()
				} else {
					a.lastImp = fmt.Sprintf("已清理 %d 项无引用缓存；保留正在使用的壁纸预览", removed)
				}
				a.refreshStorage()
			})
		}()
	})
}

// managedChild prevents a malformed library record from deleting an external
// directory or the library/cache root itself.
func managedChild(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	r, e1 := filepath.Abs(root)
	p, e2 := filepath.Abs(path)
	if e1 != nil || e2 != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != "." && rel != ".." && filepath.Dir(p) == r && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// tombstone atomically detaches an item path; deletion may then run in the
// background without ever targeting a subsequently imported item of the same ID.
func tombstone(path string) (string, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	target, err := os.MkdirTemp(filepath.Dir(path), ".removed-")
	if err != nil {
		return "", err
	}
	if err = os.Remove(target); err != nil {
		return "", err
	}
	if err = os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}
