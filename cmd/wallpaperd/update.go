package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/ui"
	"wpapp/internal/update"
)

// installDir 是可更新的安装目录：Windows/Linux 为 exe 所在目录；macOS 为 .app/Contents。
func installDir(exe string) string {
	return installDirFor(runtime.GOOS, exe)
}

func installDirFor(goos, exe string) string {
	cleaned := filepath.Clean(exe)
	d := filepath.Dir(cleaned)
	if goos == "darwin" && filepath.Base(d) == "MacOS" {
		contents := filepath.Dir(d)
		if filepath.Base(contents) == "Contents" {
			return contents
		}
	}
	return d
}

func updatePub() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(updatePubKey))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return b
}

func (e *env) updater() *update.Client {
	feed := e.configSnapshot().Update.FeedURL
	return &update.Client{Feed: feed, Pub: updatePub(), Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Current: version, Dir: installDir(e.exe)}
}

// bootUpdate 在启动时处理上一次更新：新版本连续启动失败就回滚并重启旧版本（F-15）。
func bootUpdate(exe string) (restart bool) {
	c := &update.Client{Dir: installDir(exe), Current: version}
	p, readErr := c.ReadPending()
	recovering := readErr == nil && p != nil && (p.State == "applying" || p.State == "rolling_back")
	if err := c.Recover(); err != nil {
		fmt.Fprintln(os.Stderr, "update recovery:", err)
		return false
	}
	rb, err := c.Boot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "update boot:", err)
	}
	c.Cleanup()
	return recovering || rb
}

// relaunchCmd 构造重启新实例的命令。Windows 上设置 CREATE_BREAKAWAY_FROM_JOB，
// 避免旧进程退出关闭 KILL_ON_JOB_CLOSE Job Object 时一并终止新实例。
func relaunchCmd(exe string, args []string) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "WPAPP_RELAUNCH_WAIT=1")
	configureRelaunch(cmd)
	return cmd
}

// relaunch 启动一个新实例。新实例先等旧实例退出、释放单实例锁（WPAPP_RELAUNCH_WAIT）。
func relaunch(exe string) {
	_ = relaunchCmd(exe, os.Args[1:]).Start()
}

// waitRelaunch 在被 relaunch 启动时稍等旧进程退出。
func waitRelaunch() {
	if os.Getenv("WPAPP_RELAUNCH_WAIT") == "1" {
		os.Unsetenv("WPAPP_RELAUNCH_WAIT")
		time.Sleep(2 * time.Second)
	}
}

// updateHooks 把 internal/update 接到设置窗口。
func (e *env) updateHooks(h *ui.Hooks, quit func()) {
	var pending *update.Manifest
	var pendingFeed string
	var mu sync.Mutex
	h.CheckUpdate = func() (*ui.UpdateInfo, error) {
		c := e.updater()
		mu.Lock()
		pending, pendingFeed = nil, ""
		mu.Unlock()
		if c.Pub == nil {
			return nil, errors.New("update: this build has no update public key")
		}
		if c.Feed == "" {
			return nil, errors.New("update: no feed configured")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m, err := c.Check(ctx)
		if errors.Is(err, update.ErrNoUpdate) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		mu.Lock()
		pending, pendingFeed = m, c.Feed
		mu.Unlock()
		var n int64
		for _, f := range update.Plan(c.Dir, m) {
			n += f.Size
		}
		return &ui.UpdateInfo{Version: m.Version, Notes: m.Notes, Size: n}, nil
	}
	h.ApplyUpdate = func(progress func(done, total int64)) error {
		mu.Lock()
		m, feed := pending, pendingFeed
		mu.Unlock()
		if m == nil {
			return errors.New("update: check for updates first")
		}
		client := e.updater()
		if client.Feed != feed {
			return errors.New("update: feed changed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := client.Apply(ctx, m, progress)
		if err == nil {
			e.log.Info("update applied", "to", m.Version)
			mu.Lock()
			pending, pendingFeed = nil, ""
			mu.Unlock()
		}
		return err
	}
	h.CanRollback = func() (string, bool) { return e.updater().CanRollback() }
	h.Rollback = func() error { return e.updater().Rollback() }
	h.Restart = func() {
		relaunchAfterQuit.Store(true)
		quit()
	}
}

var relaunchAfterQuit atomic.Bool

// runUpdateCLI 是 -update-apply / -update-rollback：不启动壁纸，只做更新（真机测试和脚本用）。
func runUpdateCLI(f *flags) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	c := &update.Client{Pub: updatePub(), Platform: runtime.GOOS + "-" + runtime.GOARCH, Current: version, Dir: installDir(exe), Feed: f.feed}
	if c.Feed == "" {
		if p, err := config.DefaultPaths(); err == nil {
			if st, err := config.Open(p); err == nil {
				c.Feed = st.Config.Update.FeedURL
			}
		}
	}
	if f.updRoll {
		if err := c.Rollback(); err != nil {
			fmt.Fprintln(os.Stderr, "rollback:", err)
			return 1
		}
		fmt.Println("rolled back")
		return 0
	}
	if c.Pub == nil {
		fmt.Fprintln(os.Stderr, "update: this build has no update public key")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	m, err := c.Check(ctx)
	if errors.Is(err, update.ErrNoUpdate) {
		fmt.Println("up to date:", version)
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		return 1
	}
	n, err := c.Apply(ctx, m, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	p, err := c.ReadPending()
	if err != nil {
		fmt.Fprintln(os.Stderr, "update pending:", err)
		return 1
	}
	fmt.Printf("updated %s -> %s (%d bytes downloaded, %d files changed)\n", version, m.Version, n, len(p.Changed))
	return 0
}
