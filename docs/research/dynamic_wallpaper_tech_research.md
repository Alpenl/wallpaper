# 跨平台动态壁纸：技术调研报告（Windows / macOS 优先，Ubuntu 兼容参考）

> **历史研究原文**：来源日期和结论按原报告保留，本轮未重跑旧测试或重核外部数字。当前使用范围、后续修正与导航见 [调研索引](README.md)。

> 版本：2026-10-20（PDT）。优先级已按最新要求调整：**Windows、macOS 是核心平台，MVP 先做这两个**；Linux 只看 Ubuntu 上的 GNOME（X11/Wayland）和 KDE，用来检验架构兼容性。
> 可信度标注：**【源码】** 表示本次 `git clone` 下来、用 rg 看过的代码（路径按仓库根目录写）；**【文档】** 表示官方或项目文档；**【实测】** 表示第三方测出来的数字；**【推断】** 是根据 API 语义推出来的，还没实测，落地前要验证。
> 市场部分见 `docs/research/wallpaper_research.md`。

---

## 0. MyGo 与“无 cgo”约束（已查到资料）

- MyGo = **egoist/mygo**（https://github.com/egoist/mygo ，架构文档 https://mygo.egoist.dev/docs/architecture.md ）【文档】：
  - 用 `CGO_ENABLED=0` 构建。macOS/Linux 在运行时通过 **purego**（dlopen + 汇编 trampoline）调用原生 API，Windows 通过 Go 的 `syscall` 包，任何地方都不 `import "C"`。
  - WebView 用系统自带的：WKWebView / WebKitGTK 4.1 / WebView2，不捆绑浏览器。原生 UI 用 Metal / D3D11 / OpenGL 自绘。
  - 官方数据：hello-world 二进制约 7 MB；macOS 上约 35 MB（加上 WKWebView 的子进程约 62 MB）；空闲 0% CPU，“Nothing polls”。
  - purego 的限制：`SyscallN` 不能直接传 float 或结构体（要用 `RegisterFunc`，它走反射，**不要放在热路径上**）；`NewCallback` **每个进程最多约 2000 个而且永不释放**，必须在启动时按签名一次性创建好。
  - 如果依赖确实需要 cgo，可以设 `CGO_ENABLED=1`（https://mygo.egoist.dev/docs/distribution ），代价是失去任意机器交叉编译的能力。
- 对各项接入的影响和建议：

| 接入对象 | 可选方案 | 建议 |
|---|---|---|
| Win32 (Progman/WorkerW, SetParent, DWM, 电源/会话通知) | `syscall`/`golang.org/x/sys/windows` 直接调 | **完全可行**，零 cgo；回调（WndProc、EnumWindows）用 `syscall.NewCallback`，全局复用 |
| libmpv（Win/mac/Linux） | ①purego dlopen `libmpv-2.dll/.dylib/.so` 调 `mpv_create/mpv_set_option_string/mpv_command/mpv_observe_property`；②**子进程 mpv + `--wid` + JSON IPC**（Lively 就是这么做的，见 §2） | MVP 用 **子进程 + IPC**：崩溃隔离，暂停的时候还能整个挂起进程；第二阶段再上 purego + render API |
| libmpv render API（`mpv_render_context_create`, `mpv_render_context_render`, update callback） | purego：要传结构体数组 `mpv_render_param`，指针用 unsafe 手工布局，update callback 用一个全局 `NewCallback` | 可行，但 OpenGL/D3D 上下文也得全部经 purego 管理，工作量大；**【推断】** |
| Cocoa（NSWindow level, collectionBehavior, NSWorkspace 通知） | purego 的 `objc` 包（objc_msgSend）；MyGo 内部已经这么做 | 可行；`setFrame:`（NSRect 含 CGFloat）要用 `RegisterFunc` |
| AVFoundation / VideoToolbox | 同上走 objc；或者子进程（mpv `--hwdec=videotoolbox`） | macOS 首选 AVPlayerLayer（系统管零拷贝和省电），用 purego objc 调用 |
| Wayland（layer-shell） | 纯 Go 协议实现（例如社区的 wayland 纯 Go 客户端库，**未调研具体成熟度**），或者 purego 调 libwayland-client，或者直接子进程跑 `mpvpaper` | Linux 不是首发，**用子进程方案隔离** |

---

## 1. 桌面集成

### 1.1 Windows（核心）

**经典方案：Progman 0x052C → WorkerW → SetParent**【源码】`lively/src/Lively/Lively/Core/WinDesktopCore.cs`（约 150–230 行）：
1. `progman = FindWindow("Progman")`；
2. `SendMessageTimeout(progman, 0x052C, 0xD, 0x1, SMTO_NORMAL, 1000, …)`，这是未公开文档的消息，作用是让 Progman 在图标层后面生成一个 WorkerW；
3. 旧结构：`EnumWindows` 找到含 `SHELLDLL_DefView` 子窗口的顶层窗口，取它的**下一个同级** `WorkerW`（`FindWindowEx(NULL, tophandle, "WorkerW", NULL)`）；
4. 把壁纸 HWND `SetParent` 到这个 WorkerW 上，再按显示器矩形摆位置。
- LuminWallpaper 也是同样的做法：`src/platform/windows/desktop_integration_windows.cpp:116-120`【源码】。

**Win11 24H2 之后的变化**【源码注释，引用了 Microsoft 的说明】`WinDesktopCore.cs` 约 155–175 行：
- Progman 现在带 `WS_EX_NOREDIRECTIONBITMAP`（没有 GDI 内容）；`SHELLDLL_DefView` 变成 Progman 的 **WS_EX_LAYERED 子窗口**；WorkerW 也成了 **Progman 的子窗口**，z 序排在 DefView 下面。
- 检测方法：`HasExtendedStyle(progman, WS_EX_NOREDIRECTIONBITMAP)`。命中的话，改用 `FindWindowEx(progman, 0, "WorkerW", 0)` 查找。
- 微软建议：自己创建一个 **WS_EX_LAYERED 子 HWND**，放在 DefView 下面、WorkerW 上面，并设 `SetLayeredWindowAttributes(bAlpha=0xFF)`，这样 DX present 不会掉性能。
- 其他坑：24H2 下 mpv 窗口边框对齐出问题，Lively 加了 `--no-border`（`VideoMpvPlayer.cs`，见 issue https://github.com/rocksdanister/lively/issues/2415 ）。WebView2 作为 WorkerW/Progman 子窗口时拿不到正确的 DPI（`Factories/WallpaperPluginFactory.cs:219` 注释）。
- 图标显示/隐藏：`DesktopUtil.cs:20` 给 DefView 发 `WM_COMMAND 0x7402`【源码】。

**DirectComposition**：微软的说明建议对 layered 子窗口做 DX blt present。**目前没有找到哪个开源壁纸项目在 WorkerW 下用 DComp visual 树的实现**，只能标为【推断/待验证】。建议的渲染路径：D3D11 flip-model 交换链（`DXGI_SWAP_EFFECT_FLIP_DISCARD`），把 mpv 的 `--vo=gpu-next --gpu-api=d3d11` 嵌进子 HWND，在 24H2 上实测撕裂和功耗之后再决定要不要引入 DComp。

**Go 实现要点**：上面用到的 API 全部在 user32/dwmapi，`x/sys/windows` 加 `syscall.NewCallback` 就够了，不需要 cgo。

### 1.2 macOS（核心）

- 【源码】`LuminWallpaper/src/platform/macos/desktop_integration_macos.mm:118-121`：
  `[win setLevel:kCGDesktopWindowLevel]`；`setCollectionBehavior: CanJoinAllSpaces | Stationary | IgnoresCycle`；`NSWindowStyleMaskBorderless`；`setFrame:` 设为屏幕 frame；`NSApp setActivationPolicy:Accessory`（不显示 Dock 图标）。
- 层级说明【推断，基于 CGWindowLevel 的定义】：`kCGDesktopWindowLevel` 低于 `kCGDesktopIconWindowLevel`，所以壁纸在 Finder 图标下面。如果要能点到图标，就设 `ignoresMouseEvents=YES`。
- 多 Space：`CanJoinAllSpaces` 让一个窗口出现在所有 Space 上；`Stationary` 让它不参与 Exposé/Mission Control 的动画。**每块屏幕一个 NSWindow**。
- Plash（sindresorhus）：**已经闭源**，仓库 README 写着 "The app is no longer open-source"（https://github.com/sindresorhus/Plash ），所以没法引用它的源码。它是 WKWebView 壁纸的代表产品。

### 1.3 Linux（仅 Ubuntu GNOME / KDE，兼容参考）

- **GNOME X11**：做一个 `_NET_WM_WINDOW_TYPE_DESKTOP` 窗口。【源码】Fresco `src/daemon/x11win.rs:19-20`；Komorebi `komorebi/screen.py:58` 的 `set_type_hint(Gdk.WindowTypeHint.DESKTOP)`。
- **GNOME Wayland**：Mutter **不支持 wlr-layer-shell**。
  - Hanabi 扩展【源码】`gnome-ext-hanabi/src/`：用 `Meta.WaylandClient.new_subprocess` 启动渲染子进程（`waylandSubprocess.ts:47`），子进程里 GStreamer 用 `gtk4paintablesink`/`clappersink` 输出（`renderer/renderer.ts:348-356`）。Shell 那边 patch `Background.BackgroundManager.prototype._createBackgroundActor`（`gnomeShellOverride.ts:111`），再用 `Clutter.Clone` 把渲染窗口克隆到背景 actor 上（`wallpaper.ts:184`），同时把渲染窗口从 `get_window_actors` 里隐藏掉。
  - 结论：**GNOME Wayland 只能通过 Shell 扩展实现**。Fresco 在这种情况下直接退回静态帧（`src/capability.rs:4,28` 的 `WaylandGnomeStatic`）【源码】。
- **KDE Plasma 6**：支持 `zwlr_layer_shell_v1`，可以走 layer-shell 的 BACKGROUND 层（mpvpaper `src/main.c:1267`，`set_exclusive_zone(-1)` 在 :949，空 input region 在 :935）；另一条路是写原生 Plasma wallpaper 插件（QML，**这次没有深入调研**）。Fresco 是先探测 registry 里有没有 `zwlr_layer_shell_v1`，再决定用哪个后端（`capability.rs`）。
- Hyprland/wlroots：同样走 layer-shell，不展开。

### 1.4 扩展点设计（建议接口）
```go
type DesktopHost interface {          // 每平台一个实现
    Monitors() []Monitor               // id, rect(物理px), scale, refreshHz, primary
    AttachSurface(m Monitor) (Surface, error) // 返回 HWND/NSWindow*/X11 Window/wl_surface 或 "extension-proxy"
    Detach(Surface)
    Events() <-chan HostEvent          // 显示器增删/DPI/explorer重启/Space切换/锁屏/电源
    Capabilities() Caps                // LiveVideo|Web|Shader|StaticOnly, PerMonitorPause, OcclusionSource
}
type VisibilityProbe interface { State(monitorID) Visibility } // Visible|Covered|Fullscreen|Locked|Remote
```
Linux 有三个后端：X11Desktop、LayerShell（KDE）、GnomeExtensionProxy（Shell 扩展通过 D-Bus 拿到子进程窗口，参考 Hanabi）。都通过 `Capabilities` 告诉上层能做什么，能力不够时降级成静态壁纸。

---

## 2. 视频性能

### 2.1 两种嵌入方式
- **wid 嵌入（子进程）**【源码】Lively `Core/Wallpapers/VideoMpvPlayer.cs:87-128`。参数：`--volume=0 --loop-file --keep-open --media-controls=no --geometry=-9999:0 --force-window=yes --window-minimized=yes --stop-screensaver=no --input-default-bindings=no --no-border --no-osc --input-ipc-server=<pipe> --hwdec=auto-safe --target-colorspace-hint-mode=… --config-dir=…`。窗口先在屏幕外创建，再 SetParent 到 WorkerW，之后通过 named pipe IPC 控制。
- **render API（进程内）**【源码】mpvpaper `src/main.c:74` 的 `mpv_render_context *mpv_glcontext`，配合 Wayland 的 `wl_surface_frame` 回调（:186-225）**按合成器帧回调驱动渲染**，天然和刷新率同步，被遮挡时合成器不发回调，渲染也就停了。linux-wallpaperengine `VideoPlayback/MPV/GLPlayer.cpp:231` 设 `hwdec=auto`。
- 取舍：wid 方案简单、跨进程隔离，暂停时可以挂起整个进程；render API 能和自家合成层（叠加 Shader、转场）共用一个 GL/D3D 上下文，但在无 cgo 条件下实现难度最高。

### 2.2 硬件解码设置
- 参考配置：Lively 用 `auto-safe`；OpenWallpaper 用 `auto-safe`（`wallpaperd/src/video.c:221`）；Fresco 的 `select_hwdec`（`src/config.rs:248-258`）是 NVIDIA 用 `nvdec,vaapi,auto-safe`，其他用 `auto-safe`，**旋转画面时改用 `*-copy`**（copy 模式会回读到内存，CPU 占用更高，只在需要时用）。
- 各平台：
  - Windows：`--hwdec=d3d11va` + `--vo=gpu-next --gpu-api=d3d11`，帧留在 D3D11 纹理里，全程零拷贝【文档：mpv manual hwdec 章节 https://mpv.io/manual/master/#options-hwdec 】；也可以用 Media Foundation（Lively 有一个 WMF 播放器 `Core/Wallpapers/VideoWmfProcess.cs`）。
  - macOS：`videotoolbox`，或者直接用 AVPlayerLayer（系统合成，【推断】功耗最低）。
  - Linux：`vaapi`/`nvdec`。
- **避免回读**：不要用 `*-copy`；不要在 CPU 侧做缩放、截图、取色；视频滤镜（`--vf`）基本都会迫使回到 CPU 或 copy 模式。
- 生效验证：Windows 任务管理器 GPU 引擎里应该出现 “Video Decode”【文档】Lively Wiki Performance https://github.com/rocksdanister/lively/wiki/Performance ；mpv 可以读属性 `hwdec-current`，Fresco 注意到要在文件打开**之后**再读才准（`daemon/mpvpaper.rs:856-860`）。

### 2.3 实测数据（只列能找到的）
| 来源 | 场景 | 数据 |
|---|---|---|
| Fresco README【实测，turbostat，Intel N150，VA-API】 | 4K60 全质量 / Reduced / Minimum | GPU 2.77 / 1.60 / 0.99 W；整个封装 7.94 / 5.95 / 4.97 W |
| 同上 | 1080p60 全质量 / Reduced | GPU 1.37 / 0.63 W；封装 6.00 / 4.03 W |
| OBS PR #6021【实测，3950X】（OBS 场景，**不是壁纸**） | 4K H.264 / 4K HEVC 软解→硬解 | CPU 9%→2%，20%→3% |
| signalrgb-wallpaper 提交 b1addc3【实测，2×1440p，Lively】 | 两块屏都被遮挡：span 模式 / per-display 模式 | 3.7% GPU / 0% GPU |
| Lively issue #2157 | 用户反馈 mpv GPU 占用高 | 作者解释：低频率下 GPU% 看起来高，不代表功耗高，**没给数字** |
- Fresco 的结论很关键：**省掉的大头是 GPU 缩放（scaler）开销，不是解码**。降低 scaler 质量（不用 ewa/spline，改 bilinear）就能让 GPU 功耗降 42–65%，而且不丢帧。
- **Wallpaper Engine 的 CPU/GPU 实测没有找到可信数字，这里不编。**

### 2.4 让 4K 接近零 CPU 的做法
1. 用硬解加零拷贝 vo（d3d11va+gpu-next / videotoolbox / vaapi+dmabuf）。
2. **在源文件上动手**：转码或预缩放到显示器的原生分辨率，这样运行时完全不需要缩放；默认帧率降到 30fps（【推断】解码和合成负载近似减半）。
3. 低档 scaler：`--scale=bilinear --dscale=bilinear --cscale=bilinear`，关掉 `--deband`、`--interpolation`。
4. 刷新同步：Wayland 靠帧回调；Win/mac 用默认 `video-sync=audio` 就行（壁纸本来就静音）。**不要开 display-resample**，它会额外做插帧。
5. 静音并关掉音频输出（`--aid=no` 或 `--ao=null`）。
6. 被遮挡或全屏时要**真正停掉渲染**，而不只是降帧（见 §3）。

---

## 3. 暂停与省电（核心）

### 3.1 Lively 的实现（Windows 标杆）【源码】`lively/src/Lively/Lively/Core/Suspend/Playback.cs`
- 用 `DispatcherTimer` 轮询，默认 **500 ms**（`SettingsModel.cs:173` 的 `ProcessTimerInterval = 500 // reduce to 250 for quicker response`）。
- 系统状态优先判断（`IsPauseDueToSystemState`，约 400–420 行）：屏保、锁屏（`SystemEvents.SessionSwitch` 的 `SessionLock/Unlock`，:64-99）、远程桌面（`RemoteConnect/Disconnect`）、电池（`PowerUtil.GetACPowerStatus()==Offline`）、省电模式（`GetBatterySaverStatus()`）。
- 四种算法（:130-145）：
  - `foreground`：前台窗口 `GetForegroundWindow` 加上窗口矩形和显示器矩形的比较；
  - `all`：任意一个可见窗口盖住了工作区；
  - `grid`：按网格计算覆盖率，默认 tile **50 px**，阈值 **0.05**（`SettingsModel.cs:169-170`），还有调试叠加层 `Views/WindowCoverageDebugOverlay.xaml.cs`；
  - `gamemode`：`SHQueryUserNotificationState`，只有在 `QUNS_RUNNING_D3D_FULL_SCREEN` 时才暂停（:339-352）。
- 按显示器暂停：`DisplayPause.perdisplay`（:192, :244）。注意 **span 模式下这套逻辑会失效**（见上面 signalrgb 的实测）。

### 3.2 Windows 推荐方案（在 Lively 基础上改成事件驱动）
- 全屏/遮挡：用 `SetWinEventHook(EVENT_SYSTEM_FOREGROUND / EVENT_OBJECT_LOCATIONCHANGE / EVENT_SYSTEM_MINIMIZESTART…)` 触发重新计算，再加一个 1 s 的兜底轮询。判定时要排除 `DwmGetWindowAttribute(DWMWA_CLOAKED)` 为真的窗口（其他虚拟桌面或 UWP 隐藏的窗口），排除 Lively 那份 `WindowClassExclusions.cs` 里列的类名（WorkerW、Progman 等）。`SHQueryUserNotificationState` 只作为 D3D 独占全屏的补充信号。【API 来自文档，组合方案属于推断】
- 电源：`RegisterPowerSettingNotification(GUID_ACDC_POWER_SOURCE, GUID_POWER_SAVING_STATUS)`，事件驱动，不用轮询。
- 锁屏/远程：`WTSRegisterSessionNotification` 处理 `WM_WTSSESSION_CHANGE`；`GetSystemMetrics(SM_REMOTESESSION)`。
- 显示器休眠：`GUID_CONSOLE_DISPLAY_STATE`。
- **暂停的力度**分几级：mpv `pause=yes`（解码停了，但窗口和交换链还在）；隐藏窗口；`NtSuspendProcess` 挂起子进程（Lively 的 `ExtPrograms.cs` 里有 SuspendCnt，**对 mpv 具体用了哪一级没有逐行核实**）；长时间暂停就直接销毁播放器，只留最后一帧截图当静态壁纸（GPU 显存也释放掉）。

### 3.3 macOS 推荐方案
- 遮挡：【源码】LuminWallpaper `desktop_integration_macos.mm:142-230` 用 `CGWindowListCopyWindowInfo` 枚举窗口，按像素累加覆盖面积和阈值比较，还用 “Dock 拥有、名字为空的窗口” 来识别 Mission Control（这是启发式，属于脆弱逻辑）。
- 更好的做法【文档 API，推断组合】：
  - 壁纸窗口自己监听 `NSWindowDidChangeOcclusionStateNotification` / `occlusionState`，这是系统给的事件，零轮询；不过对 desktop level 窗口是否可靠**需要实测**。
  - 全屏 App：`NSWorkspaceActiveSpaceDidChangeNotification` 之后再用 CGWindowList 检查当前 Space 是不是全屏 Space。
  - 锁屏和睡眠：`com.apple.screenIsLocked/Unlocked`（DistributedNotificationCenter）、`NSWorkspaceScreensDidSleep/WakeNotification`。
  - 电源：`IOPSNotificationCreateRunLoopSource`，加上 `NSProcessInfo.isLowPowerModeEnabled`（macOS 12+）。
  - 热状态：`NSProcessInfoThermalStateDidChangeNotification`，状态 ≥ serious 时自动降帧或暂停。

### 3.4 Linux 参考（简述）
- Hanabi `autoPause.ts:43-46`：最大化/全屏（任一显示器或全部显示器）、焦点、电池、MPRIS 播放中都会触发暂停，监听的是 Meta.Window 信号，属于事件驱动。
- mpvpaper：`update_mpv_pause_state` 把 list/auto/user/full 四个暂停标志做 OR 合并（`src/main.c:284-307`），通过 `wlr-foreign-toplevel` 监控全屏。
- linux-wallpaperengine：`Render/Drivers/Detectors/{X11,Wayland}FullScreenDetector.cpp`，每 250 ms 检查一次（`WallpaperApplication.cpp:33`）。
- Fresco：`daemon/fullscreen.rs`、`x11_fullscreen.rs`（X11 上是轮询），`battery.rs`。
- **把多个暂停原因做 OR 合并**（mpvpaper 的模式）很值得照搬：每个原因单独一个标志位，只有合并结果发生变化时才下发 pause/play，避免状态抖动。

---

## 4. 网页与 Shader 壁纸
- 系统 WebView 和捆绑浏览器的对比：**没有找到同一台机器上 WebView2/WKWebView/WebKitGTK 和 CEF/Electron 的对照实测**。能拿到的只有 MyGo 文档里的数字：macOS 上 WKWebView 的 hello-world 合计约 62 MB【文档】。Lively 用 WebView2 和 CEF 做网页壁纸，它的 Wiki 只说资源占用“取决于壁纸设计”【文档】。建议自己做对照：同一个 WebGL 页面、1080p、30fps，测 PSS 内存、CPU 和 GPU 功耗。
- 结论【推断】：WebView2 和 Edge 共享运行时（Evergreen），WKWebView 是系统组件，两者都不会增加安装包体积，也正好符合 MyGo 的约束。Electron/CEF 每个应用都要多带一个 Chromium（体积上百 MB），**不建议用**。
- 网页壁纸的暂停：Lively 是挂起整个播放器进程，而不是给页面发事件（signalrgb 提交里的结论【实测】）。对 WebView2 可以用 `ICoreWebView2_3::TrySuspend`，WKWebView 可以把它从窗口上摘掉或隐藏，让 rAF 停下来【文档 API，未实测】。
- Shader：建议原生 D3D11 或 Metal 的全屏 pass（MyGo 原生 UI 本身就用 Metal/D3D11/GL，可以复用它的设备）。性能控制：
  - 帧率上限：可选 15/30/60，默认 30；
  - 渲染分辨率缩放：0.5–1.0，最后再放大到全屏；
  - 时间轴静止时不 present；
  - 被遮挡时停止计时。
  - OpenWallpaper 用 WASM + AOT 执行场景脚本（README），这个思路可以借鉴。

---

## 5. 多显示器、DPI、热插拔、虚拟桌面
- Windows：
  - 进程设为 Per-Monitor-V2 DPI aware；每块显示器一个子 HWND，坐标用 WorkerW 的客户区坐标（虚拟屏幕原点可能是负数）；
  - 监听 `WM_DISPLAYCHANGE`、`WM_DPICHANGED`；
  - explorer 重启时会广播 `RegisterWindowMessage("TaskbarCreated")`，收到后重新做一遍 WorkerW 挂载【推断，常见做法】；
  - Windows 的虚拟桌面不影响 WorkerW（壁纸本来就是全局的），但遮挡判定要排除 cloaked 窗口；
  - span 模式下不能按显示器暂停，所以默认用 per-display。
- macOS：
  - 每个 `NSScreen` 一个窗口；监听 `NSApplicationDidChangeScreenParametersNotification` 处理热插拔和分辨率变化；
  - `backingScaleFactor` 决定渲染缓冲区尺寸；
  - Space 由 `CanJoinAllSpaces` 处理；“每个 Space 一张不同壁纸”这个需求**没找到成熟的开源实现**。
- 多屏同一个视频：Fresco 支持同步播放（README）。如果分辨率相同，可以考虑只解码一路、多路共享输出【推断】。
- 断开的屏幕：OpenWallpaper 会让对应屏幕的渲染休眠，不消耗资源（README）。

---

## 6. 开源项目一览
| 项目 | 是否找到 | 关键路径和亮点 |
|---|---|---|
| Lively (rocksdanister/lively) | 是，C#/Win | `Core/WinDesktopCore.cs`（0x052C、24H2 分支）、`Core/Suspend/Playback.cs`（4 种暂停算法）、`Core/Wallpapers/VideoMpvPlayer.cs`（mpv 参数）、`Helpers/SystemIdleUtil.cs` |
| mpvpaper (GhostNaN) | 是，C/wlroots | `src/main.c`：render API、帧回调、暂停标志合并、foreign-toplevel |
| linux-wallpaperengine (Almamu) | 是 | `Render/Drivers/Detectors/*FullScreenDetector.cpp`、`MPV/GLPlayer.cpp`、PulseAudio 播放检测 |
| Hanabi (jeffshee/gnome-ext-hanabi) | 是 | GNOME Wayland 的唯一实用方案：Shell 扩展 + 子进程 + Clutter.Clone |
| Hidamari (jeffshee) | 是 | Python + **VLC**（`player/video_player.py:16,120`），不是 mpv |
| Komorebi | 这次 clone 的是社区维护的 Komorebi-Fork（原仓库现状未核实） | `screen.py:58` 的 DESKTOP type hint，只支持 X11 |
| Plash | **已闭源** | 没有源码可引用 |
| ScreenPlay (Kelteseth) | 主仓库在 GitLab，**这次没能拉取源码** | Qt/QML 实现，跨 Win/mac/Linux；暂停实现**未核实** |
| Fresco | 是：DibbayajyotiRoy/Fresco，Rust+GTK4+libmpv，**只支持 Linux**（2026-06 创建） | `capability.rs`（后端探测）、`config.rs::select_hwdec`、`battery.rs`、按显示器的全屏暂停、功耗实测 |
| OpenWallpaper | 有**两个同名项目**：mechakotik/openwallpaper（Wayland，WASM 场景，libmpv，`wallpaperd/src/video.c`）；githander/OpenWallpaper（C# Windows 视频壁纸，2021 年后停更）。**需要跟用户确认指的是哪个** | — |
| LuminWallpaper (jensroth-git) | 是，MIT C++ SDK | `src/platform/{windows,macos,linux}/desktop_integration_*`，支持 24H2，有遮挡比例计算，适合直接对照移植到 Go |

---

## 7. 性能 checklist（目标值属于【建议目标】，不是实测）
**启动**
- [ ] 常驻进程只有 Go 主控加一个播放器；不启动 WebView 管理 UI，设置窗口按需打开、关掉就销毁。目标：从冷启动到第一帧 < 1 s，常驻内存 < 60 MB（参考 MyGo 原生 UI 约 44 MB【文档】）。
- [ ] 先显示静态首帧，再起播放器，避免黑屏。
- [ ] hwdec 探测结果缓存起来（Fresco 用 `Once` 只记录一次）。

**空闲（静态或暂停态的桌面）**
- [ ] 不轮询，全部事件驱动；兜底轮询 ≥ 1 s。目标：0% CPU、0 次唤醒/秒级（macOS 用 Activity Monitor 的 Energy、Windows 用 `powercfg /energy` 验证）。

**播放中**
- [ ] 确认 `hwdec-current` 不是 `no`，用的不是 `*-copy`。
- [ ] 源文件是原生分辨率、30fps；低档 scaler。
- [ ] 目标：4K30 时 CPU < 2–3%（一般参照 OBS 硬解后 2–3% 的数字），iGPU 上 GPU 功耗接近 Fresco Reduced 档（4K60 约 1.6 W）。
- [ ] 静音且不初始化音频设备。

**暂停后**
- [ ] 按显示器独立判断；触发后 ≤ 500 ms 内停下来（Lively 默认值）。
- [ ] 暂停超过 N 秒（例如 30 s）就挂起或销毁播放器，释放显存，只留截图。目标：暂停期间 GPU 0%（参考 signalrgb 的 per-display 实测为 0%）。
- [ ] 恢复播放时不重新加载文件，或者最多 < 300 ms。

---

## 8. 推荐技术栈与 MVP 路线
| 平台 | 壳 | 集成 | 视频 | 暂停信号 |
|---|---|---|---|---|
| Windows | MyGo + x/sys/windows | 0x052C/WorkerW，24H2 用 layered 子 HWND | 子进程 mpv `--wid` + `d3d11va`/`gpu-next d3d11` + IPC（阶段二改为 purego libmpv render 或 MF） | WinEventHook + SHQUNS + PowerSetting + WTS |
| macOS | MyGo + purego objc | NSWindow `kCGDesktopWindowLevel` + CanJoinAllSpaces/Stationary，每屏一个窗口 | **AVPlayerLayer**（VideoToolbox，系统零拷贝）；mpv 作为备选，用来覆盖格式 | occlusionState + NSWorkspace + IOPS + LowPower + thermal |
| Ubuntu | 同上 | X11 DESKTOP 窗口 / KDE layer-shell / GNOME Wayland 用扩展代理（Hanabi 模式），不行就降级为静态 | 子进程 mpv / mpvpaper | 扩展通过 D-Bus 上报 |

**路线**
1. M1（4–6 周，Windows）：WorkerW 集成（包含 24H2 分支）、多屏、mpv 子进程、按显示器的遮挡/全屏/电池/锁屏/RDP 暂停、静态首帧；建立功耗基准（Intel iGPU 加一张 N 卡，4K30/4K60）。
2. M2（4–6 周，macOS）：desktop level 窗口、AVPlayerLayer、Space 和热插拔、occlusion 暂停；在 Apple Silicon 上用 `powermetrics` 做基准。
3. M3：网页壁纸（WebView2/WKWebView，带暂停）和 Shader（D3D11/Metal，带帧率上限和分辨率缩放）。
4. M4（Ubuntu）：GNOME X11 + KDE layer-shell，GNOME Wayland 扩展。

**主要风险**
- 0x052C 是未公开文档的消息，24H2 已经改过一次层级，以后的 Windows 版本可能还会变。需要启动自检，失败就降级成静态壁纸，并对每个 Windows 版本做回归测试。
- purego 有回调上限，结构体传参也受限。render API 方案工作量大，子进程方案又会多一份进程内存。
- GNOME Wayland 依赖 Shell 扩展，GNOME 每个大版本都可能需要适配。
- macOS 上 occlusionState 对 desktop level 窗口是否可靠还没验证。
- WebView 和 CEF 的对比、Wallpaper Engine 的资源占用，目前**没有可信实测**，需要自己测。
