// Package wpk 实现 10 章的壁纸包：manifest v0 解析与校验、安全解压。
// schema 里 entry 的正则用了前瞻断言（RE2 不支持），所以路径规则全部在代码里实现（10.4）。
package wpk

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Type        string   `json:"type"` // video | image
	Entry       string   `json:"entry"`
	Title       I18n     `json:"title"`
	Description I18n     `json:"description,omitempty"`
	Author      *Author  `json:"author,omitempty"`
	License     string   `json:"license,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Video       *Video   `json:"video,omitempty"`
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
	case "video", "image":
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

// 入口扩展名与 type 匹配（10.4 语义校验）。
// MVP 偏差：GIF 按 video 类型直接交给播放器（mpv 原生支持），没有先转成视频（F-08 原文要求转成视频）。
var entryExts = map[string][]string{
	"video": {".mp4", ".mov", ".m4v", ".webm", ".gif"},
	"image": {".png", ".jpg", ".jpeg", ".webp", ".bmp"},
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
