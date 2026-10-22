package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"wpapp/internal/desktop"
	"wpapp/internal/ui"
	"wpapp/internal/visibility"
)

// emitter 由能接收外部信号的宿主实现（mac、linux 用 mygo.Power 补锁屏/睡眠）。
type emitter interface{ Emit(desktop.Event) }

func runUI(f *flags) int {
	mygo.App.SetName("Wallpaper")
	mygo.App.SetVersion(version)
	waitRelaunch()
	if !mygo.App.RequestSingleInstanceLock() {
		return 0 // 已有实例：参数已交给它（它会打开设置窗口）
	}
	if exe, err := os.Executable(); err == nil && bootUpdate(exe) {
		relaunch(exe) // 新版本连续启动失败，已回滚：启动旧版本
		return 0
	}
	uiMainInit()
	e, err := setup(f, nil, mygo.App.SetOpenAtLogin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	defer e.close()
	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	// 退出顺序：先让事件循环收尾（关渲染器、写 state.json cleanShutdown），再 Quit。
	// 不能在 OnWillQuit（主线程）里同步等：mac 上收尾要 RunOnMain，会死锁（MyGo workaround 4）。
	quit := func() {
		go func() {
			cancel()
			select {
			case <-loopDone:
			case <-time.After(5 * time.Second):
			}
			mygo.App.Quit()
		}()
	}
	settings := ui.NewSettings(e.app, runtime.GOOS)
	settings.SetLaunchAtLogin = mygo.App.SetOpenAtLogin
	settings.ExportDiag = e.exportDiag
	settings.OpenPath = mygo.Shell.OpenPath
	settings.ShowInFolder = mygo.Shell.ShowItemInFolder
	e.updateHooks(&settings.Hooks, quit)
	settings.Info = ui.Info{Version: build.Version, Commit: build.Revision(), Platform: runtime.GOOS + "/" + runtime.GOARCH, EncoderName: e.enc,
		DataDir: filepath.Dir(e.paths.Logs), LogsDir: e.paths.Logs, SignedBuild: signedBuild == "1", UpdateKey: updatePub() != nil}
	if runtime.GOOS != "darwin" {
		settings.Info.MpvPath = e.mpvPath(e.configSnapshot())
	}
	sh := &ui.Shell{S: settings, OnQuit: quit}
	e.app.OnChange(sh.Changed)
	mygo.App.OnSecondInstance(func([]string, string) { sh.Open() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		cancel() // 系统注销等外部退出：尽力而为
		if runtime.GOOS != "darwin" {
			select {
			case <-loopDone:
			case <-time.After(2 * time.Second):
			}
		}
	})
	mygo.App.WhenReady(func() {
		if em, ok := e.host.(emitter); ok && runtime.GOOS != "windows" {
			sig := func(r visibility.Reason, on bool) func() {
				return func() { em.Emit(desktop.Event{Kind: desktop.EventSignal, Reason: r, Active: on, Source: "mygo.Power"}) }
			}
			mygo.Power.OnLockScreen(sig(visibility.Locked, true))
			mygo.Power.OnUnlockScreen(sig(visibility.Locked, false))
			mygo.Power.OnSuspend(sig(visibility.Suspend, true))
			mygo.Power.OnResume(sig(visibility.Suspend, false))
		}
		cfg := e.configSnapshot()
		first := !cfg.Startup.Asked
		if err := sh.Start(first || e.app.View().SafeMode || cfg.Startup.OpenSettings); err != nil {
			e.log.Error("tray", "err", err)
			sh.Open() // 没有托盘（例如部分 Linux 面板）时至少给窗口
		}
		if cfg.Update.AutoCheck && cfg.Update.FeedURL != "" && settings.Info.UpdateKey {
			settings.CheckUpdates()
		}
		go func() { // 稳定运行 60 s 后确认本次更新（F-15）
			select {
			case <-time.After(60 * time.Second):
				_ = e.updater().MarkGood()
			case <-ctx.Done():
			}
		}()
		go func() {
			defer close(loopDone)
			if err := e.app.Run(ctx); err != nil {
				e.log.Error("run", "err", err)
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		e.log.Error("mygo", "err", err)
		return 1
	}
	if relaunchAfterQuit.Load() {
		relaunch(e.exe)
	}
	return 0
}
