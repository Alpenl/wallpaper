// wallpaperd 是动态壁纸 MVP 的唯一可执行文件（02 章：单进程 + 渲染子进程）。
//
//	wallpaperd                         托盘 + 设置窗口（MyGo），正常使用
//	wallpaperd -headless [-import f]   无 UI，供 e2e / Wine 验证；-duration 后退出并打印状态 JSON
//	wallpaperd -mfhost -pipe … -swap … Windows Media Foundation 渲染子进程（内部使用）
//	wallpaperd -helper probe -in f     在子进程里探测媒体文件（隔离解析器崩溃），输出 JSON
//	wallpaperd -update-apply [-feed u]  检查并增量安装更新；-update-rollback 回滚（F-15）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"wpapp/internal/app"
	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/media"
	"wpapp/internal/render"
	imgr "wpapp/internal/render/image"
	"wpapp/internal/render/mf"
	"wpapp/internal/render/mpv"
	"wpapp/internal/render/shader"
	webwall "wpapp/internal/render/web"
	"wpapp/internal/telemetry"
	"wpapp/internal/transcode"
)

var version = "0.3.0-mvp"

// updatePubKey 是 F-15 更新清单的 Ed25519 公钥（base64），构建时注入：-ldflags "-X main.updatePubKey=…"。
// 为空时不能在线更新（界面会说明）。signedBuild 由签名流水线注入 "1"。
var updatePubKey, signedBuild string

type flags struct {
	headless      bool
	standalone    bool
	fake          int
	importPath    string
	duration      time.Duration
	debug         bool
	shaderhost    bool
	webhost       bool
	mfhost        bool
	pipe, swap    string
	logPath       string
	helper        string
	in            string
	out           string
	width, height int
	cover         bool
	version       bool
	statusOut     string
	exportDiag    bool
	updApply      bool
	updRoll       bool
	feed          string
}

func parseFlags(args []string) *flags {
	f := &flags{}
	fs := flag.NewFlagSet("wallpaperd", flag.ExitOnError)
	fs.BoolVar(&f.headless, "headless", false, "无 UI 运行（e2e/Wine）")
	fs.BoolVar(&f.standalone, "standalone", false, "Windows：不挂桌面，用普通窗口（Wine 自检）")
	fs.IntVar(&f.fake, "fake-monitors", 0, "Windows standalone：把主屏切成 N 块虚拟显示器")
	fs.StringVar(&f.importPath, "import", "", "headless：启动后导入并设为所有显示器的壁纸")
	fs.DurationVar(&f.duration, "duration", 0, "headless：运行时长（0=直到 Ctrl+C）")
	fs.StringVar(&f.statusOut, "status", "", "headless：退出前把状态 JSON 写到此文件（- 为 stdout）")
	fs.BoolVar(&f.debug, "debug", false, "调试日志")
	fs.BoolVar(&f.shaderhost, "shaderhost", false, "internal: isolated native shader renderer")
	fs.BoolVar(&f.webhost, "webhost", false, "internal: isolated web renderer")
	fs.BoolVar(&f.mfhost, "mfhost", false, "内部：MF 渲染子进程")
	fs.StringVar(&f.pipe, "pipe", "", "内部：管道名")
	fs.StringVar(&f.swap, "swap", "flip", "内部：MF 交换链模式")
	fs.StringVar(&f.logPath, "log", "", "内部：子进程日志")
	fs.StringVar(&f.helper, "helper", "", "内部：probe / transcode")
	fs.StringVar(&f.in, "in", "", "内部：helper 输入文件")
	fs.StringVar(&f.out, "out", "", "内部：helper 输出文件")
	fs.BoolVar(&f.cover, "cover", false, "内部：居中裁切")
	fs.IntVar(&f.width, "width", 3840, "内部：转换目标宽度")
	fs.IntVar(&f.height, "height", 2160, "内部：转换目标高度")
	fs.BoolVar(&f.version, "version", false, "打印版本")
	fs.BoolVar(&f.exportDiag, "export-diagnostics", false, "导出诊断包后退出")
	fs.BoolVar(&f.updApply, "update-apply", false, "检查并安装更新后退出（F-15，命令行/测试用）")
	fs.BoolVar(&f.updRoll, "update-rollback", false, "回滚上一次更新后退出")
	fs.StringVar(&f.feed, "feed", "", "更新源（覆盖 config.update.feedURL）")
	fs.Parse(args)
	return f
}

func main() {
	f := parseFlags(os.Args[1:])
	switch {
	case f.version:
		fmt.Println("wallpaperd", version, runtime.GOOS+"/"+runtime.GOARCH)
		return
	case f.shaderhost:
		os.Exit(shader.HostMain())
	case f.webhost:
		os.Exit(webwall.HostMain())
	case f.mfhost:
		os.Exit(mf.HostMain(f.pipe, f.swap, f.logPath))
	case f.helper == "shader-preview":
		if err := shader.PreviewMain(f.in, f.out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	case f.helper == "probe":
		info, err := media.ProbeFile(f.in)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		json.NewEncoder(os.Stdout).Encode(info)
		return
	case f.helper == "transcode":
		os.Exit(runTranscodeHelper(f))
	case f.helper != "":
		fmt.Fprintln(os.Stderr, "unknown helper operation")
		os.Exit(2)
	case f.exportDiag:
		e, err := setup(f, nil, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		p, err := e.exportDiag()
		e.store.State.CleanShutdown = true
		e.store.State.Starts = nil
		st := e.store.State.Clone()
		e.store.StateSaver.Schedule(func() any { return st })
		if flushErr := e.store.FlushAll(); flushErr != nil {
			fmt.Fprintln(os.Stderr, "config shutdown:", flushErr)
		}
		e.close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(p)
	case f.updApply || f.updRoll:
		os.Exit(runUpdateCLI(f))
	case f.headless:
		os.Exit(runHeadless(f))
	default:
		os.Exit(runUI(f))
	}
}

// env 是 UI 与 headless 共用的初始化结果。
type env struct {
	paths         config.Paths
	store         *config.Store
	log           *slog.Logger
	logw          *telemetry.AsyncWriter
	host          desktop.Host
	app           *app.App
	exe           string
	enc           string // 编码器显示名
	initialConfig config.Config
}

func setup(f *flags, extraLog io.Writer, setLogin func(bool) error) (*env, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	lw, err := telemetry.NewAsyncWriter(paths.Logs, "wallpaperd")
	if err != nil {
		return nil, err
	}
	var w io.Writer = lw
	if extraLog != nil {
		w = io.MultiWriter(lw, extraLog)
	}
	log := telemetry.NewLogger(w, f.debug)
	log.Info("start", "version", version, "os", runtime.GOOS, "arch", runtime.GOARCH, "headless", f.headless)
	platInit(log)
	store, err := config.Open(paths)
	if err != nil {
		if closeErr := lw.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "logger:", closeErr)
		}
		return nil, err
	}
	safe := store.State.EnterSafeMode(time.Now())
	st := store.State.Clone()
	if safe {
		log.Warn("safe mode: too many unclean starts")
	}
	exe, _ := os.Executable()
	e := &env{paths: paths, store: store, log: log, logw: lw, exe: exe, initialConfig: store.Config.Clone()}
	e.host = newHost(f, log)
	imp := importer.Options{ShaderSnapshot: func(dir, dst string) error { return shader.Preview(e.exe, dir, dst) }, Library: paths.Library, Cache: paths.Cache, GOOS: runtime.GOOS, Lang: store.Config.UI.Lang,
		Probe: e.probe}
	imp.Snapshot = e.snapshot
	enc, ext, encName := e.encoder(store.Config)
	e.enc = encName
	e.app = app.New(app.Deps{Host: e.host, Store: store, Log: log, SafeMode: safe, SetLaunchAtLogin: setLogin,
		NewRenderer: e.newRenderer, Transcode: enc, TranscodeExt: ext,
		ResolveEncoder: func(cfg config.Config) (func(context.Context, string, string) error, string) {
			fn, ext, _ := e.encoder(cfg)
			return fn, ext
		},
		EncodeRendition: func(ctx context.Context, src, dst string, target transcode.Target) error {
			cfg := e.configSnapshot()
			fn, _, _ := transcode.CoverEncoder(runtime.GOOS, cfg.Render.FFmpegPath, e.nativeEncoderHelper(), target)
			if fn == nil {
				return transcode.ErrNoEncoder
			}
			return fn(ctx, src, dst)
		},
		EncoderInfo: e.encoderCapabilities,
		ProbeMedia:  e.probeContext,
		Import: func(ctx context.Context, src string) (*importer.Result, error) {
			opts := imp
			cfg := e.configSnapshot()
			opts.Lang = cfg.UI.Lang
			opts.BeforeReplace = func(id string) error { return e.app.PreparePackageReplacement(ctx, id) }
			if !f.headless {
				opts.ConsentSignal = func(title, permission string) bool { return e.app.RequestSignalConsent(ctx, title, permission) }
				opts.ConsentNetwork = func(title string, hosts []string) bool { return e.app.RequestNetworkConsent(ctx, title, hosts) }
			}
			opts.Transcode, opts.TranscodeExt, _ = e.encoder(cfg)
			opts.CanTranscodeGIF = e.encoderCapabilities(cfg).GIF
			return importer.Import(ctx, src, opts)
		}})
	store.SetOnError(e.app.ReportPersistenceError)
	store.StateSaver.Schedule(func() any { return st })
	_ = store.StateSaver.Flush()
	return e, nil
}

// probe 在子进程里解析（02 章：不可信文件的解析与主进程隔离），子进程失败时不回退到进程内。
func (e *env) probe(path string) (*media.Info, error) {
	return e.probeContext(context.Background(), path)
}

func (e *env) probeContext(parent context.Context, path string) (*media.Info, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.exe, "-helper", "probe", "-in", path)
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("probe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("probe helper: %w", err)
	}
	var info media.Info
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (e *env) mpvPath(cfg config.Config) string {
	if cfg.Render.MpvPath != "" {
		return cfg.Render.MpvPath
	}
	name := "mpv"
	if runtime.GOOS == "windows" {
		name = "mpv.exe"
	}
	for _, p := range []string{filepath.Join(filepath.Dir(e.exe), "mpv", name), filepath.Join(filepath.Dir(e.exe), name)} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// configSnapshot is safe for importer and updater workers. Initial configuration
// is immutable; after Run publishes, use the app's cloned snapshot, not Store.
func (e *env) configSnapshot() config.Config {
	if e.app != nil {
		if v := e.app.View(); v.Config.SchemaVersion > 0 {
			return v.Config
		}
	}
	return e.initialConfig.Clone()
}

func (e *env) snapshot(src, dst string) error {
	if runtime.GOOS == "darwin" {
		return macSnapshot(src, dst)
	}
	return mpv.FirstFrame(e.mpvPath(e.configSnapshot()), src, dst)
}

func (e *env) newRenderer(kind string, cfg config.Config, d desktop.DisplayID) render.Renderer {
	if kind == "shader" {
		return shader.New(e.exe)
	}
	if kind == "web" {
		return webwall.New(e.exe)
	}
	if kind == "image" {
		return imgr.New()
	}
	if r := platformRenderer(e, cfg, d); r != nil {
		return r
	}
	return mpv.New(mpv.Options{Path: e.mpvPath(cfg), Hwdec: cfg.Render.Hwdec, Display: string(d),
		LogFile: filepath.Join(e.paths.Logs, "mpv-"+safeName(string(d))+".log")})
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// exportDiag 写到日志目录的上一级：diagnostics-<时间>.zip（F-16）。
func (e *env) exportDiag() (string, error) {
	e.logw.Sync()
	dst := filepath.Join(filepath.Dir(e.paths.Logs), "diagnostics-"+time.Now().Format("20060102-150405")+".zip")
	cfgs := []string{filepath.Join(e.paths.Config, "config.json"), filepath.Join(e.paths.Config, "state.json")}
	// A separate file keeps logger health available even when its own writer fails.
	status := struct {
		Error   string `json:"error"`
		Dropped uint64 `json:"dropped"`
	}{Dropped: e.logw.Dropped()}
	if err := e.logw.Err(); err != nil {
		status.Error = err.Error()
	}
	statusPath := filepath.Join(e.paths.Logs, "logger-status.json")
	if err := config.WriteAtomic(statusPath, status); err != nil {
		return dst, err
	}
	cfgs = append(cfgs, statusPath)
	return dst, telemetry.ExportDiagnostics(e.paths.Logs, cfgs, dst, version)
}

func (e *env) close() {
	err := e.logw.Close()
	if dropped := e.logw.Dropped(); err != nil || dropped > 0 {
		fmt.Fprintf(os.Stderr, "logger: error=%v dropped=%d\n", err, dropped)
	}
}

func runHeadless(f *flags) int {
	if err := headlessPre(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	e, err := setup(f, os.Stderr, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	defer e.close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if f.duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, f.duration)
		defer cancel()
	}
	if f.importPath != "" {
		go func() {
			n := len(e.app.View().Library)
			e.app.Import(f.importPath, "")
			for i := 0; i < 600; i++ {
				v := e.app.View()
				if len(v.Library) > n {
					it := v.Library[len(v.Library)-1]
					e.log.Info("headless: assign", "item", it.ID)
					e.app.AssignAll(it.ID)
					return
				}
				if !v.Importing && v.LastError != "" {
					e.log.Error("headless: import failed", "err", v.LastError)
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}
	err = e.app.Run(ctx)
	if f.statusOut != "" {
		b, _ := json.MarshalIndent(e.app.View(), "", "  ")
		if f.statusOut == "-" {
			os.Stdout.Write(append(b, '\n'))
		} else {
			os.WriteFile(f.statusOut, b, 0o644)
		}
	}
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		e.log.Error("run", "err", err)
		return 1
	}
	return 0
}
