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

// WriteAtomic：写 tmp → fsync → rename；成功后把旧主文件保留为 .bak（前提是它能解析）。
func WriteAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil && json.Valid(old) {
		_ = writeSync(path+".bak", old)
	}
	tmp := path + ".tmp"
	if err := writeSync(tmp, b); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// Load 读取 path 到 v。主文件缺失返回 os.ErrNotExist（v 保持默认值）；
// 主文件损坏时回退到 .bak；schemaVersion 较旧时依次迁移（迁移前备份为 .v<N>）；较新时返回 ErrNewerSchema。
func Load(path string, current int, migrations map[int]Migration, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if bb, e2 := os.ReadFile(path + ".bak"); e2 == nil && json.Valid(bb) {
				b = bb
			} else {
				return os.ErrNotExist
			}
		} else {
			return err
		}
	}
	if !json.Valid(b) {
		bb, e2 := os.ReadFile(path + ".bak")
		if e2 != nil || !json.Valid(bb) {
			return fmt.Errorf("config: %s corrupt and no usable .bak", filepath.Base(path))
		}
		b = bb
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	ver := 0
	if f, ok := raw["schemaVersion"].(float64); ok {
		ver = int(f)
	}
	newer := ver > current
	if ver < current {
		_ = writeSync(fmt.Sprintf("%s.v%d", path, ver), b)
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

// Saver 把同一文件 1 s 内的多次修改合并成一次写（16.3 写节流）。ReadOnly 时不写。
type Saver struct {
	Path     string
	Delay    time.Duration
	ReadOnly bool
	mu       sync.Mutex
	writeMu  sync.Mutex
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
	err := s.flush()
	// 回调可再次 Schedule/Flush；不在持有写锁时调用。
	if err != nil && s.OnError != nil {
		s.OnError(err)
	}
	return err
}

func (s *Saver) flush() error {
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
		return nil
	}
	write := s.writeAtomic
	if write == nil {
		write = WriteAtomic
	}
	return write(s.Path, snap())
}
