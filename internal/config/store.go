package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Versioned 是带 schemaVersion 的文件。
type Versioned interface{ Version() int }

// Migration 把 schemaVersion=N 的原始 JSON 迁移到 N+1。
type Migration func(raw map[string]any) error

// ErrNewerSchema 表示文件来自更新版本的应用：已按只读方式加载已知字段，不应回写（16.3）。
var ErrNewerSchema = errors.New("config: file written by a newer app version; loaded read-only")

// WriteAtomic：写 tmp → fsync → 原子更新 .bak → rename 替换主文件。
func WriteAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, readErr := os.ReadFile(path)
	tmp := path + ".tmp"
	if err := writeSync(tmp, b); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	var backupErr error
	if readErr == nil && json.Valid(old) {
		backupErr = writeAtomicRaw(path+".bak", old)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if backupErr != nil {
		return fmt.Errorf("config: save succeeded but backup failed: %w", backupErr)
	}
	return nil
}

func writeAtomicRaw(dst string, b []byte) error {
	tmp := dst + ".tmp"
	if err := writeSync(tmp, b); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func decodeConfigDoc(b []byte) (raw map[string]any, ver int, overflow bool, err error) {
	if !json.Valid(b) {
		return nil, 0, false, errors.New("invalid JSON")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, 0, false, err
	}
	if obj == nil {
		return nil, 0, false, errors.New("top-level JSON value must be an object")
	}
	if rv, ok := obj["schemaVersion"]; ok {
		dec := json.NewDecoder(bytes.NewReader(rv))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, 0, false, fmt.Errorf("invalid schemaVersion: %w", err)
		}
		num, ok := val.(json.Number)
		if !ok {
			return nil, 0, false, errors.New("invalid schemaVersion: expected non-negative integer")
		}
		s := string(num)
		if s == "" || s[0] == '-' {
			return nil, 0, false, fmt.Errorf("invalid schemaVersion %q", s)
		}
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return nil, 0, false, fmt.Errorf("invalid schemaVersion %q", s)
			}
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			if errors.Is(err, strconv.ErrRange) {
				ver = math.MaxInt
				overflow = true
			} else {
				return nil, 0, false, fmt.Errorf("invalid schemaVersion %q: %w", s, err)
			}
		} else {
			ver = n
		}
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, 0, false, err
	}
	if raw == nil {
		return nil, 0, false, errors.New("top-level JSON value must be an object")
	}
	return raw, ver, overflow, nil
}

// Load 读取 path 到 v。主文件缺失返回 os.ErrNotExist（v 保持默认值）；
// 主文件损坏时回退到 .bak；schemaVersion 较旧时依次迁移（迁移前备份为 .v<N>）；较新时返回 ErrNewerSchema。
func Load(path string, current int, migrations map[int]Migration, v any) error {
	b, err := os.ReadFile(path)
	var raw map[string]any
	var ver int
	var overflow bool
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			bb, e2 := os.ReadFile(path + ".bak")
			if e2 != nil {
				return os.ErrNotExist
			}
			raw, ver, overflow, e2 = decodeConfigDoc(bb)
			if e2 != nil {
				return os.ErrNotExist
			}
			b = bb
		} else {
			return err
		}
	} else {
		var parseErr error
		raw, ver, overflow, parseErr = decodeConfigDoc(b)
		if parseErr != nil {
			bb, e2 := os.ReadFile(path + ".bak")
			if e2 != nil {
				return fmt.Errorf("config: %s corrupt and no usable .bak: %w", filepath.Base(path), parseErr)
			}
			raw, ver, overflow, e2 = decodeConfigDoc(bb)
			if e2 != nil {
				return fmt.Errorf("config: %s corrupt and no usable .bak: %w", filepath.Base(path), parseErr)
			}
			b = bb
		}
	}
	newer := overflow || ver > current
	if !newer && ver < current {
		if err := writeAtomicRaw(fmt.Sprintf("%s.v%d", path, ver), b); err != nil {
			return fmt.Errorf("config: migration backup: %w", err)
		}
		for n := ver; n < current; n++ {
			if m := migrations[n]; m != nil {
				if err := m(raw); err != nil {
					return fmt.Errorf("config: migrate %d→%d: %w", n, n+1, err)
				}
			}
			raw["schemaVersion"] = n + 1
		}
		if b, err = json.Marshal(raw); err != nil {
			return err
		}
	}
	unmarshalBytes := b
	if overflow {
		// Clamp huge future schemaVersion in memory only so known fields can still
		// be decoded read-only without overflowing int struct fields.
		raw["schemaVersion"] = math.MaxInt
		if clamped, err := json.Marshal(raw); err == nil {
			unmarshalBytes = clamped
		}
	}
	if err := json.Unmarshal(unmarshalBytes, v); err != nil {
		return err
	}
	if newer {
		return ErrNewerSchema
	}
	return nil
}

// Saver 把同一文件 1 s 内的多次修改合并成一次写（16.3 写节流）。ReadOnly 时不写。
type Saver struct {
	Path     string
	Delay    time.Duration
	ReadOnly bool
	mu       sync.Mutex
	writeMu  sync.Mutex
	lastErr  error
	pending  func() any
	t        *time.Timer
	OnError  func(error)
	// writeAtomic 可在测试中替换，以控制磁盘写入的完成时机；nil 使用 WriteAtomic。
	writeAtomic func(string, any) error
}

// Schedule 记录最新快照，Delay 之后写入。snap 在写入时调用，须自行保证并发安全。
func (s *Saver) Schedule(snap func() any) {
	if s.ReadOnly {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = snap
	if s.t == nil {
		d := s.Delay
		if d == 0 {
			d = time.Second
		}
		s.t = time.AfterFunc(d, func() { _ = s.Flush() })
	}
}

// Flush 等待正在进行的写入，然后立即写出待写内容。
func (s *Saver) Flush() error {
	err, attempted := s.flush()
	// 回调可再次 Schedule/Flush；不在持有写锁时调用。
	if attempted && err != nil && s.OnError != nil {
		s.OnError(err)
	}
	return err
}

func (s *Saver) flush() (error, bool) {
	// 必须先取得写锁，再取出最新快照。否则多个 Flush 可能乱序覆盖文件，
	// 或同时使用同一个 .tmp；即使 pending 为空，退出刷新也必须等待磁盘写完。
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	snap := s.pending
	s.pending = nil
	if s.t != nil {
		s.t.Stop()
		s.t = nil
	}
	s.mu.Unlock()
	if snap == nil || s.ReadOnly {
		return s.lastErr, false
	}
	write := s.writeAtomic
	if write == nil {
		write = WriteAtomic
	}
	s.lastErr = write(s.Path, snap())
	if s.lastErr != nil {
		s.lastErr = fmt.Errorf("config: save %s: %w", filepath.Base(s.Path), s.lastErr)
	}
	return s.lastErr, true
}
