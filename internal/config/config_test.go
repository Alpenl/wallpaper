package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultsMatchDecisions(t *testing.T) {
	c := Defaults()
	if c.Pause.BatteryPolicy != "pause" || c.Pause.LowBatteryPct != 20 {
		t.Fatal("D-07 默认值不对", c.Pause)
	}
	if c.Render.WindowsBackend != "mpv" || c.Privacy.Telemetry || c.Startup.LaunchAtLogin {
		t.Fatal("D-04/D-15/F-10 默认值不对", c)
	}
}

func TestNormalize(t *testing.T) {
	c := Config{Pause: Pause{BatteryPolicy: "zzz", LowBatteryPct: 300}, Render: Render{WindowsBackend: "x"}, Privacy: Privacy{Telemetry: true}, Perf: Perf{FPSCap: -1}}
	c.Normalize()
	if c.Pause.BatteryPolicy != "pause" || c.Pause.LowBatteryPct != 20 || c.Render.WindowsBackend != "mpv" || c.Privacy.Telemetry || c.Perf.FPSCap != 30 || c.Pause.ResumeDebounceMs != 300 {
		t.Fatal(c)
	}
	c.Render.WindowsBackend = "mf"
	c.Normalize()
	if c.Render.WindowsBackend != "mf" {
		t.Fatal("mf 应保留")
	}
}

func TestAtomicWriteAndBak(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	a := Defaults()
	if err := WriteAtomic(p, a); err != nil {
		t.Fatal(err)
	}
	a.Pause.LowBatteryPct = 33
	if err := WriteAtomic(p, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tmp 应已被 rename")
	}
	// 主文件损坏 → 回退到 .bak（上一个版本 lowBatteryPct=20）
	os.WriteFile(p, []byte("{broken"), 0o644)
	var got Config
	if err := Load(p, ConfigVersion, nil, &got); err != nil {
		t.Fatal(err)
	}
	if got.Pause.LowBatteryPct != 20 {
		t.Fatal("应读到 .bak", got.Pause)
	}
	// 主文件缺失、.bak 也坏 → 报错
	os.Remove(p)
	os.WriteFile(p+".bak", []byte("x"), 0o644)
	if err := Load(p, ConfigVersion, nil, &got); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("x"), 0o644)
	if err := Load(p, ConfigVersion, nil, &got); err == nil {
		t.Fatal("都损坏时应报错")
	}
}

func TestMigrationAndNewer(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	os.WriteFile(p, []byte(`{"schemaVersion":0,"old":5}`), 0o644)
	var out struct {
		SchemaVersion int `json:"schemaVersion"`
		New           int `json:"new"`
	}
	mig := map[int]Migration{0: func(r map[string]any) error { r["new"] = r["old"]; delete(r, "old"); return nil }}
	if err := Load(p, 1, mig, &out); err != nil || out.New != 5 || out.SchemaVersion != 1 {
		t.Fatal(err, out)
	}
	if _, err := os.Stat(p + ".v0"); err != nil {
		t.Fatal("迁移前应备份 .v0")
	}
	bad := map[int]Migration{0: func(map[string]any) error { return errors.New("boom") }}
	if err := Load(p+".v0", 1, bad, &out); err == nil {
		t.Fatal("迁移失败应报错")
	}
	os.WriteFile(p, []byte(`{"schemaVersion":9,"new":7,"future":1}`), 0o644)
	if err := Load(p, 1, nil, &out); !errors.Is(err, ErrNewerSchema) || out.New != 7 {
		t.Fatal(err, out)
	}
}

func TestStoreOpenReadOnlyAndSaver(t *testing.T) {
	t.Setenv("WPAPP_HOME", t.TempDir())
	p, _ := DefaultPaths()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Pause.BatteryPolicy = "fps15"
	s.ConfigSaver.Delay = 20 * time.Millisecond
	var n atomic.Int32
	for i := 0; i < 5; i++ {
		s.ConfigSaver.Schedule(func() any { n.Add(1); return s.Config })
	}
	time.Sleep(100 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("1 s 内的多次修改应合并成一次写，实际 %d 次", n.Load())
	}
	s2, err := Open(p)
	if err != nil || s2.Config.Pause.BatteryPolicy != "fps15" {
		t.Fatal(err, s2.Config.Pause)
	}
	// 更新版本写的文件：只读
	b, _ := json.Marshal(map[string]any{"schemaVersion": 99})
	os.WriteFile(filepath.Join(p.Config, "library.json"), b, 0o644)
	s3, err := Open(p)
	if err != nil || !s3.LibrarySaver.ReadOnly || len(s3.ReadOnlyWarn) != 1 {
		t.Fatal(err, s3.ReadOnlyWarn)
	}
	s3.LibrarySaver.Schedule(func() any { return s3.Library })
	s3.FlushAll()
	b2, _ := os.ReadFile(filepath.Join(p.Config, "library.json"))
	if string(b2) != string(b) {
		t.Fatal("只读文件不应回写")
	}
}

func TestLibraryAndAssignments(t *testing.T) {
	var l Library
	l.Upsert(Item{ID: "a", Title: "1"})
	l.Upsert(Item{ID: "b"})
	l.Upsert(Item{ID: "a", Title: "2"})
	if len(l.Items) != 2 || l.Find("a").Title != "2" {
		t.Fatal(l)
	}
	if !l.Remove("a") || l.Remove("zz") || l.Find("a") != nil {
		t.Fatal(l)
	}
	a := Assignments{Default: "d", Displays: map[string]string{"x": "1"}}
	if a.For("x") != "1" || a.For("y") != "d" {
		t.Fatal(a)
	}
}

func TestSafeMode(t *testing.T) {
	s := State{CleanShutdown: true}
	t0 := time.Unix(1000, 0)
	if s.EnterSafeMode(t0) {
		t.Fatal("首次启动不应进入安全模式")
	}
	for i := 1; i <= 2; i++ {
		if s.EnterSafeMode(t0.Add(time.Duration(i) * 10 * time.Second)) {
			t.Fatal(i)
		}
	}
	if !s.EnterSafeMode(t0.Add(40 * time.Second)) {
		t.Fatal("2 分钟内此前已有 3 次非正常启动 → 安全模式")
	}
	// 隔了很久
	if s.EnterSafeMode(t0.Add(time.Hour)) {
		t.Fatal("超出 2 分钟窗口不应进入")
	}
	s.CleanShutdown = true
	if s.EnterSafeMode(t0.Add(time.Hour+time.Second)) || len(s.Starts) != 1 {
		t.Fatal("正常退出后应清零", s.Starts)
	}
}

func TestPathsPlatforms(t *testing.T) {
	t.Setenv("WPAPP_HOME", "")
	p, err := DefaultPaths()
	if err != nil {
		t.Skip(err)
	}
	if p.Config == "" || p.Library == "" || p.Cache == "" || p.Logs == "" {
		t.Fatal(p)
	}
}

func TestConfigAndAssignmentsFutureSchemaUnchanged(t *testing.T) {
	for _, name := range []string{"config.json", "assignments.json"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WPAPP_HOME", t.TempDir())
			p, err := DefaultPaths()
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Ensure(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(p.Config, name)
			before := []byte("{\n  \"schemaVersion\": 99, \"future\": [1, 2, 3]\n}\n")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			saver, value := s.ConfigSaver, any(s.Config)
			if name == "assignments.json" {
				saver, value = s.AssignSaver, s.Assign
			}
			if !saver.ReadOnly {
				t.Fatal("future schema must be read-only")
			}
			saver.Schedule(func() any { return value })
			s.FlushAll()
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("future schema file changed")
			}
		})
	}
}
