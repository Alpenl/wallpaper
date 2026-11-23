package wpk

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// 10.4 规模限制。
const (
	MaxEntries    = 10000
	MaxTotal      = 4 << 30
	MaxNonVideo   = 256 << 20
	MaxRatio      = 200
	MaxPreview    = 2 << 20
	MaxThumb      = 256 << 10
	ratioFloorRaw = 1 << 20 // 小于 1 MiB 的条目不做压缩比判断（极小文本的压缩比天然可以很高）
)

// ErrUnsafe 是所有安全检查失败的公共前缀。
var ErrUnsafe = errors.New("wpk: unsafe package")

func unsafeErr(f string, a ...any) error {
	return fmt.Errorf("%w: "+f, append([]any{ErrUnsafe}, a...)...)
}

var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// CheckPath 实现 10.4 的路径规则：拒绝绝对路径、.. 段、反斜杠、盘符、NUL、Windows 保留名、末尾点或空格、空段。
func CheckPath(p string) error {
	switch {
	case p == "":
		return errors.New("empty path")
	case strings.ContainsRune(p, 0):
		return errors.New("NUL in path")
	case strings.Contains(p, `\`):
		return errors.New("backslash in path")
	case strings.HasPrefix(p, "/"):
		return errors.New("absolute path")
	case len(p) >= 2 && p[1] == ':':
		return errors.New("drive letter")
	case strings.ContainsAny(p, `:*?"<>|`):
		return errors.New("forbidden character")
	}
	segs := strings.Split(strings.TrimSuffix(p, "/"), "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return fmt.Errorf("bad segment %q", s)
		}
		if strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
			return fmt.Errorf("segment ends with dot or space: %q", s)
		}
		stem := strings.ToUpper(s)
		if i := strings.IndexByte(stem, '.'); i >= 0 {
			stem = stem[:i]
		}
		if reserved[stem] {
			return fmt.Errorf("reserved name %q", s)
		}
	}
	return nil
}

var execExts = map[string]bool{".exe": true, ".dll": true, ".scr": true, ".msi": true, ".bat": true, ".cmd": true, ".ps1": true,
	".vbs": true, ".lnk": true, ".app": true, ".dylib": true, ".so": true, ".command": true, ".sh": true, ".pkg": true,
	".com": true, ".cpl": true, ".jar": true}

// IsExecutableHeader 判断 MZ / ELF / Mach-O（含 fat）魔数。
func IsExecutableHeader(b []byte) bool {
	if len(b) >= 2 && b[0] == 'M' && b[1] == 'Z' {
		return true
	}
	if len(b) < 4 {
		return false
	}
	switch {
	case bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'}),
		bytes.Equal(b[:4], []byte{0xfe, 0xed, 0xfa, 0xce}), bytes.Equal(b[:4], []byte{0xfe, 0xed, 0xfa, 0xcf}),
		bytes.Equal(b[:4], []byte{0xce, 0xfa, 0xed, 0xfe}), bytes.Equal(b[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}),
		bytes.Equal(b[:4], []byte{0xca, 0xfe, 0xba, 0xbe}):
		return true
	}
	return false
}

// Result 是解压结果。
type Result struct {
	Manifest *Manifest
	Dir      string
	Files    int
	Bytes    int64
}

// Extract 安全解压 .wpk 到 libraryDir/<id>/：先解到临时目录，全部校验通过后再替换。
func Extract(zipPath, libraryDir string) (*Result, error) {
	return ExtractChecked(zipPath, libraryDir, nil)
}

// renameDir is a package-internal hook for atomic-swap failure-injection tests.
var renameDir = os.Rename

// ExtractChecked calls beforePublish only after all files have passed validation.
func ExtractChecked(zipPath, libraryDir string, beforePublish func(*Manifest) error) (*Result, error) {
	if beforePublish == nil {
		return ExtractCheckedDir(zipPath, libraryDir, nil)
	}
	return ExtractCheckedDir(zipPath, libraryDir, func(m *Manifest, _ string) error {
		return beforePublish(m)
	})
}

// ExtractCheckedDir calls beforePublish with the parsed manifest and the staged
// temporary directory after all files are unpacked and validated, before publishing
// into libraryDir/<id>.
func ExtractCheckedDir(zipPath, libraryDir string, beforePublish func(*Manifest, string) error) (*Result, error) {
	st, err := os.Stat(zipPath)
	if err != nil {
		return nil, fmt.Errorf("wpk: stat: %w", err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("wpk: open: %w", err)
	}
	defer zr.Close()
	return extractReader(&zr.Reader, st.Size(), libraryDir, beforePublish)
}

// precheck validates entry count, NFC normalization, path rules, case-insensitive
// uniqueness, symlinks/special files, and executable extensions.
func precheck(zr *zip.Reader) (map[string]*zip.File, error) {
	if len(zr.File) > MaxEntries {
		return nil, unsafeErr("too many entries (%d)", len(zr.File))
	}
	seen := map[string]*zip.File{}
	for _, f := range zr.File {
		if !norm.NFC.IsNormalString(f.Name) {
			return nil, unsafeErr("%q: non-NFC filename is not allowed", f.Name)
		}
		if err := CheckPath(f.Name); err != nil {
			return nil, unsafeErr("%q: %v", f.Name, err)
		}
		key := strings.ToLower(strings.TrimSuffix(f.Name, "/"))
		if seen[key] != nil {
			return nil, unsafeErr("duplicate name (case-insensitive) %q", f.Name)
		}
		seen[key] = f
		mode := f.Mode()
		if mode&fs.ModeSymlink != 0 || mode&(fs.ModeType&^fs.ModeDir) != 0 {
			return nil, unsafeErr("%q: links and special files are not allowed", f.Name)
		}
		if !f.FileInfo().IsDir() && execExts[strings.ToLower(path.Ext(f.Name))] {
			return nil, unsafeErr("%q: executable files are not allowed", f.Name)
		}
	}
	return seen, nil
}

func entryLimit(name string) int64 {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "/") {
		if lower == "preview.webp" || lower == "preview.png" {
			return MaxPreview
		}
		if lower == "thumb.webp" || lower == "thumb.png" {
			return MaxThumb
		}
		if strings.HasPrefix(lower, "preview.") && !IsVideoExt(name) {
			return MaxPreview
		}
		if strings.HasPrefix(lower, "thumb.") && !IsVideoExt(name) {
			return MaxThumb
		}
	}
	if IsVideoExt(name) {
		return MaxTotal
	}
	return MaxNonVideo
}

// unpack writes all archive entries into root with streaming size, magic, and ratio checks.
func unpack(zr *zip.Reader, root *os.Root, archiveSize int64) (files int, total int64, err error) {
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(strings.TrimSuffix(name, "/"), 0o755); err != nil {
				return 0, 0, err
			}
			continue
		}
		if d := path.Dir(name); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return 0, 0, err
			}
		}
		limit := entryLimit(name)
		n, err := copyEntry(root, name, f, limit, MaxTotal-total, archiveSize)
		if err != nil {
			return 0, 0, err
		}
		total += n
		files++
	}
	return files, total, nil
}

// SafeUnzip extracts any zip archive (e.g. a Lively package) into dst under all WPK safety rules.
func SafeUnzip(zipPath, dst string) error {
	st, err := os.Stat(zipPath)
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	if _, err := precheck(&zr.Reader); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	_, _, err = unpack(&zr.Reader, root, st.Size())
	return err
}

func checkWPKRootNames(zr *zip.Reader) error {
	for _, f := range zr.File {
		trimmed := strings.TrimSuffix(f.Name, "/")
		if strings.Contains(trimmed, "/") {
			continue
		}
		lower := strings.ToLower(f.Name)
		if (lower == "manifest.json" || strings.HasPrefix(lower, "preview.") || strings.HasPrefix(lower, "thumb.")) && f.Name != lower {
			return unsafeErr("%q: root metadata/preview filenames must be lowercase", f.Name)
		}
	}
	return nil
}

func extractReader(zr *zip.Reader, archiveSize int64, libraryDir string, beforePublish func(*Manifest, string) error) (*Result, error) {
	seen, err := precheck(zr)
	if err != nil {
		return nil, err
	}
	if err := checkWPKRootNames(zr); err != nil {
		return nil, err
	}
	manifestFile := seen["manifest.json"]
	if manifestFile == nil || manifestFile.FileInfo().IsDir() || manifestFile.Name != "manifest.json" {
		return nil, errors.New("wpk: manifest.json missing")
	}
	hasPreview := (seen["preview.webp"] != nil && !seen["preview.webp"].FileInfo().IsDir() && seen["preview.webp"].Name == "preview.webp") ||
		(seen["preview.png"] != nil && !seen["preview.png"].FileInfo().IsDir() && seen["preview.png"].Name == "preview.png")
	if !hasPreview {
		return nil, errors.New("wpk: preview.webp|png missing")
	}
	mb, err := readAllLimited(manifestFile, MaxManifest)
	if err != nil {
		return nil, err
	}
	m, err := Parse(mb)
	if err != nil {
		return nil, err
	}
	if !norm.NFC.IsNormalString(m.Entry) {
		return nil, unsafeErr("entry %q must be NFC-normalized", m.Entry)
	}
	if entry := seen[strings.ToLower(m.Entry)]; entry == nil || entry.FileInfo().IsDir() || entry.Name != m.Entry {
		return nil, fmt.Errorf("wpk: entry %q not found in package", m.Entry)
	}
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(libraryDir, ".import-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return nil, err
	}
	res := &Result{Manifest: m}
	res.Files, res.Bytes, err = unpack(zr, root, archiveSize)
	root.Close()
	if err != nil {
		return nil, err
	}
	if beforePublish != nil {
		if err := beforePublish(m, tmp); err != nil {
			return nil, err
		}
	}
	dst := filepath.Join(libraryDir, m.ID)
	if err := replaceDirAtomic(tmp, dst); err != nil {
		return nil, err
	}
	ok = true
	res.Dir = dst
	return res, nil
}

func replaceDirAtomic(tmp, dst string) error {
	if _, err := os.Lstat(dst); err != nil {
		if os.IsNotExist(err) {
			return renameDir(tmp, dst)
		}
		return err
	}
	var rb [8]byte
	_, _ = rand.Read(rb[:])
	backup := dst + ".old-" + hex.EncodeToString(rb[:])
	if err := renameDir(dst, backup); err != nil {
		return err
	}
	if err := renameDir(tmp, dst); err != nil {
		if rbErr := renameDir(backup, dst); rbErr != nil {
			return fmt.Errorf("wpk: publish failed (%w) and rollback failed (%v)", err, rbErr)
		}
		return err
	}
	// Best-effort cleanup of the previous version after the new package is live.
	_ = os.RemoveAll(backup)
	return nil
}

func readAllLimited(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("wpk: %s too large", f.Name)
	}
	return b, nil
}

// countByteReader implements flate.Reader (io.Reader + io.ByteReader) so
// compress/flate reads directly from it without adding an extra buffer,
// letting us measure the exact number of compressed bytes consumed.
type countByteReader struct {
	br *bufio.Reader
	n  int64
}

func (c *countByteReader) Read(p []byte) (int, error) {
	k, err := c.br.Read(p)
	c.n += int64(k)
	return k, err
}

func (c *countByteReader) ReadByte() (byte, error) {
	b, err := c.br.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// copyEntry streams an entry to disk while enforcing size limits, 4-byte
// executable-header detection across short reads, and spoof-proof,
// overflow-free compression ratio checks.
func copyEntry(root *os.Root, name string, f *zip.File, limit, remaining, archiveSize int64) (int64, error) {
	if archiveSize > 0 && f.CompressedSize64 > uint64(archiveSize) {
		return 0, unsafeErr("%q: compressed size exceeds archive size", name)
	}
	if f.CompressedSize64 > uint64(math.MaxInt64/2) {
		return 0, unsafeErr("%q: invalid compressed size", name)
	}
	var rc io.ReadCloser
	var verifyCRC bool
	var cr *countByteReader
	var err error
	if f.Method == zip.Deflate && f.Flags&0x1 == 0 {
		raw, rerr := f.OpenRaw()
		if rerr != nil {
			return 0, rerr
		}
		cr = &countByteReader{br: bufio.NewReader(raw)}
		rc = flate.NewReader(cr)
		verifyCRC = true
	} else {
		rc, err = f.Open()
		if err != nil {
			return 0, err
		}
	}
	defer rc.Close()
	out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	buf := make([]byte, 64<<10)
	var n int64
	var head [4]byte
	headLen := 0
	headChecked := false
	crc := crc32.NewIEEE()
	effectiveComp := func() int64 {
		var comp int64
		if cr != nil {
			comp = cr.n
		} else if f.CompressedSize64 <= uint64(math.MaxInt64) {
			comp = int64(f.CompressedSize64)
		} else {
			comp = math.MaxInt64
		}
		if archiveSize > 0 && comp > archiveSize {
			comp = archiveSize
		}
		if comp < 1 {
			comp = 1
		}
		return comp
	}
	for {
		k, rerr := rc.Read(buf)
		if k > 0 {
			if !headChecked {
				take := min(k, len(head)-headLen)
				copy(head[headLen:], buf[:take])
				headLen += take
				if headLen >= 2 && IsExecutableHeader(head[:headLen]) {
					return 0, unsafeErr("%q: executable header", name)
				}
				if headLen == len(head) {
					headChecked = true
				}
			}
			n += int64(k)
			if n > limit {
				return 0, unsafeErr("%q: exceeds size limit", name)
			}
			if n > remaining {
				return 0, unsafeErr("package exceeds 4 GiB total")
			}
			if f.Method != zip.Store && n > ratioFloorRaw && n/MaxRatio > effectiveComp() {
				return 0, unsafeErr("%q: compression ratio > %d:1", name, MaxRatio)
			}
			if verifyCRC {
				_, _ = crc.Write(buf[:k])
			}
			if _, err := out.Write(buf[:k]); err != nil {
				return 0, err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, fmt.Errorf("wpk: %s: %w", name, rerr)
		}
	}
	if !headChecked && headLen > 0 && IsExecutableHeader(head[:headLen]) {
		return 0, unsafeErr("%q: executable header", name)
	}
	if verifyCRC && crc.Sum32() != f.CRC32 {
		return 0, fmt.Errorf("wpk: %s: %w", name, zip.ErrChecksum)
	}
	return n, nil
}

// WriteManifest 把 manifest 写到包目录（用于导入普通文件时自动生成的包）。
func WriteManifest(dir string, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	b, err := marshalManifest(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644)
}

// LoadDir 读取并校验已解压包目录里的 manifest，并确认 entry 存在且不越界。
func LoadDir(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	m, err := Parse(b)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	st, err := root.Stat(m.Entry)
	if err != nil {
		return nil, fmt.Errorf("wpk: entry: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("wpk: entry is not a regular file")
	}
	return m, nil
}
