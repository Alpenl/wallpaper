package telemetry

import (
	"wpapp/internal/buildinfo"

	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 绝对路径（Windows 盘符、UNC、Z:\ 之类；Unix 以 / 开头且至少两段）。16.4 规则：诊断包里不出现素材路径。
var pathRe = regexp.MustCompile(`(?i)([a-z]:[\\/]|\\\\|/(?:home|users|tmp|var|mnt|media|private|volumes|library|opt|root)/)[^\s"',;\]\)]+`)

// windowTitleRe 防御性去掉形如 "title":"…" 的字段（壁纸标题可能含个人信息）。
var titleRe = regexp.MustCompile(`"(title|name|file|path|dir|snapshot|entry)":"(?:[^"\\]|\\.)*"`)

// Redact 按 16.4 规则清洗一行：路径 → <path:hash8>，标题类字段 → <redacted>。
func Redact(s string) string {
	s = pathRe.ReplaceAllStringFunc(s, func(p string) string { return "<path:" + HashPath(p) + ">" })
	s = titleRe.ReplaceAllStringFunc(s, func(m string) string {
		k := m[1 : strings.Index(m[1:], `"`)+1]
		return `"` + k + `":"<redacted>"`
	})
	return s
}

// ExportDiagnostics 把日志目录（清洗后）和指定的配置文件打成 zip（F-16）。
// mpv 日志只保留警告/错误行。素材库（library.json）不导出。
func ExportDiagnostics(logsDir string, configFiles []string, dst string, build buildinfo.Info) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	w, _ := z.Create("README.txt")
	fmt.Fprintf(w, "wallpaperd %s\nexported %s\n路径已替换为 sha256 前缀，标题类字段已去除（16.4）。\n", build.String(), time.Now().Format(time.RFC3339))
	ents, _ := os.ReadDir(logsDir)
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if err := addScrubbed(z, filepath.Join(logsDir, n), "logs/"+n, strings.HasPrefix(n, "mpv-") || strings.HasPrefix(n, "mfhost-")); err != nil {
			return err
		}
	}
	for _, p := range configFiles {
		if _, err := os.Stat(p); err == nil {
			if err := addScrubbed(z, p, "config/"+filepath.Base(p), false); err != nil {
				return err
			}
		}
	}
	return z.Close()
}

func addScrubbed(z *zip.Writer, src, name string, onlyWarn bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := z.Create(name)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(io.LimitReader(in, 16<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if onlyWarn && !strings.Contains(l, "][w][") && !strings.Contains(l, "][e][") && !strings.Contains(l, "][f][") {
			continue
		}
		fmt.Fprintln(w, Redact(Scrub(l)))
	}
	return sc.Err()
}
