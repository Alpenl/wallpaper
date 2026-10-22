# wpproto：真机测试原型

> **独立研究实验**：本目录不是 `internal/` 下的生产实现。下文旧实验结果保留其原始范围，不自动计入本轮产品验收；当前状态与模块测试范围见 [验证记录](../../VALIDATION.md)。

这是一个用来验证设计假设的测试程序，**不是产品代码**。三个平台共用一份源码，全部用 `CGO_ENABLED=0` 交叉编译。

| 文件 | 内容 |
|---|---|
| `main.go` | 命令行参数和日志（每行开头是相对启动时间的秒数） |
| `mpvipc.go` | mpv JSON IPC 客户端；定期统计 `hwdec-current`、掉帧数、`time-pos`，以及 STALL 检测（02 章 2.4） |
| `proto_windows.go` | Progman/WorkerW 挂载（24H2 和旧版两条分支）、自建子 HWND、Job Object、命名管道、TaskbarCreated 重挂、健康检查、前台全屏暂停 |
| `proto_darwin.go` | 4 种窗口层级、AVQueuePlayer + AVPlayerLooper + AVPlayerLayer，每次采样同时记录 occlusionState 和 CGWindowList 的结果 |
| `proto_linux.go` | 兼容参考：用 xgb 建 `_NET_WM_WINDOW_TYPE_DESKTOP` 窗口，再用 `mpv --wid` 嵌入（只适用于 X11 会话） |
| `bin/` | 预编译产物和 `SHA256SUMS`（Go 1.27.3，`-trimpath -ldflags="-s -w"`） |

## 构建
```bash
cd prototype
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/wpproto_windows_amd64.exe .
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o bin/wpproto_darwin_arm64 .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o bin/wpproto_linux_amd64 .
```
依赖：`github.com/ebitengine/purego v0.11.1`（macOS）和 `github.com/jezek/xgb v1.3.1`（Linux）。Windows 只用标准库 `syscall`。

## 准备测试素材
按 06 章规范转一段 30–60 s 的 4K30 无音轨片段，并保证首帧是 IDR：
```bash
ffmpeg -i src.mp4 -an -vf "scale=3840:2160:force_original_aspect_ratio=increase,crop=3840:2160,fps=30" \
  -c:v libx264 -profile:v high -crf 21 -g 60 -bf 2 -pix_fmt yuv420p -movflags +faststart loop_h264.mp4
```
（libx264 只用于在测试机上准备素材，不随产品分发，见 11 章。）

## Windows
1. 下载 mpv 的 Windows 构建，并记下 `mpv.exe` 的路径。
2. 运行：`wpproto_windows_amd64.exe -video D:\loop_h264.mp4 -mpv C:\tools\mpv\mpv.exe -log wp.log`
3. 常用参数：
   - `-monitor N`：选择显示器；
   - `-hwdec d3d11va|auto-safe|no`；
   - `-stats 5s`：统计采样间隔；
   - `-nopause`：只记录，不暂停；
   - `-duration 10m`：运行时长。
4. 需要关注的日志行：
   - `DESKTOP … raised24H2=true|false`：走的是哪条挂载分支；
   - `SURFACE … parent=`：24H2 上父窗口应该是 Progman，旧版上应该是 WorkerW；
   - `JOB ok`；
   - `STATS hwdec-current=d3d11va …`：硬解是否生效，以及掉帧数；
   - `FOREGROUND fullscreen=true` 和 `PAUSE=true`；
   - `TASKBAR_CREATED` 和 `REATTACH`；
   - `HEALTH`；
   - `STALL` 和 `MPV_EXIT`（重启）。
5. 已知简化：重挂和 mpv 启动是在 UI 线程上同步执行的，重挂退避期间消息循环会被阻塞，最长约 15 s；只做单块显示器；暂停只看前台全屏和 QUNS，不做网格遮挡。（**第 13 轮、第 19 轮已解决**：异步重挂；`-monitor -1` 多屏；visibility 状态机 + 网格遮挡，见文末）

## macOS
1. `xattr -d com.apple.quarantine wpproto_darwin_arm64`（下载的文件带有隔离属性）。arm64 产物里已有 Go 链接器生成的 ad-hoc 签名（box 上检查过 LC_CODE_SIGNATURE 存在）。
2. 运行：`./wpproto_darwin_arm64 -video ~/loop.mp4 -level desktop -log wp.log -duration 10m`
3. 参数：
   - `-level desktop|desktop-1|desktop+1|desktopicon-1`；
   - `-fsaux`：collectionBehavior 加上 FullScreenAuxiliary；
   - `-sample 500ms`：采样间隔；
   - `-nopause`；
   - `-monitor N`。
4. 需要关注的日志行：
   - `WINDOW level=…(数值)`；
   - `SAMPLE occVisible=… occFlips=… cgCoverFrac=… cgFullscreen=… cgCostUs=…`：其中 `cgCostUs` 就是 Q-M6 需要的 CGWindowList 单次调用开销；
   - `PAUSE=`；
   - 最后的 `SUMMARY`：`occFlips` 越大，说明 occlusionState 跳得越厉害。
5. 没有屏幕录制权限时，CGWindowList 仍然会返回窗口边界和层级（原型不读取窗口标题）【推断，基于 Apple 的权限说明，需要实测确认】。

## Linux（兼容参考，只适用于 X11 会话）
- 运行：`./wpproto_linux_amd64 -video loop.mp4`。没有 GPU 的环境可以设 `WPPROTO_MPV_VO=x11`。
- **box 实测【实测】**（Xvfb 1280×720，没有窗口管理器，mpv v0.40.0（Debian 13 的发行版构建）：
  - `xprop` 显示 `_NET_WM_WINDOW_TYPE_DESKTOP` 和 `_NET_WM_STATE_BELOW/STICKY/SKIP_*` 都已设置；
  - `xwininfo -children` 显示 mpv 的子窗口（`"x11" "mpv"`，1280×720）确实嵌在我们的窗口里；
  - 根窗口截图的像素是 testsrc2 的彩色画面，不是黑屏；
  - IPC 统计正常，`hwdec-current=no`（box 没有 GPU），循环 seek 正常，掉帧 0。
  - 没有验证的部分：真实 GNOME/KDE 窗口管理器下的层级（桌面图标层、DING 扩展），Wayland 会话。

## 与 TEST_PLAN 的对应
| 测试项 | 原型能直接回答什么 | 操作 |
|---|---|---|
| W-T1 挂载 | 24H2/25H2 上的分支、父窗口、z 序；桌面图标是否在壁纸之上 | 启动后看桌面；看 `DESKTOP`、`SURFACE` 日志 |
| W-T3 硬解与掉帧 | `hwdec-current`、`frame-drop-count`；同时运行 `bench/windows_capture.ps1 -Phase wallpaper` 采集 CPU 和 GPU | 每种素材各 10 分钟 |
| W-T6 恢复 | `taskkill /f /im explorer.exe && start explorer` 之后的 `TASKBAR_CREATED`、`REATTACH`，以及画面是否恢复 | 重复 5 次 |
| W-T9 TDR | 触发 TDR 后有没有 `STALL` 或 `MPV_EXIT`，画面在 10 s 内能否恢复 | 见 TEST_PLAN |
| 崩溃兜底（Job） | 用任务管理器结束 wpproto 后，mpv.exe 是否在 1 s 内消失 | 用任务管理器观察 |
| 暂停 | 全屏游戏或 F11 浏览器 → `PAUSE=true`；CPU 和 GPU 回落（配合采集脚本） | |
| M1 层级 | 4 种 `-level` 各跑一次：图标是否可点、Mission Control、Stage Manager、全屏 Space 下的表现 | 每种 5 分钟 |
| M2/M4 遮挡 | `occFlips` 与 `cgCovered` 对比；暂停是否正确 | 拖动、最大化、全屏、切换 Space |
| Q-M6 | `cgCostUs` 的分布 | 同时运行 `macos_powermetrics.sh` |
| M3 循环 | AVPlayerLooper 的循环点有没有卡顿（目测，加上录屏逐帧检查） | |

## 第 13 轮修正（Q-P2、Q-P3）
- **Q-P2 异步重挂**（`proto_windows.go`）：`reattach()` 只销毁旧表面并启动退避；退避（0/1/2/4/8/16 s）在 goroutine 里计时，再 `PostMessage(wmAppReattach)` 回到 UI 线程做**一次**创建尝试；失败继续排下一档，6 次后放弃（产品中降级为静态壁纸）。`startMpv`（等待命名管道最长约 5 s）全部改为 goroutine 调用；重挂期间健康检查暂停、`startMpv` 不启动（避免 wid=0）。消息循环不再阻塞【源码，本仓库；Windows 真机未运行】。
- **Q-P3 卡住检测**（`mpvipc.go`）：进度键改为 `(loops, time-pos)`，`loops` 统计 mpv 的 `playback-restart` 事件（每次循环都会触发）。box/Xvfb 实测：3 s 视频、3 s 采样间隔，6 次采样里 time-pos 有 4 次恰好相同（2.433333），旧实现会误报 STALL，新实现因 loops 递增未误报【实测】。
- 已重新交叉编译 5 个目标，`bin/SHA256SUMS` 已更新。

## 第 16 轮：Media Foundation 后端（Q-W10）
- 切换：`-backend=mpv`（默认）| `-backend=mf`。两个后端共用同一套挂载、健康检查、异步重挂、暂停判定代码（`backend_windows.go` 的 `renderBackend` 接口）。
- 方案：`IMFMediaEngine` **帧服务器模式**（`MF_MEDIA_ENGINE_DXGI_MANAGER`，D3D11 设备开 `VIDEO_SUPPORT` 与多线程保护）→ 每个 vblank（`IDXGIOutput::WaitForVBlank`）调 `OnVideoStreamTick`，有新帧才 `TransferVideoFrame` 到自建子 HWND 交换链的后台缓冲并 `Present`。没有选 `MF_MEDIA_ENGINE_PLAYBACK_HWND` 模式，因为那样由 MF 自己建窗口/交换链，我们无法控制 vblank 节奏、暂停时“完全不 present”，也拿不到 DEVICE_REMOVED。纯 Go：COM 用 vtable 下标 + `syscall.SyscallN`（下标与 GUID 核对自 mingw-w64 12.0 头文件），无 cgo。
- 循环：`SetLoop(TRUE)`；兜底：收到 ENDED 时再 `Play()`。暂停：`Pause()` 且渲染线程阻塞（不等 vblank、不 present）。静音：`SetMuted(TRUE)`。
- 统计（`-stats` 周期写 `MFSTATS`）：`IMFMediaEngineEx::GetStatistics` 的 FRAMES_RENDERED / FRAMES_DROPPED / FRAMES_CORRUPTED；本程序 `presented`（已 Present 帧数）、`appSkipped`（相邻帧时间戳间隔 > 1.5 帧 → 取帧线程错过的帧）、`loops`（时间戳回退次数）、`endedFallback`。卡住判定与 mpv 同规则：(loops, pts) 连续 2 次不变且未暂停 → 重建后端。
- `Present` 返回 `DXGI_ERROR_DEVICE_REMOVED/RESET` → 重建后端（W-T9 可以直接对比两个后端的恢复）。
- 新增调试参数：`-standalone`（不挂桌面，开普通顶层窗口，用于隔离挂载因素）、`-pausetest 3s`（周期强制暂停/恢复，用于测暂停期开销）、`-swap flip|blt`（分层子窗口若不接受 FLIP 交换链时改用 blt）。
- 已知简化：不调用带 double 参数/返回值的方法（syscall 不能正确传浮点寄存器），所以重挂/重建后从头播放；MF 后端在本进程内运行（产品中应与 mpv 一样放进独立渲染进程，同一 exe 加子命令即可）。（**第 19 轮已改为子进程**，见文末）

### Wine 10.0 上的半实测（box，Xvfb，`-standalone -backend mf`，3 s 640×360 H.264 30 fps 片源）【实测，Wine——不代表真 Windows】
| 项 | 结果 |
|---|---|
| COM 链路（MFStartup → D3D11 → DXGI 管理器 → 类工厂 → CreateInstance → 交换链 → GetBuffer → GetContainingOutput） | 全部成功（`MF_READY ... engineEx=true`），说明 vtable 下标、GUID、结构体布局至少在 amd64 上正确 |
| 解码与显示 | 装 gstreamer1.0-libav 后，约 2 s 内 `presented=46–48`，与 30 fps 相符 |
| 暂停/恢复（`-pausetest 1500ms`） | 暂停期间 `presented` 与 `pts` 冻结，恢复后从原位置继续 |
| 循环 | **Wine 不支持**：收到 ENDED，`Play()` 兜底也不从头播放；随后卡住检测触发重建并恢复播放。真 Windows 上 SetLoop 的行为需真机确认（交接清单 2.2） |
| GetStatistics | Wine 返回失败（日志里为 -1）；真机需确认返回类型 |

## 第 19–20 轮：接入状态机、MF 子进程化
### 架构
- 每块显示器一个 `screen`（子 HWND + 渲染后端）。`-monitor -1` = 全部显示器；`-monitor 0` = 主屏（默认）；`-monitor N` = 第 N 块。
- 暂停由 `../visibility`（07 章 v1.1 状态机，`go.mod` 用 `replace wpvis => ../visibility` 引入）驱动。所有状态机调用都在 UI 线程上；后端结果用 `PostMessage(wmAppBackend)` 回到 UI 线程。定时器只在状态机有截止时间时设置（新增 `Controller.NextDeadline()`），空闲时不周期唤醒（遮挡兜底轮询 1 s 除外）。
- 信号源 → Reason：
| 信号 | API | Reason / 范围 |
|---|---|---|
| 前台全屏 | `SetWinEventHook`（FOREGROUND、MOVESIZEEND、MINIMIZESTART/END）+ 1 s 兜底；前台窗口覆盖某屏、非 shell、非 cloaked；QUNS 独占全屏算在前台窗口所在屏 | Fullscreen / 按屏 |
| 遮挡 | 同上触发；50 px 网格，未覆盖 ≤ 5%，或该屏有最大化窗口，或单窗口覆盖 ≥ 95%（07 章 7.4） | Covered / 按屏 |
| 锁屏 | `WTSRegisterSessionNotification` → `WM_WTSSESSION_CHANGE` LOCK/UNLOCK | Locked / 全局 |
| 远程桌面 | 同上 REMOTE/CONSOLE_CONNECT/DISCONNECT 时查 `GetSystemMetrics(SM_REMOTESESSION)`；启动时查一次 | Remote / 全局 |
| 交流/电池、电量、省电、显示器开关 | `RegisterPowerSettingNotification`（GUID_ACDC_POWER_SOURCE、GUID_BATTERY_PERCENTAGE_REMAINING、GUID_POWER_SAVING_STATUS、GUID_CONSOLE_DISPLAY_STATE） | Battery、LowBattery（电池且 ≤ 20%）、BatterySaver、DisplayOff / 全局 |
| 睡眠/唤醒 | `WM_POWERBROADCAST` PBT_APMSUSPEND / PBT_APMRESUMEAUTOMATIC | Suspend / 全局 |
| 屏保 | 1 s 轮询 `SPI_GETSCREENSAVERRUNNING` | Screensaver / 全局 |
| 手动 | `-pausetest D`：每 D 切换一次 | User / 全局 |
- Action 执行：Pause/Freeze → 后端 level 1；Release → level 3（mpv `stop`，进程保留，恢复时 `loadfile … start=<pos>`；mfhost 释放引擎和交换链，恢复时从头播放）；Snapshot → 结束渲染进程（有意结束，不算崩溃）；Resume → level 0，或从 L4 重新启动进程；Restart → 指数退避（1/2/4…≤30 s）重启，结果回报 `RestartOK/RestartFailed`；RestartedAt → 按级别就位；HealthCheck → 健康检查；FPSCap → 只记录（原型未实现限帧）。
- 日志新增：`SIGNAL`（信号源、显示器、Reason 开关）、`VIS`（每个 Action、当前级别、有效原因）、`SUPERVISOR`、`PROBE_COST`（遮挡/全屏判定耗时，每 60 次和退出时汇总）。
- `-pausetest D` 时状态机的 t23/t34 缩短为 D/3、2D/3，一个暂停周期内会依次经过 L1→L3→L4，便于在真机上一次测完各级别的开销。

### MF 子进程（mfhost）
- `-backend mf` 时，主进程为每块屏启动 `wpproto -mfhost -pipe <名字>` 子进程。它继承主进程的 Job Object（`IsProcessInJob` 在 hello 里回报），通过 **IPC v1**（02 章 2.3：NDJSON，`v/t/id/m/p` 信封，1 MiB 行上限，版本不符断开）通信。主进程创建管道 `\\.\pipe\wpd-<会话ID>-<16 字节随机 hex>`（`FILE_FLAG_FIRST_PIPE_INSTANCE`），子进程作为客户端连接；5 s 内没连上就结束子进程。
- 方法：`hello{proto,app}` → `{pid,caps,inJob}`、`load{entry,wid,bounds,paused}`、`setPause{level 0..3}`、`ping`、`shutdown`；事件：`ready`、`firstFrame{ms}`、`stats{rendered,dropped,corrupted,presented,appSkipped,loops,endedFallback,pts100ns,paused}`、`error{code,msg,fatal}`。卡住或 DEVICE_REMOVED 时子进程发 `error{fatal}` 后退出，由主进程的 supervisor 重启 —— 与 mpv 的处理路径完全相同。
- 子进程日志：`<log>.mfhost<屏号>.log`。
- 已知简化：管道用默认安全描述符（产品要显式设置只含当前用户 SID 的 DACL）；ipc.go 有 `go test`（net.Pipe）覆盖往返、错误、版本不符、超长行、超时。

### 第 19 轮修复的隐患
- `syscall.NewCallback` 创建的回调永不释放，总数上限约 2000。旧代码在每次遮挡轮询和每次重挂时新建回调，1 s 轮询约 30 分钟后会 panic。已改为只建一次。
- mpv 统计在暂停/释放期间不再查询（L3 时 `time-pos` 不可用会被误判为 IPC 断开，同时减少暂停期唤醒）。

### Wine 10.0 验证（box，Xvfb）【实测，Wine——不代表真 Windows】
| 场景 | 结果 |
|---|---|
| mf 子进程：启动、IPC hello/load、`inJob=true` | 通过；两块屏各自一个 mfhost 进程，管道名符合 2.3 格式 |
| `-pausetest`：User → L1 → L3（释放）→ L4（结束进程）→ 恢复（重新拉起进程） | mf 与 mpv 两个后端都通过；恢复去抖 300 ms 可见（6.228 s 解除 → 6.530 s Resume）；L4 结束进程没有被当作崩溃 |
| supervisor | Wine 下 MF 不循环 → 子进程报 `error{stall,fatal}` 退出 → Restart（1 s 退避）→ `RestartedAt`；重启期间 User 变为开，重启后正确以 L1（暂停）就位 |
| mpv（shinchiro 2026-10-21 Windows 版，`-hwdec no`）：循环、L3 `stop`、L4、重新拉起 | 通过（Wine 下 mpv 循环正常） |
| 按屏独立：`-standalone -fakemonitors 2`，另开 `-covertest` 窗口 | 覆盖右半屏 → 只有屏 1 Fullscreen+Covered → 只暂停屏 1；覆盖左屏 1240×720 → 只有屏 0 Covered，进入去抖 150 ms 可见（9.005 → 9.155 s） |
| 判定开销 | Wine 下平均约 1.06–1.13 ms、最大约 4.2 ms（18 次）—— 超过 07 章 7.6 的 1 ms 目标，但 Wine 的每次 Win32 调用都要经过 wineserver，**只能说明需要在真机上测**（交接清单 3.4） |
| 锁屏、远程、电源、睡眠通知 | Wine 中注册全部成功，但无法注入事件 → 真机 |
