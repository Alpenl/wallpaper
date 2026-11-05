package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Versioned 是带 schemaVersion 的文件。
type Versioned interface{ Version() int }

// Migration 把 schemaVersion=N 的原始 JSON 迁移到 N+1。
type Migration func(raw map[string]any) error

// ErrNewerSchema 表示文件来自更新版本的应用：已按只读方式加载已知字段，不应回写（16.3）。
var ErrNewerSchema = errors.New("config: file written by a newer app version; loaded read-only")

// ErrOldSchema 表示文件来自更旧的 schema，本版本不迁移该文件。
var ErrOldSchema = errors.New("config: older schema is not supported")

// WriteAtomic：写 tmp → fsync → rename；成功后把旧主文件保留为 .bak（前提是它能解析）。
func WriteAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var backupErr error
	if old, err := os.ReadFile(path); err == nil && json.Valid(old) {
		backupErr = writeSync(path+".bak", old)
	}
	tmp := path + ".tmp"
	if err := writeSync(tmp, b); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if backupErr != nil {
		return fmt.Errorf("config: save succeeded but backup failed: %w", backupErr)
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

// readConfigFile reads path, falling back to .bak only when the main file is
// missing or invalid. Other read errors are returned so callers never mistake
// a transient lock for an old or missing file.
func readConfigFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if bb, e2 := os.ReadFile(path + ".bak"); e2 == nil && json.Valid(bb) {
				return bb, nil
			}
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	if !json.Valid(b) {
		bb, e2 := os.ReadFile(path + ".bak")
		if e2 != nil || !json.Valid(bb) {
			return nil, fmt.Errorf("config: %s corrupt and no usable .bak", filepath.Base(path))
		}
		return bb, nil
	}
	return b, nil
}

func schemaVersionOf(raw map[string]any) int {
	if f, ok := raw["schemaVersion"].(float64); ok {
		return int(f)
	}
	return 0
}

// Load 读取 path 到 v。主文件缺失返回 os.ErrNotExist（v 保持默认值）；
// 主文件损坏时回退到 .bak；schemaVersion 较旧时依次迁移（迁移前备份为 .v<N>）；较新时返回 ErrNewerSchema。
func Load(path string, current int, migrations map[int]Migration, v any) error {
	b, err := readConfigFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	ver := schemaVersionOf(raw)
	newer := ver > current
	if ver < current {
		if err := writeSync(fmt.Sprintf("%s.v%d", path, ver), b); err != nil {
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
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	if newer {
		return ErrNewerSchema
	}
	return nil
}

// LoadCurrent reads path into v only when it is at the current schema or newer.
// Older files are not migrated: they return ErrOldSchema and leave v untouched.
// Newer files are loaded and returned with ErrNewerSchema, like Load.
func LoadCurrent(path string, current int, v any) error {
	b, err := readConfigFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	ver := schemaVersionOf(raw)
	if ver < current {
		return ErrOldSchema
	}
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	if ver > current {
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
