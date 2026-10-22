package wpk

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ZipFolder stages directories through exactly the same validator as archives.
// os.Root bounds every open even if a source is changed during traversal.
func ZipFolder(dir, dst string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	count := 0
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if name == "." {
			return nil
		}
		count++
		if count > MaxEntries {
			return unsafeErr("too many entries")
		}
		if err := CheckPath(name); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || d.Type()&^(fs.ModeDir) != 0 {
			return unsafeErr("links and special files are not allowed")
		}
		if d.IsDir() {
			return nil
		}
		if execExts[filepath.Ext(name)] {
			return unsafeErr("executable files are not allowed")
		}
		f, e := root.Open(name)
		if e != nil {
			return e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", name)
		}
		limit := int64(MaxNonVideo)
		if IsVideoExt(name) {
			limit = MaxTotal
		}
		if st.Size() > limit {
			return unsafeErr("file too large")
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		n, e := io.Copy(w, io.LimitReader(f, min(limit, MaxTotal-total)+1))
		total += n
		if n > limit || total > MaxTotal {
			return unsafeErr("size limit exceeded")
		}
		return e
	})
	if e := zw.Close(); err == nil {
		err = e
	}
	return err
}
