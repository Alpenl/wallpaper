package wpk

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"golang.org/x/text/unicode/norm"
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
	seen := map[string]bool{}
	buf := make([]byte, 64<<10)
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
		if !norm.NFC.IsNormalString(name) {
			return unsafeErr("%q: non-NFC filename is not allowed", name)
		}
		if err := CheckPath(name); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSuffix(name, "/"))
		if seen[key] {
			return unsafeErr("duplicate name (case-insensitive) %q", name)
		}
		seen[key] = true
		if d.Type()&fs.ModeSymlink != 0 || d.Type()&^(fs.ModeDir) != 0 {
			return unsafeErr("links and special files are not allowed")
		}
		if d.IsDir() {
			return nil
		}
		if execExts[strings.ToLower(path.Ext(name))] {
			return unsafeErr("%q: executable files are not allowed", name)
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
		limit := entryLimit(name)
		if st.Size() > limit {
			return unsafeErr("%q: file too large", name)
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		var n int64
		var head [4]byte
		headLen := 0
		headChecked := false
		remaining := MaxTotal - total
		for {
			k, rerr := f.Read(buf)
			if k > 0 {
				if !headChecked {
					take := min(k, len(head)-headLen)
					copy(head[headLen:], buf[:take])
					headLen += take
					if headLen >= 2 && IsExecutableHeader(head[:headLen]) {
						return unsafeErr("%q: executable header", name)
					}
					if headLen == len(head) {
						headChecked = true
					}
				}
				n += int64(k)
				if n > limit || n > remaining {
					return unsafeErr("%q: size limit exceeded", name)
				}
				if _, werr := w.Write(buf[:k]); werr != nil {
					return werr
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
		if !headChecked && headLen > 0 && IsExecutableHeader(head[:headLen]) {
			return unsafeErr("%q: executable header", name)
		}
		total += n
		return nil
	})
	if e := zw.Close(); err == nil {
		err = e
	}
	return err
}
