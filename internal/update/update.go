// Package update 是 F-15 的增量更新与回滚（11 章）。不依赖 MyGo 的更新器：后者要求 mygo build 流水线和公开仓库。
//
// 发布端（cmd/wpupdate）：对一个平台的安装目录生成 manifest.json（版本 + 每个文件的大小和 SHA-256 + 下载 URL），
// 用 Ed25519 私钥签名得到 manifest.json.sig。私钥不进仓库。
//
// 客户端：
//  1. Check 下载清单和签名，用编译进程序的公钥验签，版本比当前新才返回。
//  2. Plan 对比本地文件的 SHA-256，只列出变化或新增的文件（增量）。
//  3. Apply 把这些文件下载到 <dir>/.update/staging 并逐个校验哈希，全部通过才开始替换：
//     先持久化 applying 计划，再逐个备份和替换；中断由 Recover 回滚。
//  4. 新版本启动时调 Boot：pending 存在就累计启动次数，超过 MaxBoots（说明新版本起不来或反复崩溃）自动 Rollback。
//     稳定运行后调 MarkGood 标记 good。用户也可以在设置里手动 Rollback（rollback 目录保留到下一次更新）。
//
// Windows 上正在运行的 exe 可以改名但不能覆盖，所以替换一律用“先移走旧文件再移入新文件”。
// macOS .app 的 dir 是 Contents 目录；逐文件替换后签名封印是否仍然有效需要真机验证【推断】。
// 主程序两次 rename 之间若断电，可能没有可启动的程序；用户必须重新运行安装器。
package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// File 是清单里的一个文件。Path 用 / 分隔、相对安装目录。
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url,omitempty"` // 空 = 清单所在目录 + Path
}

// Manifest 是 manifest.json。
type Manifest struct {
	Schema   int       `json:"schema"`
	Version  string    `json:"version"`
	Platform string    `json:"platform"` // windows-amd64 …
	Notes    string    `json:"notes,omitempty"`
	Date     time.Time `json:"date"`
	Files    []File    `json:"files"`
	// Remove 列出新版本不再需要的旧文件（回滚时会还原）。
	Remove []string `json:"remove,omitempty"`
}

const (
	stateDir    = ".update"
	MaxBoots    = 2
	pendingName = "pending.json"
)

var (
	ErrSignature = errors.New("update: bad signature")
	ErrNoUpdate  = errors.New("update: already up to date")
	ErrNothing   = errors.New("update: nothing to roll back")
)

// Sign 对清单字节签名，返回 base64 签名。
func Sign(priv ed25519.PrivateKey, manifest []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest))
}

// Verify 验签并解析清单。
func Verify(pub ed25519.PublicKey, manifest []byte, sig string) (*Manifest, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, manifest, s) {
		return nil, ErrSignature
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, err
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	for _, f := range m.Files {
		if !safeRel(f.Path) {
			return nil, fmt.Errorf("update: unsafe path %q", f.Path)
		}
	}
	for _, p := range m.Remove {
		if !safeRel(p) {
			return nil, fmt.Errorf("update: unsafe path %q", p)
		}
	}
	return &m, nil
}

func safeRel(p string) bool {
	if p == "" || strings.Contains(p, `\`) || path.IsAbs(p) || strings.HasPrefix(p, stateDir) {
		return false
	}
	c := path.Clean(p)
	return c == p && c != ".." && !strings.HasPrefix(c, "../")
}

// Client 是更新客户端。
type Client struct {
	Feed       string // 清单所在目录的 URL：<Feed>/<platform>/manifest.json
	Pub        ed25519.PublicKey
	Platform   string
	Current    string
	Dir        string // 安装目录
	HTTP       *http.Client
	rename     func(string, string) error
	writeState func(*Pending) error
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (c *Client) base() string { return strings.TrimRight(c.Feed, "/") + "/" + c.Platform + "/" }

func (c *Client) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if pu.Scheme != "https" && !(pu.Scheme == "http" && (pu.Hostname() == "127.0.0.1" || pu.Hostname() == "localhost")) {
		return nil, fmt.Errorf("update: refusing non-https url %s", pu.Redacted())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("update: GET %s: %s", pu.Path, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update: %s too large", pu.Path)
	}
	return b, nil
}

// Check 返回比当前新的已验签清单；没有更新时返回 ErrNoUpdate。
func (c *Client) Check(ctx context.Context) (*Manifest, error) {
	if c.Current == "dev" {
		return nil, ErrNoUpdate
	}
	if _, err := parseVersion(c.Current); err != nil {
		return nil, err
	}
	if c.Feed == "" {
		return nil, errors.New("update: no feed configured")
	}
	mb, err := c.get(ctx, c.base()+"manifest.json", 4<<20)
	if err != nil {
		return nil, err
	}
	sig, err := c.get(ctx, c.base()+"manifest.json.sig", 4<<10)
	if err != nil {
		return nil, err
	}
	m, err := Verify(c.Pub, mb, string(sig))
	if err != nil {
		return nil, err
	}
	if m.Platform != c.Platform {
		return nil, fmt.Errorf("update: manifest for %s, want %s", m.Platform, c.Platform)
	}
	if !Newer(m.Version, c.Current) {
		return nil, ErrNoUpdate
	}
	return m, nil
}

// HashFile 返回文件的 SHA-256（十六进制）。
func HashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// Plan 返回需要下载的文件（本地缺失或哈希不同）。
func Plan(dir string, m *Manifest) []File {
	var out []File
	for _, f := range m.Files {
		h, n, err := HashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || n != f.Size || !strings.EqualFold(h, f.SHA256) {
			out = append(out, f)
		}
	}
	return out
}

// PlanEntry records whether a path existed before any replacement.
type PlanEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

// Pending is the durable update intent and stable-boot state.
type Pending struct {
	State   string      `json:"state"`
	Plan    []PlanEntry `json:"plan"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Boots   int         `json:"boots"`
	Changed []string    `json:"changed"` // 替换或新增的文件
	Added   []string    `json:"added"`   // 之前不存在（回滚时删除）
	Removed []string    `json:"removed"` // 按 Remove 移走的文件
	Good    bool        `json:"good"`
}

func (c *Client) sd(parts ...string) string {
	return filepath.Join(append([]string{c.Dir, stateDir}, parts...)...)
}

// Apply 增量下载并替换。progress 可为 nil。返回下载的字节数。
func (c *Client) Apply(ctx context.Context, m *Manifest, progress func(done, total int64)) (int64, error) {
	if err := validateManifest(m); err != nil {
		return 0, err
	}
	if c.Current == "dev" {
		return 0, ErrNoUpdate
	}
	if _, err := parseVersion(c.Current); err != nil {
		return 0, err
	}
	if !Newer(m.Version, c.Current) {
		return 0, ErrNoUpdate
	}
	previous, err := c.ReadPending()
	if err != nil {
		return 0, fmt.Errorf("update: refusing update: %w", err)
	}
	if previous != nil && previous.State != "good" {
		return 0, fmt.Errorf("update: unfinished transaction (%s)", previous.State)
	}
	seen := map[string]bool{}
	pathsToValidate := append([]string{}, m.Remove...)
	for _, f := range m.Files {
		pathsToValidate = append(pathsToValidate, f.Path)
	}
	for _, rel := range pathsToValidate {
		if !safeRel(rel) || seen[rel] {
			return 0, fmt.Errorf("update: invalid or duplicate path %q", rel)
		}
		seen[rel] = true
	}
	todo := Plan(c.Dir, m)
	var total, done int64
	for _, f := range todo {
		total += f.Size
	}
	staging := c.sd("staging")
	if err := os.RemoveAll(staging); err != nil {
		return done, err
	}
	for _, f := range todo {
		u := f.URL
		if u == "" {
			u = c.base() + f.Path
		}
		b, err := c.get(ctx, u, f.Size)
		if err != nil {
			return done, err
		}
		sum := sha256.Sum256(b)
		if int64(len(b)) != f.Size || !strings.EqualFold(hex.EncodeToString(sum[:]), f.SHA256) {
			return done, fmt.Errorf("update: hash mismatch for %s", f.Path)
		}
		dst := filepath.Join(staging, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return done, err
		}
		if err := writeSynced(dst, b, 0o755); err != nil {
			return done, err
		}
		done += f.Size
		if progress != nil {
			progress(done, total)
		}
	}
	// 先记录全部路径的原始存在状态，再允许第一次 rename。
	p := &Pending{From: c.Current, To: m.Version, State: "applying", Plan: []PlanEntry{}}
	changed := map[string]bool{}
	for _, f := range todo {
		changed[f.Path] = true
		p.Changed = append(p.Changed, f.Path)
	}
	paths := append(append([]string{}, p.Changed...), m.Remove...)
	for _, rel := range paths {
		exists, err := fileExists(filepath.Join(c.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return done, err
		}
		p.Plan = append(p.Plan, PlanEntry{Path: rel, Existed: exists})
		if changed[rel] && !exists {
			p.Added = append(p.Added, rel)
		}
		if !changed[rel] && exists {
			p.Removed = append(p.Removed, rel)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return done, err
	}
	isMain := func(rel string) bool {
		return filepath.Clean(filepath.Join(c.Dir, filepath.FromSlash(rel))) == filepath.Clean(exe) ||
			rel == "wallpaperd" || rel == "wallpaperd.exe" || rel == "MacOS/wallpaperd"
	}
	sort.SliceStable(p.Plan, func(i, j int) bool { return !isMain(p.Plan[i].Path) && isMain(p.Plan[j].Path) })
	if err := os.RemoveAll(c.sd("rollback")); err != nil {
		return done, err
	}
	if err := c.writePending(p); err != nil {
		return done, err
	}
	for _, step := range p.Plan {
		cur := filepath.Join(c.Dir, filepath.FromSlash(step.Path))
		if step.Existed {
			if err := c.moveFile(cur, c.sd("rollback", filepath.FromSlash(step.Path))); err != nil {
				return done, err
			}
		}
		if changed[step.Path] {
			if err := c.moveFile(filepath.Join(staging, filepath.FromSlash(step.Path)), cur); err != nil {
				return done, err
			}
		}
	}
	p.State = "awaiting"
	return done, c.writePending(p)
}

func (c *Client) writePending(p *Pending) error {
	if c.writeState != nil {
		return c.writeState(p)
	}
	return c.persistPending(p)
}

func (c *Client) persistPending(p *Pending) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.sd(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(c.sd(pendingName+".tmp"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	err = errors.Join(werr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), c.sd(pendingName)); err != nil {
		return err
	}
	return c.syncParents(c.sd())
}

// ReadPending distinguishes absent records from unreadable or corrupt records.
func (c *Client) ReadPending() (*Pending, error) {
	b, err := os.ReadFile(c.sd(pendingName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("update: corrupt pending: %w", err)
	}
	if p.From == "" || p.To == "" {
		return nil, errors.New("update: pending missing versions")
	}
	if p.State == "" {
		p.Plan = []PlanEntry{}
		p.State = "awaiting"
		if p.Good {
			p.State = "good"
		}
		added := map[string]bool{}
		for _, rel := range p.Added {
			added[rel] = true
		}
		for _, rel := range append(append([]string{}, p.Changed...), p.Removed...) {
			p.Plan = append(p.Plan, PlanEntry{Path: rel, Existed: !added[rel]})
		}
	} else if p.Plan == nil {
		return nil, errors.New("update: pending missing plan")
	}
	switch p.State {
	case "applying", "awaiting", "good", "rolling_back":
	default:
		return nil, fmt.Errorf("update: invalid pending state %q", p.State)
	}
	seen := map[string]bool{}
	for _, step := range p.Plan {
		if !safeRel(step.Path) || seen[step.Path] {
			return nil, fmt.Errorf("update: invalid pending path %q", step.Path)
		}
		seen[step.Path] = true
	}
	return &p, nil
}

// Recover rolls back an interrupted transaction before Boot and Cleanup.
func (c *Client) Recover() error {
	p, err := c.ReadPending()
	if err != nil || p == nil {
		return err
	}
	if p.State != "applying" && p.State != "rolling_back" {
		return nil
	}
	return c.rollback(p)
}

// Boot counts unconfirmed starts and rolls back after MaxBoots.
func (c *Client) Boot() (bool, error) {
	p, err := c.ReadPending()
	if err != nil || p == nil {
		return false, err
	}
	if p.State == "good" {
		return false, nil
	}
	if p.State != "awaiting" {
		return false, errors.New("update: recovery required before boot")
	}
	p.Boots++
	if p.Boots > MaxBoots {
		err := c.Rollback()
		return err == nil, err
	}
	return false, c.writePending(p)
}

// MarkGood is the sole stable-boot confirmation.
func (c *Client) MarkGood() error {
	p, err := c.ReadPending()
	if err != nil || p == nil {
		return err
	}
	if p.State == "good" {
		return nil
	}
	if p.State != "awaiting" {
		return errors.New("update: cannot confirm unfinished transaction")
	}
	p.State, p.Good = "good", true
	return c.writePending(p)
}

func (c *Client) CanRollback() (string, bool) {
	p, err := c.ReadPending()
	if err != nil || p == nil {
		return "", false
	}
	return p.From, true
}

func (c *Client) Rollback() error {
	p, err := c.ReadPending()
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNothing
	}
	return c.rollback(p)
}

func (c *Client) rollback(p *Pending) error {
	p.State, p.Good = "rolling_back", false
	if err := c.writePending(p); err != nil {
		return err
	}
	if err := c.restore(p); err != nil {
		return err
	}
	if err := os.RemoveAll(c.sd("rollback")); err != nil {
		return err
	}
	if err := c.syncParents(c.sd()); err != nil {
		return err
	}
	if err := os.Remove(c.sd(pendingName)); err != nil {
		return err
	}
	return c.syncParents(c.sd())
}

func fileExists(name string) (bool, error) {
	_, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) restore(p *Pending) error {
	var errs []error
	for _, step := range p.Plan {
		cur := filepath.Join(c.Dir, filepath.FromSlash(step.Path))
		old := c.sd("rollback", filepath.FromSlash(step.Path))
		exists, err := fileExists(cur)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if step.Existed {
			backup, err := fileExists(old)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !backup {
				// 未备份或已经还原；两边都不存在则不是可忽略的恢复步骤。
				if !exists {
					errs = append(errs, fmt.Errorf("update: missing original and backup for %s", step.Path))
				}
				continue
			}
		}
		if exists {
			// Rename also handles a running executable on Windows, without copy/delete.
			trash := c.sd("trash", fmt.Sprint(time.Now().UnixNano()), filepath.FromSlash(step.Path)+".old")
			if err := c.moveFile(cur, trash); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if step.Existed {
			if err := c.moveFile(old, cur); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Cleanup removes expendable staging and trash after recovery.
func (c *Client) Cleanup() {
	_ = os.RemoveAll(c.sd("trash"))
	_ = os.RemoveAll(c.sd("staging"))
}

func (c *Client) moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rename := c.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(src, dst); err != nil {
		return err
	}
	return errors.Join(c.syncParents(filepath.Dir(src)), c.syncParents(filepath.Dir(dst)))
}

// Sync new directory entries as well as the leaf directory after each rename.
func (c *Client) syncParents(dir string) error {
	var errs []error
	for {
		errs = append(errs, syncDir(dir))
		if filepath.Clean(dir) == filepath.Clean(c.Dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return errors.Join(errs...)
}

func writeSynced(name string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	return errors.Join(werr, f.Sync(), f.Close())
}

// Windows does not support syncing directory handles through os.File.Sync.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// BuildManifest 扫描 dir 生成清单（发布端使用）。
func BuildManifest(dir, version, platform, notes string) (*Manifest, error) {
	if _, err := parseVersion(version); err != nil {
		return nil, err
	}
	m := &Manifest{Schema: 1, Version: version, Platform: platform, Notes: notes, Date: time.Now().UTC().Truncate(time.Second)}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == stateDir {
				return filepath.SkipDir
			}
			return nil
		}
		h, n, err := HashFile(p)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, File{Path: rel, Size: n, SHA256: h})
		return nil
	})
	return m, err
}

// Encode 返回清单的规范 JSON（签名针对这些字节）。
func (m *Manifest) Encode() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	_ = enc.Encode(m)
	return b.Bytes()
}
