// wpupdate 是 F-15 增量更新的发布端工具（internal/update）。私钥只放在发布机或 CI 密钥里，不进仓库。
//
//	wpupdate keygen -o keys/                       生成 update.key（私钥，base64）和 update.pub（公钥，base64）
//	wpupdate manifest -dir dist/windows-amd64 -version 0.2.0 -platform windows-amd64 -key keys/update.key -out feed/
//	    → feed/windows-amd64/manifest.json + manifest.json.sig + 全部文件（上传到 HTTPS 静态目录即可）
//	wpupdate verify -feed feed/ -platform windows-amd64 -pub keys/update.pub
//
// 客户端的公钥在构建时注入：-ldflags "-X main.updatePubKey=<update.pub 内容>"，更新源在 config.json 的 update.feedURL。
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"wpapp/internal/config"
	"wpapp/internal/telemetry"
	"wpapp/internal/update"
)

func main() {
	if paths, err := config.DefaultPaths(); err != nil {
		fmt.Fprintln(os.Stderr, "crash output:", err)
	} else if err := telemetry.InstallCrashOutput(paths.Logs, "wpupdate"); err != nil {
		fmt.Fprintln(os.Stderr, "crash output:", err)
	}
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wpupdate:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wpupdate keygen|manifest|verify [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("o", ".", "输出目录")
	fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "update.key"), []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, "update.pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644)
}

func readKey(p string, size int) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != size {
		return nil, fmt.Errorf("bad key %s", p)
	}
	return k, nil
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	dir := fs.String("dir", "", "一个平台的安装目录内容（例如 dist/windows-amd64）")
	ver := fs.String("version", "", "版本号")
	plat := fs.String("platform", "", "windows-amd64 | windows-arm64 | darwin-arm64 | …")
	key := fs.String("key", "", "update.key")
	out := fs.String("out", "feed", "输出目录")
	notes := fs.String("notes", "", "更新说明")
	fs.Parse(args)
	if *dir == "" || *ver == "" || *plat == "" || *key == "" {
		return fmt.Errorf("-dir -version -platform -key are required")
	}
	priv, err := readKey(*key, ed25519.PrivateKeySize)
	if err != nil {
		return err
	}
	m, err := update.BuildManifest(*dir, *ver, *plat, *notes)
	if err != nil {
		return err
	}
	dst := filepath.Join(*out, *plat)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := copyFile(filepath.Join(*dir, filepath.FromSlash(f.Path)), filepath.Join(dst, filepath.FromSlash(f.Path))); err != nil {
			return err
		}
	}
	mb := m.Encode()
	if err := os.WriteFile(filepath.Join(dst, "manifest.json"), mb, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, "manifest.json.sig"), []byte(update.Sign(ed25519.PrivateKey(priv), mb)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d files, version %s\n", dst, len(m.Files), m.Version)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	feed := fs.String("feed", "feed", "本地 feed 目录")
	plat := fs.String("platform", "", "平台")
	pubp := fs.String("pub", "", "update.pub")
	fs.Parse(args)
	pub, err := readKey(*pubp, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	d := filepath.Join(*feed, *plat)
	mb, err := os.ReadFile(filepath.Join(d, "manifest.json"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(d, "manifest.json.sig"))
	if err != nil {
		return err
	}
	m, err := update.Verify(ed25519.PublicKey(pub), mb, string(sig))
	if err != nil {
		return err
	}
	if bad := update.Plan(d, m); len(bad) > 0 {
		return fmt.Errorf("%d files do not match the manifest", len(bad))
	}
	fmt.Printf("OK %s %s (%d files)\n", m.Platform, m.Version, len(m.Files))
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
