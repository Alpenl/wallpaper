// Package wescene inspects local, unencrypted WE packages without extracting assets.
// Call Probe only in an isolated helper process. This is not a sandbox.
package wescene

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxEntries    = 10000
	MaxIndexBytes = 4 << 20
	MaxSceneBytes = 16 << 20
)

type Entry struct {
	Name         string
	Offset, Size int64
}
type Index struct {
	Version    int
	DataOffset int64
	Entries    []Entry
}

type indexReader struct {
	r         io.ReaderAt
	size, pos int64
}

func (r *indexReader) bytes(n int64) ([]byte, error) {
	if n < 0 || n > MaxIndexBytes-r.pos || r.pos > r.size || n > r.size-r.pos {
		return nil, fmt.Errorf("truncated index or index budget exceeded")
	}
	b := make([]byte, int(n))
	_, err := r.r.ReadAt(b, r.pos)
	r.pos += n
	return b, err
}
func (r *indexReader) number() (int64, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	n := int64(int32(binary.LittleEndian.Uint32(b)))
	if n < 0 {
		return 0, fmt.Errorf("negative index value")
	}
	return n, nil
}
func cleanName(s string) (string, error) {
	if s == "" || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\\:") || strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("invalid entry name")
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal in entry name")
		}
	}
	s = path.Clean(s)
	if s == "." {
		return "", fmt.Errorf("empty normalized name")
	}
	return s, nil
}

// ReadIndex validates all table and data ranges before returning any index.
// Names use case-sensitive slash-path normalization; aliases are rejected.
func ReadIndex(r io.ReaderAt, size int64) (*Index, error) {
	rd := &indexReader{r: r, size: size}
	n, err := rd.number()
	if err != nil {
		return nil, err
	}
	if n != 8 {
		return nil, fmt.Errorf("invalid magic length")
	}
	magic, err := rd.bytes(n)
	if err != nil {
		return nil, err
	}
	if string(magic[:4]) != "PKGV" {
		return nil, fmt.Errorf("invalid package magic")
	}
	version := 0
	for _, c := range magic[4:] {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("invalid package version")
		}
		version = version*10 + int(c-'0')
	}
	if version < 1 || version > 24 {
		return nil, fmt.Errorf("unsupported package version %04d", version)
	}
	count, err := rd.number()
	if err != nil {
		return nil, err
	}
	if count > MaxEntries {
		return nil, fmt.Errorf("entry count exceeds budget")
	}
	idx := &Index{Version: version, Entries: make([]Entry, 0, int(count))}
	seen := make(map[string]bool)
	for i := int64(0); i < count; i++ {
		n, err = rd.number()
		if err != nil {
			return nil, err
		}
		if n == 0 || n > 1024 {
			return nil, fmt.Errorf("invalid name length")
		}
		b, err := rd.bytes(n)
		if err != nil {
			return nil, err
		}
		name, err := cleanName(string(b))
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate normalized entry name")
		}
		seen[name] = true
		offset, err := rd.number()
		if err != nil {
			return nil, err
		}
		length, err := rd.number()
		if err != nil {
			return nil, err
		}
		idx.Entries = append(idx.Entries, Entry{name, offset, length})
	}
	idx.DataOffset = rd.pos
	ranges := make([]Entry, 0, len(idx.Entries))
	for i := range idx.Entries {
		e := &idx.Entries[i]
		if e.Offset > size-rd.pos || e.Size > size-rd.pos-e.Offset {
			return nil, fmt.Errorf("entry data out of range")
		}
		e.Offset += rd.pos
		if e.Size > 0 {
			ranges = append(ranges, *e)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Offset < ranges[j].Offset })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].Offset < ranges[i-1].Offset+ranges[i-1].Size {
			return nil, fmt.Errorf("overlapping entry data")
		}
	}
	return idx, nil
}
