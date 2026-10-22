// Package importer 实现本地媒体与安全壁纸包导入（F-11/F-12）：
//   - 普通视频或图片文件：探测 → 判断是否需要转码 → 生成 manifest v0 的包目录 → 首帧截图；
//   - .wpk / manifest 目录：共享 wpk 安全校验；
//   - Lively zip 或目录：video/gif/picture 与纯本地 web；
//   - Wallpaper Engine 项目目录：video 与不要求音频处理的本地 web。
//
// 媒体探测与截图通过 Options 里的函数完成，产品里它们都在 helper 子进程里运行（10.4）。
package importer

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/media"
	"wpapp/internal/transcode"
	"wpapp/internal/wpk"
)

// Options 是导入依赖。
type Options struct {
	ShaderSnapshot  func(packageDir, dst string) error
	Library         string
	Cache           string
	GOOS            string
	Probe           func(path string) (*media.Info, error)
	Snapshot        func(src, dstPNG string) error                   // 可为 nil
	Transcode       func(ctx context.Context, src, dst string) error // 可为 nil（本平台无系统编码器）
	TranscodeExt    string                                           // .mp4/.mov/.m4v; empty preserves the legacy .mov contract
	CanTranscodeGIF bool                                             // only advertise when this encoder accepts GIF sources
	BeforeReplace   func(id string) error
	ConsentNetwork  func(title string, hosts []string) bool // nil means deny; called only for a declared permission
	Lang            string
}

// Result 是导入结果。
type Result struct {
	Item     config.Item
	Warnings []string
}

// ErrUnsupported 表示素材要求未实现的类型或功能（例如可执行程序、音频可视化）。
var ErrUnsupported = errors.New("import: not supported in this version")

// ErrNeedsTranscode 表示本平台播放器不能直接播放，且没有可用的系统编码器。
var ErrNeedsTranscode = errors.New("import: needs transcoding")

// SupportedExts 是可以拖入的扩展名。
var SupportedExts = []string{".mp4", ".mov", ".m4v", ".webm", ".gif", ".png", ".jpg", ".jpeg", ".webp", ".bmp", ".wpk", ".zip", ".glsl", ".frag"}

// Import 按来源分派。
func Import(ctx context.Context, src string, o Options) (*Result, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		switch {
		case exists(filepath.Join(src, "LivelyInfo.json")):
			return importLively(ctx, src, o)
		case exists(filepath.Join(src, "project.json")):
			return importWE(ctx, src, o)
		case exists(filepath.Join(src, "manifest.json")):
			return importFolder(src, o)
		}
		return nil, fmt.Errorf("%w: 目录里没有 LivelyInfo.json 或 project.json", ErrUnsupported)
	}
	switch strings.ToLower(filepath.Ext(src)) {
	case ".glsl", ".frag":
		return importShader(src, o)
	case ".wpk":
		return importWPK(src, o)
	case ".zip":
		if zipHas(src, "LivelyInfo.json") {
			tmp, err := os.MkdirTemp(o.Library, ".lively-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmp)
			if err := wpk.SafeUnzip(src, tmp); err != nil {
				return nil, err
			}
			return importLively(ctx, tmp, o)
		}
		return importWPK(src, o)
	}
	return importFile(ctx, src, o, "native", "", nil)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func zipHas(p, name string) bool {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

func importWPK(src string, o Options) (*Result, error) {
	// Reject unavailable renderers before Extract replaces an existing ID.
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "manifest.json") {
			rd, e := f.Open()
			if e != nil {
				zr.Close()
				return nil, e
			}
			b, e := io.ReadAll(io.LimitReader(rd, wpk.MaxManifest+1))
			rd.Close()
			if e != nil {
				zr.Close()
				return nil, e
			}
			m, e := wpk.Parse(b)
			if e != nil {
				zr.Close()
				return nil, e
			}
			if e := supportedManifest(m); e != nil {
				zr.Close()
				return nil, e
			}
			break
		}
	}
	zr.Close()
	if err := validateShaderArchive(src); err != nil {
		return nil, err
	}
	granted := false
	r, err := wpk.ExtractChecked(src, o.Library, func(m *wpk.Manifest) error {
		if err := supportedManifest(m); err != nil {
			return err
		}
		if m.Type == "web" {
			for _, p := range m.Web.Permissions {
				if p == "network" && o.ConsentNetwork != nil {
					granted = o.ConsentNetwork(m.Title.Pick(o.Lang), append([]string(nil), m.Web.NetworkAllow...))
				}
			}
		}
		if o.BeforeReplace != nil {
			return o.BeforeReplace(m.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	m := r.Manifest
	if e := supportedManifest(m); e != nil {
		os.RemoveAll(r.Dir)
		return nil, e
	}
	it := config.Item{ID: m.ID, Dir: r.Dir, Schema: *m.Schema, Origin: "native", Title: m.Title.Pick(o.Lang), Type: m.Type,
		Entry: m.Entry, Transcode: "none", AddedAt: time.Now().UTC()}
	if m.Origin != nil {
		it.Origin = m.Origin.Kind
	}
	if m.Video != nil {
		it.Codec = m.Video.Codec
	}
	applyMetadata(&it, m)
	if m.Type == "shader" {
		preview := filepath.Join(it.Dir, "preview.png")
		if o.ShaderSnapshot == nil || o.ShaderSnapshot(it.Dir, preview) != nil {
			b, _ := os.ReadFile(filepath.Join(it.Dir, filepath.FromSlash(m.Entry)))
			if e := shaderPlaceholder(preview, b); e != nil {
				return nil, e
			}
		}
	}
	if m.Type == "web" || m.Type == "shader" {
		it.Web = m.Web
		it.PropertyDefs = m.Properties
		it.NetworkGranted = granted
		for _, name := range []string{"preview.png", "preview.webp"} {
			if exists(filepath.Join(it.Dir, name)) {
				it.Snapshot = filepath.Join(it.Dir, name)
				break
			}
		}
		return &Result{Item: it}, nil
	}
	res := &Result{Item: it}
	snapshotItem(o, &res.Item, filepath.Join(r.Dir, filepath.FromSlash(m.Entry)), res)
	return res, nil
}

// NewID 生成小写 UUID v4。
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// safeName 生成 content/ 下的文件名（只保留安全字符，保留扩展名）。
func safeName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	var sb strings.Builder
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			if r > 127 {
				sb.WriteRune(r)
			} else {
				sb.WriteByte('_')
			}
		}
	}
	s := strings.Trim(sb.String(), "_. ")
	if s == "" || wpk.CheckPath("content/"+s+ext) != nil {
		s = "media"
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s + ext
}

// importFile 把一个媒体文件做成包目录 library/<id>/。
func importFile(ctx context.Context, src string, o Options, origin, title string, extra func(*wpk.Manifest)) (*Result, error) {
	if o.Probe == nil {
		return nil, errors.New("import: no prober")
	}
	info, err := o.Probe(src)
	if err != nil {
		return nil, fmt.Errorf("import: 无法识别的文件: %w", err)
	}
	plan := transcode.Decide(info, o.GOOS)
	if info.Codec == "gif" && o.Transcode != nil && o.CanTranscodeGIF {
		plan.Action = transcode.Required
		plan.Notes = append(plan.Notes, "GIF 将转换为视频；未提供编码器时支持的平台仍可直接播放 GIF")
	}
	res := &Result{Warnings: plan.Notes}
	id := NewID()
	tmp, err := os.MkdirTemp(o.Library, ".import-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	if err := os.MkdirAll(filepath.Join(tmp, "content"), 0o755); err != nil {
		return nil, err
	}
	name := safeName(src)
	dst := filepath.Join(tmp, "content", name)
	status := "none"
	if plan.Action == transcode.Recommend {
		status = "recommended"
	}
	if plan.Action == transcode.Required {
		if o.Transcode == nil {
			return nil, fmt.Errorf("%w: %s", ErrNeedsTranscode, strings.Join(plan.Notes, "；"))
		}
		ext := strings.ToLower(o.TranscodeExt)
		if ext == "" {
			ext = ".mov"
		}
		if ext != ".mov" && ext != ".mp4" && ext != ".m4v" {
			return nil, fmt.Errorf("import: invalid transcode extension %q", ext)
		}
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ext
		dst = filepath.Join(tmp, "content", name)
		if err := o.Transcode(ctx, src, dst); err != nil {
			return nil, err
		}
		ni, err := o.Probe(dst)
		if err != nil {
			return nil, fmt.Errorf("import: 转码产物验证失败: %w", err)
		}
		if ni == nil || ni.Kind != "video" || (ni.Codec != "h264" && ni.Codec != "hevc") {
			return nil, errors.New("import: 转码产物不是可播放的 H.264/HEVC 视频")
		}
		info = ni
		status = "done"
	} else if err := copyFile(src, dst); err != nil {
		return nil, err
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	zero := 0
	m := &wpk.Manifest{Format: "wpk", Schema: &zero, ID: id, Version: 1, Type: info.Kind, Entry: "content/" + name,
		Title: wpk.I18n{"en": title}}
	if info.Kind == "video" {
		codec := info.Codec
		if codec == "gif" {
			codec = "other"
		}
		loop := true
		m.Video = &wpk.Video{Loop: &loop, DurationSec: info.DurationSec, W: info.W, H: info.H, FPS: info.FPS, Codec: codec, HasAudio: info.HasAudio}
		if m.Video.W != 0 && m.Video.W < 16 || m.Video.H != 0 && m.Video.H < 16 {
			m.Video.W, m.Video.H = 0, 0
		}
	}
	if origin != "native" {
		m.Origin = &wpk.Origin{Kind: origin, Ref: filepath.Base(src)}
	}
	if extra != nil {
		extra(m)
	}
	if err := wpk.WriteManifest(tmp, m); err != nil {
		return nil, err
	}
	final := filepath.Join(o.Library, id)
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	ok = true
	res.Item = config.Item{ID: id, Dir: final, Schema: 0, Origin: origin, Title: title, Type: info.Kind, Entry: m.Entry,
		Codec: info.Codec, Transcode: status, Note: strings.Join(plan.Notes, "；"), AddedAt: time.Now().UTC(), Width: info.W, Height: info.H, DurationSec: info.DurationSec}
	applyMetadata(&res.Item, m)
	snapshotItem(o, &res.Item, filepath.Join(final, "content", name), res)
	return res, nil
}

// snapshotItem 生成 cache/<id>/first.png 和包里的 preview.png（失败只记警告）。
func snapshotItem(o Options, it *config.Item, media string, res *Result) {
	defer func() { it.Colors = PreviewColors(it.Snapshot) }()
	if st, err := os.Stat(media); err == nil {
		it.SizeBytes = st.Size()
	}
	dir := filepath.Join(o.Cache, it.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Warnings = append(res.Warnings, "截图目录: "+err.Error())
		return
	}
	if it.Type == "image" {
		p := filepath.Join(dir, "first"+strings.ToLower(filepath.Ext(media)))
		if err := replacePreview(media, p); err != nil {
			res.Warnings = append(res.Warnings, "图片预览失败: "+err.Error())
			return
		}
		it.Snapshot = p
		return
	}
	if o.Snapshot == nil {
		res.Warnings = append(res.Warnings, "本平台没有截图器：系统壁纸兜底不可用")
		return
	}
	p := filepath.Join(dir, "first.png")
	if err := o.Snapshot(media, p); err != nil {
		res.Warnings = append(res.Warnings, "首帧截图失败: "+err.Error())
		return
	}
	it.Snapshot = p
	if !exists(filepath.Join(it.Dir, "preview.png")) && !exists(filepath.Join(it.Dir, "preview.webp")) {
		_ = copyFile(p, filepath.Join(it.Dir, "preview.png"))
	}
}

// replacePreview stages the complete image beside its destination. Reimporting
// a package with the same ID must replace its old preview, not silently retain
// the old bytes because copyFile intentionally refuses existing destinations.
func replacePreview(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".preview-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- Lively ----

type livelyInfo struct {
	Title          string          `json:"Title"`
	Desc           string          `json:"Desc"`
	Author         string          `json:"Author"`
	License        string          `json:"License"`
	Type           json.RawMessage `json:"Type"`
	FileName       string          `json:"FileName"`
	IsAbsolutePath bool            `json:"IsAbsolutePath"`
	Tags           []string        `json:"Tags"`
}

// livelyTypes 取自 Lively 源码 Lively.Models/Enums/WallpaperType.cs 的枚举顺序（JSON 里可能是整数或名字）。
var livelyTypes = []string{"app", "web", "webaudio", "url", "bizhawk", "unity", "godot", "video", "gif", "unityaudio", "videostream", "picture", "heic"}

func (l livelyInfo) typeName() string {
	var n int
	if json.Unmarshal(l.Type, &n) == nil {
		if n >= 0 && n < len(livelyTypes) {
			return livelyTypes[n]
		}
		return "unknown"
	}
	var s string
	_ = json.Unmarshal(l.Type, &s)
	return strings.ToLower(s)
}

func readJSON(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return err
	}
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	return json.Unmarshal(b, v)
}

// within 解析包内相对路径并确保不越界。
func within(dir, rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	if err := wpk.CheckPath(rel); err != nil {
		return "", fmt.Errorf("import: 包内路径不安全: %w", err)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

func importLively(ctx context.Context, dir string, o Options) (*Result, error) {
	var li livelyInfo
	if err := readJSON(filepath.Join(dir, "LivelyInfo.json"), &li); err != nil {
		return nil, fmt.Errorf("import: LivelyInfo.json: %w", err)
	}
	t := li.typeName()
	if t == "web" {
		if li.IsAbsolutePath {
			return nil, fmt.Errorf("%w: web entry must be inside its package", ErrUnsupported)
		}
		return importWeb(dir, li.FileName, li.Title, "lively", o)
	}
	switch t {
	case "video", "gif", "picture":
	default:
		return nil, fmt.Errorf("%w: Lively 类型 %s（MVP 只导入视频、GIF 和图片）", ErrUnsupported, t)
	}
	var src string
	if li.IsAbsolutePath {
		src = li.FileName // 10.7：用户自己打开的外部文件，直接当作素材
	} else {
		var err error
		if src, err = within(dir, li.FileName); err != nil {
			return nil, err
		}
	}
	return importFile(ctx, src, o, "lively", li.Title, func(m *wpk.Manifest) {
		if li.Desc != "" {
			m.Description = wpk.I18n{"en": trunc(li.Desc, 2000)}
		}
		if li.Author != "" {
			m.Author = &wpk.Author{Name: trunc(li.Author, 128)}
		}
		if li.License != "" && len(li.License) <= 64 {
			m.License = li.License
		}
		for _, tg := range li.Tags {
			if len(m.Tags) < 16 {
				m.Tags = append(m.Tags, trunc(tg, 32))
			}
		}
	})
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---- Wallpaper Engine ----

type weProject struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	File  string `json:"file"`
}

func importWE(ctx context.Context, dir string, o Options) (*Result, error) {
	var p weProject
	if err := readJSON(filepath.Join(dir, "project.json"), &p); err != nil {
		return nil, fmt.Errorf("import: project.json: %w", err)
	}
	if strings.EqualFold(p.Type, "web") {
		return importWeb(dir, p.File, p.Title, "we", o)
	}
	if !strings.EqualFold(p.Type, "video") {
		return nil, fmt.Errorf("%w: Wallpaper Engine 类型 %s（MVP 只导入 video）", ErrUnsupported, p.Type)
	}
	src, err := within(dir, p.File)
	if err != nil {
		return nil, err
	}
	return importFile(ctx, src, o, "we", p.Title, nil)
}

// applyMetadata retains searchable package metadata without activating scripts.
func applyMetadata(it *config.Item, m *wpk.Manifest) {
	if m.Author != nil {
		it.Author = m.Author.Name
	}
	it.Description = m.Description.Pick("zh-CN")
	it.Tags = append([]string(nil), m.Tags...)
	if m.Video != nil {
		it.Width, it.Height, it.DurationSec = m.Video.W, m.Video.H, m.Video.DurationSec
	}
}
