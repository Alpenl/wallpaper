// Package update 是 F-15 的增量更新与回滚（11 章）。不依赖 MyGo 的更新器：后者要求 mygo build 流水线和公开仓库。
//
// 发布端（cmd/wpupdate）：对一个平台的安装目录生成 manifest.json（版本 + 每个文件的大小和 SHA-256 + 下载 URL），
// 用 Ed25519 私钥签名得到 manifest.json.sig。私钥不进仓库。
//
// 客户端：
//  1. Check 下载清单和签名，用编译进程序的公钥验签，版本比当前新才返回。
//  2. Plan 对比本地文件的 SHA-256，只列出变化或新增的文件（增量）。
//  3. Apply 把这些文件下载到 <dir>/.update/staging 并逐个校验哈希，全部通过才开始替换：
//     旧文件移到 <dir>/.update/rollback/，新文件移到原位置，写 pending.json。替换中途出错立即回滚。
//  4. 新版本启动时调 Boot：pending 存在就累计启动次数，超过 MaxBoots（说明新版本起不来或反复崩溃）自动 Rollback。
//     稳定运行后调 MarkGood 清掉 pending。用户也可以在设置里手动 Rollback（rollback 目录保留到下一次更新）。
//
// Windows 上正在运行的 exe 可以改名但不能覆盖，所以替换一律用“先移走旧文件再移入新文件”。
// macOS .app 的 dir 是 Contents 目录；逐文件替换后签名封印是否仍然有效需要真机验证【推断】。
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
	Feed     string // 清单所在目录的 URL：<Feed>/<platform>/manifest.json
	Pub      ed25519.PublicKey
	Platform string
	Current  string
	Dir      string // 安装目录
	HTTP     *http.Client
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

// Pending 是 pending.json：一次已替换、还没确认的更新。
type Pending struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Boots   int      `json:"boots"`
	Changed []string `json:"changed"` // 替换或新增的文件
	Added   []string `json:"added"`   // 之前不存在（回滚时删除）
	Removed []string `json:"removed"` // 按 Remove 移走的文件
	Good    bool     `json:"good"`
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
	todo := Plan(c.Dir, m)
	var total, done int64
	for _, f := range todo {
		total += f.Size
	}
	staging := c.sd("staging")
	_ = os.RemoveAll(staging)
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
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			return done, err
		}
		done += f.Size
		if progress != nil {
			progress(done, total)
		}
	}
	// 替换（rollback 目录只保留上一版本）
	rb := c.sd("rollback")
	_ = os.RemoveAll(rb)
	p := &Pending{From: c.Current, To: m.Version}
	undo := func() { _ = c.restore(p) }
	for _, f := range todo {
		cur := filepath.Join(c.Dir, filepath.FromSlash(f.Path))
		if _, err := os.Stat(cur); err == nil {
			if err := moveFile(cur, filepath.Join(rb, filepath.FromSlash(f.Path))); err != nil {
				undo()
				return done, err
			}
		} else {
			p.Added = append(p.Added, f.Path)
		}
		p.Changed = append(p.Changed, f.Path)
		if err := moveFile(filepath.Join(staging, filepath.FromSlash(f.Path)), cur); err != nil {
			undo()
			return done, err
		}
	}
	for _, r := range m.Remove {
		cur := filepath.Join(c.Dir, filepath.FromSlash(r))
		if _, err := os.Stat(cur); err == nil {
			if err := moveFile(cur, filepath.Join(rb, filepath.FromSlash(r))); err != nil {
				undo()
				return done, err
			}
			p.Removed = append(p.Removed, r)
		}
	}
	_ = os.RemoveAll(staging)
	return done, c.writePending(p)
}

func (c *Client) writePending(p *Pending) error {
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.MkdirAll(c.sd(), 0o755); err != nil {
		return err
	}
	tmp := c.sd(pendingName + ".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.sd(pendingName))
}

// ReadPending 读取 pending.json（没有时返回 nil）。
func (c *Client) ReadPending() *Pending {
	b, err := os.ReadFile(c.sd(pendingName))
	if err != nil {
		return nil
	}
	var p Pending
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

// Boot 在每次启动时调用：未确认的更新累计启动次数，超过 MaxBoots 自动回滚，返回 rolledBack=true（调用方应提示并重启）。
func (c *Client) Boot() (rolledBack bool, err error) {
	p := c.ReadPending()
	if p == nil || p.Good {
		return false, nil
	}
	p.Boots++
	if p.Boots > MaxBoots {
		return true, c.Rollback()
	}
	return false, c.writePending(p)
}

// MarkGood 确认新版本运行正常（保留 rollback 目录以便手动回滚）。
func (c *Client) MarkGood() error {
	p := c.ReadPending()
	if p == nil || p.Good {
		return nil
	}
	p.Good = true
	return c.writePending(p)
}

// CanRollback 报告是否有可回滚的上一版本，以及它的版本号。
func (c *Client) CanRollback() (string, bool) {
	p := c.ReadPending()
	if p == nil {
		return "", false
	}
	return p.From, true
}

// Rollback 把上一次更新替换的文件还原。
func (c *Client) Rollback() error {
	p := c.ReadPending()
	if p == nil {
		return ErrNothing
	}
	if err := c.restore(p); err != nil {
		return err
	}
	_ = os.Remove(c.sd(pendingName))
	return os.RemoveAll(c.sd("rollback"))
}

func (c *Client) restore(p *Pending) error {
	rb := c.sd("rollback")
	added := map[string]bool{}
	for _, a := range p.Added {
		added[a] = true
	}
	var errs []error
	for _, f := range append(append([]string{}, p.Changed...), p.Removed...) {
		cur := filepath.Join(c.Dir, filepath.FromSlash(f))
		if added[f] {
			if err := os.Remove(cur); err != nil && !os.IsNotExist(err) {
				// Windows 上正在运行的新 exe 删不掉：改名留待下次清理
				_ = os.Rename(cur, cur+".old")
			}
			continue
		}
		old := filepath.Join(rb, filepath.FromSlash(f))
		if _, err := os.Stat(old); err != nil {
			continue
		}
		if _, err := os.Stat(cur); err == nil {
			trash := c.sd("trash", fmt.Sprint(time.Now().UnixNano()), filepath.FromSlash(f))
			if err := moveFile(cur, trash); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := moveFile(old, cur); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Cleanup 删除上一次留下的 trash 和 staging（启动时调用；Windows 上正在运行的旧 exe 只能下次删）。
func (c *Client) Cleanup() {
	_ = os.RemoveAll(c.sd("trash"))
	_ = os.RemoveAll(c.sd("staging"))
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	b, err := os.ReadFile(src) // 跨卷：复制后删除
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		return err
	}
	return os.Remove(src)
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
