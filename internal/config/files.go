package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// 当前各文件的 schemaVersion。
const (
	ConfigVersion      = 1
	LibraryVersion     = 1
	AssignmentsVersion = 1
	StateVersion       = 1
)

// Config 是 config.json（16.2）。默认值见 Defaults，对应 DECISIONS_PENDING 的 D-07/D-15。
type Config struct {
	SchemaVersion int     `json:"schemaVersion"`
	Perf          Perf    `json:"perf"`
	Pause         Pause   `json:"pause"`
	Startup       Startup `json:"startup"`
	Privacy       Privacy `json:"privacy"`
	Render        Render  `json:"render"`
	UI            UI      `json:"ui"`
	Update        Update  `json:"update"`
}

// Update 是 F-15 增量更新的客户端设置（internal/update）。FeedURL 为空 = 未配置更新源。
type Update struct {
	FeedURL   string `json:"feedURL,omitempty"`
	AutoCheck bool   `json:"autoCheck"`
}

type Perf struct {
	Profile     string  `json:"profile"` // balanced | smooth | saver
	FPSCap      int     `json:"fpsCap"`
	RenderScale float64 `json:"renderScale"`
}

type Pause struct {
	BatteryPolicy    string `json:"batteryPolicy"` // pause | fps15 | ignore
	LowBatteryPct    int    `json:"lowBatteryPct"`
	ResumeDebounceMs int    `json:"resumeDebounceMs"`
	OnFullscreen     bool   `json:"onFullscreen"`
	OnCovered        bool   `json:"onCovered"`
}

type Startup struct {
	LaunchAtLogin bool `json:"launchAtLogin"`
	Asked         bool `json:"asked"` // F-10：首次运行时询问过
}

type Privacy struct {
	Telemetry    bool   `json:"telemetry"` // D-15：不做遥测，恒为 false
	CrashReports string `json:"crashReports"`
}

// Render 选择渲染后端（D-04：Windows 默认 mpv，可选 mf）。
type Render struct {
	WindowsBackend string `json:"windowsBackend"`
	MpvPath        string `json:"mpvPath,omitempty"` // 空 = 安装目录下的 mpv/mpv(.exe)，再找 PATH
	Hwdec          string `json:"hwdec,omitempty"`
	FFmpegPath     string `json:"ffmpegPath,omitempty"` // F-07：用户自己安装的 ffmpeg（不随安装包分发）
}

type UI struct {
	Lang  string `json:"lang"`
	Theme string `json:"theme"` // system | light | dark
	// Accent 是强调色名（blue | purple | green | orange | pink）。
	Accent string `json:"accent"`
	// LibraryView 是壁纸库的视图（grid | list）。
	LibraryView string `json:"libraryView"`
}

// Defaults 返回默认配置。
func Defaults() Config {
	return Config{SchemaVersion: ConfigVersion,
		Perf:    Perf{Profile: "balanced", FPSCap: 30, RenderScale: 1},
		Pause:   Pause{BatteryPolicy: "pause", LowBatteryPct: 20, ResumeDebounceMs: 300, OnFullscreen: true, OnCovered: true},
		Startup: Startup{LaunchAtLogin: false},
		Privacy: Privacy{Telemetry: false, CrashReports: "never"},
		Render:  Render{WindowsBackend: "mpv"},
		UI:      UI{Lang: "zh-CN", Theme: "system", Accent: "blue", LibraryView: "grid"},
		Update:  Update{AutoCheck: true}}
}

// Normalize 修正越界值。
func (c *Config) Normalize() {
	d := Defaults()
	switch c.Pause.BatteryPolicy {
	case "pause", "fps15", "ignore":
	default:
		c.Pause.BatteryPolicy = d.Pause.BatteryPolicy
	}
	if c.Pause.LowBatteryPct < 0 || c.Pause.LowBatteryPct > 100 {
		c.Pause.LowBatteryPct = d.Pause.LowBatteryPct
	}
	if c.Pause.ResumeDebounceMs <= 0 || c.Pause.ResumeDebounceMs > 10000 {
		c.Pause.ResumeDebounceMs = d.Pause.ResumeDebounceMs
	}
	if c.Render.WindowsBackend != "mf" {
		c.Render.WindowsBackend = "mpv"
	}
	if c.Perf.FPSCap < 0 || c.Perf.FPSCap > 240 {
		c.Perf.FPSCap = d.Perf.FPSCap
	}
	switch c.Perf.Profile {
	case "smooth", "balanced", "saver":
	default:
		c.Perf.Profile = d.Perf.Profile
	}
	switch c.UI.Theme {
	case "system", "light", "dark":
	default:
		c.UI.Theme = d.UI.Theme
	}
	switch c.UI.Accent {
	case "blue", "purple", "green", "orange", "pink":
	default:
		c.UI.Accent = d.UI.Accent
	}
	if c.UI.LibraryView != "list" {
		c.UI.LibraryView = "grid"
	}
	c.Privacy.Telemetry = false
}

// Item 是 library.json 的一项。
type Item struct {
	ID        string    `json:"id"`
	Dir       string    `json:"dir"` // library/<id>
	Schema    int       `json:"schema"`
	Origin    string    `json:"origin"` // native | lively | we | file
	Title     string    `json:"title"`
	Type      string    `json:"type"` // video | image
	Entry     string    `json:"entry"`
	Codec     string    `json:"codec,omitempty"`
	Transcode string    `json:"transcode"` // none | recommended | done
	Note      string    `json:"note,omitempty"`
	Snapshot  string    `json:"snapshot,omitempty"` // cache/<id>/first.png
	AddedAt   time.Time `json:"addedAt"`
}

// Library 是 library.json。
type Library struct {
	SchemaVersion int    `json:"schemaVersion"`
	Items         []Item `json:"items"`
}

// Find 按 id 查找。
func (l *Library) Find(id string) *Item {
	for i := range l.Items {
		if l.Items[i].ID == id {
			return &l.Items[i]
		}
	}
	return nil
}

// Upsert 新增或替换（同 id 视为更新，10.3）。
func (l *Library) Upsert(it Item) {
	if p := l.Find(it.ID); p != nil {
		*p = it
		return
	}
	l.Items = append(l.Items, it)
}

// Remove 删除。
func (l *Library) Remove(id string) bool {
	for i := range l.Items {
		if l.Items[i].ID == id {
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
			return true
		}
	}
	return false
}

// Assignments 是 assignments.json：显示器稳定 ID → 壁纸 id。
type Assignments struct {
	SchemaVersion int               `json:"schemaVersion"`
	Default       string            `json:"default,omitempty"`
	Displays      map[string]string `json:"displays"`
}

// For 返回某显示器的壁纸 id（没有单独设置时用 Default）。
func (a *Assignments) For(display string) string {
	if v, ok := a.Displays[display]; ok {
		return v
	}
	return a.Default
}

// State 是 state.json。
type State struct {
	SchemaVersion int                `json:"schemaVersion"`
	CleanShutdown bool               `json:"cleanShutdown"`
	Starts        []time.Time        `json:"starts"`             // 非正常退出后的启动时间（安全模式判定）
	Positions     map[string]float64 `json:"positions"`          // 显示器 → time-pos（续播）
	SafeMode      bool               `json:"safeMode,omitempty"` // 当前是否处于安全模式
}

// EnterSafeMode 实现 2.5 第 4 条：上次非正常退出且 2 分钟内已有 3 次这样的启动 → 安全模式。
// 调用后 State 已记录本次启动并置 CleanShutdown=false，调用方应立刻保存。
func (s *State) EnterSafeMode(now time.Time) bool {
	if s.CleanShutdown {
		s.Starts = nil
	}
	keep := s.Starts[:0]
	for _, t := range s.Starts {
		if now.Sub(t) <= 2*time.Minute {
			keep = append(keep, t)
		}
	}
	s.Starts = append(keep, now)
	s.SchemaVersion = StateVersion
	s.CleanShutdown = false
	s.SafeMode = len(s.Starts) > 3
	return s.SafeMode
}

// Store 汇总四个文件。
type Store struct {
	Paths        Paths
	ConfigSaver  *Saver
	LibrarySaver *Saver
	AssignSaver  *Saver
	StateSaver   *Saver
	Config       Config
	Library      Library
	Assign       Assignments
	State        State
	ReadOnlyWarn []string
}

// Open 读取全部文件；缺失时用默认值。
func Open(p Paths) (*Store, error) {
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	s := &Store{Paths: p, Config: Defaults(), Library: Library{SchemaVersion: LibraryVersion},
		Assign: Assignments{SchemaVersion: AssignmentsVersion, Displays: map[string]string{}},
		State:  State{SchemaVersion: StateVersion, CleanShutdown: true, Positions: map[string]float64{}}}
	mk := func(name string) *Saver { return &Saver{Path: filepath.Join(p.Config, name)} }
	s.ConfigSaver, s.LibrarySaver, s.AssignSaver, s.StateSaver = mk("config.json"), mk("library.json"), mk("assignments.json"), mk("state.json")
	s.StateSaver.Delay = 30 * time.Second // 16.2：state 节流 30 s
	load := func(sv *Saver, ver int, v any) error {
		err := Load(sv.Path, ver, nil, v)
		switch {
		case err == nil, errors.Is(err, os.ErrNotExist):
			return nil
		case errors.Is(err, ErrNewerSchema):
			sv.ReadOnly = true
			s.ReadOnlyWarn = append(s.ReadOnlyWarn, filepath.Base(sv.Path))
			return nil
		}
		return err
	}
	if err := load(s.ConfigSaver, ConfigVersion, &s.Config); err != nil {
		return nil, err
	}
	s.Config.Normalize()
	if err := load(s.LibrarySaver, LibraryVersion, &s.Library); err != nil {
		return nil, err
	}
	if err := load(s.AssignSaver, AssignmentsVersion, &s.Assign); err != nil {
		return nil, err
	}
	if s.Assign.Displays == nil {
		s.Assign.Displays = map[string]string{}
	}
	if err := load(s.StateSaver, StateVersion, &s.State); err != nil {
		return nil, err
	}
	if s.State.Positions == nil {
		s.State.Positions = map[string]float64{}
	}
	return s, nil
}

// FlushAll 立刻写出所有待写内容。
func (s *Store) FlushAll() {
	for _, sv := range []*Saver{s.ConfigSaver, s.LibrarySaver, s.AssignSaver, s.StateSaver} {
		_ = sv.Flush()
	}
}
