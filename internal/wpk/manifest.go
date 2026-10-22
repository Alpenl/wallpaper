// Package wpk 实现 10 章的壁纸包：manifest v0 解析与校验、安全解压。
// schema 里 entry 的正则用了前瞻断言（RE2 不支持），所以路径规则全部在代码里实现（10.4）。
package wpk

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"
)

// SchemaCurrent 是当前支持的格式主版本；读取范围 [当前-1, 当前]（10.3 版本策略 1）。
const SchemaCurrent = 0

// MaxManifest 是 manifest.json 上限。
const MaxManifest = 256 << 10

var (
	reUUID   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reLang   = regexp.MustCompile(`^[a-z]{2}(-[A-Z][A-Za-z]{1,3})?$`)
	reSemver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	rePropK  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	reHost   = regexp.MustCompile(`^[a-z0-9.-]+$`)
	reColor  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// ErrNewerSchema：包的主版本比应用新，拒绝导入并提示升级。
var ErrNewerSchema = errors.New("wpk: package schema is newer than this app supports")

// I18n 是多语言文本。
type I18n map[string]string

// Pick 选语言：精确 → 语言前缀 → en → 任意。
func (t I18n) Pick(lang string) string {
	if v, ok := t[lang]; ok {
		return v
	}
	if i := strings.IndexByte(lang, '-'); i > 0 {
		if v, ok := t[lang[:i]]; ok {
			return v
		}
	}
	if v, ok := t["en"]; ok {
		return v
	}
	for _, v := range t {
		return v
	}
	return ""
}

type Author struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Video struct {
	Loop        *bool   `json:"loop,omitempty"`
	DurationSec float64 `json:"durationSec,omitempty"`
	W           int     `json:"w,omitempty"`
	H           int     `json:"h,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
	Codec       string  `json:"codec,omitempty"`
	HasAudio    bool    `json:"hasAudio,omitempty"`
}

type Web struct {
	Permissions  []string `json:"permissions,omitempty"`
	NetworkAllow []string `json:"networkAllow,omitempty"`
	FPSCap       int      `json:"fpsCap,omitempty"`
	Transparent  bool     `json:"transparent,omitempty"`
}

type Shader struct {
	Dialect string   `json:"dialect"`
	Passes  []string `json:"passes,omitempty"`
}

type Prop struct {
	MaxLength int               `json:"maxLength,omitempty"`
	Key       string            `json:"key"`
	Type      string            `json:"type"`
	Label     I18n              `json:"label,omitempty"`
	Default   json.RawMessage   `json:"default"`
	Min       *float64          `json:"min,omitempty"`
	Max       *float64          `json:"max,omitempty"`
	Step      *float64          `json:"step,omitempty"`
	Options   []json.RawMessage `json:"options,omitempty"`
}

type Perf struct {
	MaxFPS         int     `json:"maxFps,omitempty"`
	MinRenderScale float64 `json:"minRenderScale,omitempty"`
	AllowBattery   *bool   `json:"allowBattery,omitempty"`
}

type Origin struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

// Manifest 是 manifest.json v0（10.3）。Raw 保留原始字节，再次导出时不丢失未知字段。
type Manifest struct {
	Format      string   `json:"format"`
	Schema      *int     `json:"schema"`
	MinApp      string   `json:"minApp,omitempty"`
	ID          string   `json:"id"`
	Version     int      `json:"version"`
	Type        string   `json:"type"`
	Entry       string   `json:"entry"`
	Title       I18n     `json:"title"`
	Description I18n     `json:"description,omitempty"`
	Author      *Author  `json:"author,omitempty"`
	License     string   `json:"license,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Video       *Video   `json:"video,omitempty"`
	Web         *Web     `json:"web,omitempty"`
	Shader      *Shader  `json:"shader,omitempty"`
	Properties  []Prop   `json:"properties,omitempty"`
	Perf        *Perf    `json:"perf,omitempty"`
	Origin      *Origin  `json:"origin,omitempty"`
	Raw         []byte   `json:"-"`
}

// Parse 解析并校验 manifest。
func Parse(b []byte) (*Manifest, error) {
	if len(b) > MaxManifest {
		return nil, fmt.Errorf("wpk: manifest.json exceeds 256 KiB")
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("wpk: manifest.json: %w", err)
	}
	m.Raw = append([]byte(nil), b...)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func bad(f string, a ...any) error { return fmt.Errorf("wpk: manifest: "+f, a...) }

func validI18n(t I18n, field string, required bool) error {
	if len(t) == 0 {
		if required {
			return bad("%s is required", field)
		}
		return nil
	}
	for k, v := range t {
		if !reLang.MatchString(k) {
			return bad("%s: bad language tag %q", field, k)
		}
		if len([]rune(v)) > 2000 {
			return bad("%s: text too long", field)
		}
	}
	return nil
}

// Validate 实现 schema 的全部约束（路径规则用 CheckPath）。
func (m *Manifest) Validate() error {
	if m.Format != "wpk" {
		return bad("format must be \"wpk\"")
	}
	if m.Schema == nil {
		return bad("schema is required")
	}
	if *m.Schema > SchemaCurrent {
		return ErrNewerSchema
	}
	if *m.Schema < SchemaCurrent-1 || *m.Schema < 0 {
		return bad("schema %d too old", *m.Schema)
	}
	if m.MinApp != "" && !reSemver.MatchString(m.MinApp) {
		return bad("minApp must be semver")
	}
	if !reUUID.MatchString(m.ID) {
		return bad("id must be a lowercase UUID")
	}
	if m.Version < 1 {
		return bad("version must be >= 1")
	}
	switch m.Type {
	case "video", "web", "shader", "image":
	default:
		return bad("unknown type %q", m.Type)
	}
	if len(m.Entry) > 512 || !strings.HasPrefix(m.Entry, "content/") {
		return bad("entry must be under content/")
	}
	if err := CheckPath(m.Entry); err != nil {
		return bad("entry: %v", err)
	}
	if err := validI18n(m.Title, "title", true); err != nil {
		return err
	}
	if err := validI18n(m.Description, "description", false); err != nil {
		return err
	}
	if m.Author != nil && (m.Author.Name == "" || len(m.Author.Name) > 128) {
		return bad("author.name required, ≤128")
	}
	if len(m.License) > 64 {
		return bad("license too long")
	}
	if len(m.Tags) > 16 {
		return bad("at most 16 tags")
	}
	for _, t := range m.Tags {
		if len([]rune(t)) > 32 {
			return bad("tag too long")
		}
	}
	switch m.Type {
	case "video":
		if m.Video == nil {
			return bad("video object required for type video")
		}
		v := m.Video
		if v.W != 0 && (v.W < 16 || v.W > 16384) || v.H != 0 && (v.H < 16 || v.H > 16384) {
			return bad("video w/h out of range")
		}
		if v.FPS < 0 || v.FPS > 240 || v.DurationSec < 0 {
			return bad("video fps/duration out of range")
		}
		switch v.Codec {
		case "", "h264", "hevc", "av1", "vp9", "prores", "other":
		default:
			return bad("unknown codec %q", v.Codec)
		}
	case "web":
		if m.Web == nil {
			return bad("web object required for type web")
		}
		seen := map[string]bool{}
		for _, p := range m.Web.Permissions {
			if p != "network" && p != "audio-reactive" || seen[p] {
				return bad("bad web permission %q", p)
			}
			seen[p] = true
		}
		if len(m.Web.NetworkAllow) > 32 {
			return bad("too many networkAllow")
		}
		for _, h := range m.Web.NetworkAllow {
			if !reHost.MatchString(h) {
				return bad("bad host %q", h)
			}
		}
		switch m.Web.FPSCap {
		case 0, 15, 24, 30, 60:
		default:
			return bad("web.fpsCap must be 15/24/30/60")
		}
	case "shader":
		if m.Shader == nil || m.Shader.Dialect != "shadertoy-glsl" || len(m.Shader.Passes) > 1 {
			return bad("shader: dialect shadertoy-glsl, ≤1 pass")
		}
	}
	if len(m.Properties) > 64 {
		return bad("at most 64 properties")
	}
	keys := map[string]bool{}
	for _, p := range m.Properties {
		if keys[p.Key] {
			return bad("duplicate property key %q", p.Key)
		}
		keys[p.Key] = true
		if err := p.validate(); err != nil {
			return err
		}
	}
	if p := m.Perf; p != nil {
		if p.MaxFPS != 0 && (p.MaxFPS < 1 || p.MaxFPS > 240) {
			return bad("perf.maxFps out of range")
		}
		if p.MinRenderScale != 0 && (p.MinRenderScale < 0.25 || p.MinRenderScale > 1) {
			return bad("perf.minRenderScale out of range")
		}
	}
	if o := m.Origin; o != nil {
		switch o.Kind {
		case "native", "lively", "we":
		default:
			return bad("origin.kind invalid")
		}
	}
	return m.checkEntryExt()
}

func (p Prop) validate() error {
	if !rePropK.MatchString(p.Key) {
		return bad("property key %q invalid", p.Key)
	}
	if len(p.Default) == 0 {
		return bad("property %s: default required", p.Key)
	}
	if err := validI18n(p.Label, "property label", false); err != nil {
		return err
	}
	if p.MaxLength < 0 || p.MaxLength > 2000 {
		return bad("invalid maxLength")
	}
	var d any
	if err := json.Unmarshal(p.Default, &d); err != nil {
		return bad("invalid property default")
	}
	switch p.Type {
	case "slider":
		if p.Min == nil || p.Max == nil {
			return bad("slider %s: min/max required", p.Key)
		}
		if math.IsNaN(*p.Min) || math.IsInf(*p.Min, 0) || math.IsNaN(*p.Max) || math.IsInf(*p.Max, 0) || *p.Min > *p.Max || p.Step != nil && (math.IsNaN(*p.Step) || math.IsInf(*p.Step, 0) || *p.Step <= 0) {
			return bad("invalid slider range/step")
		}
		if _, ok := d.(float64); !ok {
			return bad("slider %s: default must be number", p.Key)
		}
	case "bool":
		if _, ok := d.(bool); !ok {
			return bad("bool %s: default must be boolean", p.Key)
		}
	case "select":
		for _, o := range p.Options {
			var option struct {
				Value json.RawMessage `json:"value"`
				Label I18n            `json:"label"`
			}
			if json.Unmarshal(o, &option) != nil || option.Value == nil {
				return bad("select options require value")
			}
			if err := validI18n(option.Label, "option label", false); err != nil {
				return err
			}
		}
		if len(p.Options) == 0 {
			return bad("select %s: options required", p.Key)
		}
	case "color":
		if s, ok := d.(string); !ok || !reColor.MatchString(s) {
			return bad("color %s: default must be #rrggbb", p.Key)
		}
	case "text":
		if _, ok := d.(string); !ok {
			return bad("text %s: default must be string", p.Key)
		}
	default:
		return bad("property %s: unknown type %q", p.Key, p.Type)
	}
	return p.ValidateValue(p.Default)
}

// 入口扩展名与 type 匹配（10.4 语义校验）。
// MVP 偏差：GIF 按 video 类型直接交给播放器（mpv 原生支持），没有先转成视频（F-08 原文要求转成视频）。
var entryExts = map[string][]string{
	"video":  {".mp4", ".mov", ".m4v", ".webm", ".gif"},
	"image":  {".png", ".jpg", ".jpeg", ".webp", ".bmp"},
	"web":    {".html", ".htm"},
	"shader": {".glsl", ".frag"},
}

func (m *Manifest) checkEntryExt() error {
	ext := strings.ToLower(path.Ext(m.Entry))
	for _, e := range entryExts[m.Type] {
		if e == ext {
			return nil
		}
	}
	return bad("entry extension %q does not match type %s", ext, m.Type)
}

// IsVideoExt 判断扩展名是否是视频（规模限制里视频不受 256 MiB 单文件限制）。
func IsVideoExt(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	for _, e := range entryExts["video"] {
		if e == ext {
			return true
		}
	}
	return false
}

func marshalManifest(m *Manifest) ([]byte, error) { return json.MarshalIndent(m, "", "  ") }
