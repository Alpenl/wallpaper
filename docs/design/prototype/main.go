// wpproto: 动态壁纸真机测试原型（不是产品代码）。
// 对应 bench/TEST_PLAN.md 的 W-T1/W-T3/W-T6/W-T9 与 M1/M2/M4，以及 Linux 的 X11 兼容参考。
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

var (
	flagVideo      = flag.String("video", "", "视频文件路径（必填）")
	flagMpv        = flag.String("mpv", "mpv", "mpv 可执行文件路径（Windows/Linux）")
	flagMonitor    = flag.Int("monitor", 0, "显示器序号（0=主屏，-1=全部，每块屏独立判断）")
	flagLog        = flag.String("log", "wpproto.log", "日志文件")
	flagDuration   = flag.Duration("duration", 0, "运行时长，0=一直运行直到 Ctrl+C")
	flagStats      = flag.Duration("stats", 5*time.Second, "mpv 统计采样间隔")
	flagHwdec      = flag.String("hwdec", "", "覆盖 --hwdec（默认 Windows d3d11va，Linux auto-safe）")
	flagLevel      = flag.String("level", "desktop", "macOS 窗口层级：desktop | desktop-1 | desktop+1 | desktopicon-1")
	flagFSAux      = flag.Bool("fsaux", false, "macOS：collectionBehavior 加 FullScreenAuxiliary")
	flagSample     = flag.Duration("sample", 500*time.Millisecond, "macOS 遮挡采样间隔")
	flagNoPause    = flag.Bool("nopause", false, "只记录可见性，不暂停")
	flagBackend    = flag.String("backend", "mpv", "Windows 渲染后端：mpv | mf（Media Engine，Q-W10）；其他平台忽略")
	flagStandalone = flag.Bool("standalone", false, "Windows：不挂桌面，开一个普通顶层窗口（只测渲染后端，用于 A/B 隔离挂载因素或在 Wine 中自检）")
	flagPauseTest  = flag.Duration("pausetest", 0, "Windows：每隔该时长切换一次强制暂停（测暂停/恢复与暂停期开销；0=关闭）")
	flagMFHost     = flag.Bool("mfhost", false, "内部使用：作为 Media Foundation 渲染子进程运行（由 -backend mf 的主进程启动）")
	flagPipe       = flag.String("pipe", "", "内部使用：IPC v1 命名管道名")
	flagFakeMon    = flag.Int("fakemonitors", 0, "调试：与 -standalone 合用，把主屏横向切成 N 块“虚拟显示器”（用于在单屏环境/Wine 中验证按屏独立判断）")
	flagCoverTest  = flag.String("covertest", "", "调试：只开一个普通窗口覆盖给定矩形 \"x,y,w,h\"，持续 -duration（用于在 Wine 中触发 Covered）")
	flagSwap       = flag.String("swap", "flip", "mf 后端的交换链：flip（FLIP_DISCARD）| blt（DISCARD，用于分层窗口不兼容时）")
)

var logger *log.Logger
var start = time.Now()

func logf(format string, a ...any) {
	logger.Printf("%8.3f %s", time.Since(start).Seconds(), fmt.Sprintf(format, a...))
}

func main() {
	flag.Parse()
	if *flagVideo == "" && !*flagMFHost && *flagCoverTest == "" {
		fmt.Fprintln(os.Stderr, "用法: wpproto -video <文件> [选项]；见 README.md")
		flag.PrintDefaults()
		os.Exit(2)
	}
	f, err := os.OpenFile(*flagLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	logger = log.New(io.MultiWriter(os.Stdout, f), "", 0)
	logf("START os=%s args=%v", osName(), os.Args[1:])
	if *flagCoverTest != "" {
		runCoverTest()
		return
	}
	if *flagMFHost {
		runMFHost()
		logf("EXIT")
		return
	}
	run() // 平台实现，阻塞
	logf("EXIT")
}
