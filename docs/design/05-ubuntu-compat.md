# 05 Ubuntu / Linux 兼容（仅作兼容参考，不是 MVP 目标）

> 目的：证明 02 章的接口（`desktop.Host`、`Caps.ExternalSurfaceOnly`、`render.Renderer`）能装下 Linux 的各种实现路径，并且**不需要 cgo**。Linux 版本排在 MVP 之后。

## 5.1 会话矩阵（2026-10）
| 发行版 / 桌面 | X11 会话 | Wayland 会话 | 依据 |
|---|---|---|---|
| Ubuntu 24.04 LTS / GNOME 46 | 有（"Ubuntu on Xorg"） | 默认 | 【文档】Ubuntu Discourse 62538 的背景说明；Hanabi PR #218 的测试环境是 “GNOME 46 (Ubuntu 24.04.4 LTS, x11)” |
| Ubuntu 25.04 / GNOME 48 | 有 | 默认 | 【推断】GNOME 49 才默认禁用 X11 |
| Ubuntu 25.10 / GNOME 49 | **已移除**（GDM 只提供 Wayland 会话；X11 应用通过 XWayland 运行） | 唯一选项 | 【文档】Ubuntu Discourse 62538 |
| GNOME 50 | mutter 的 X11 后端代码已删除 | 唯一选项 | 【文档】mutter NEWS 50.alpha “Drop the X11 backend !4505”；Phoronix 2025-11-05 |
| KDE Plasma 6 | 有 | 默认 | 【文档】 |

结论【推断】：GNOME 的 X11 路径只对 24.04 LTS（支持到 2029 年）和 25.04 还有意义。从 26.04 LTS 起，GNOME 只能走 Wayland 扩展路线。

## 5.2 GNOME X11（46 / 48）
- 实现：建一个顶层窗口，设置 `_NET_WM_WINDOW_TYPE_DESKTOP`，以及 `_NET_WM_STATE_BELOW/STICKY/SKIP_TASKBAR/SKIP_PAGER`，铺满屏幕后用 `mpv --wid=<xid>` 嵌入。这是 Komorebi（`screen.py:58`）和 Fresco（`daemon/x11win.rs`）的做法【源码】。
- **box 实测【实测】**：原型 `prototype/proto_linux.go` 用纯 Go 的 xgb 建窗口，`CGO_ENABLED=0` 编译。在 Xvfb 1280×720 下：
  - `xprop` 确认窗口类型和状态都设置上了；
  - `xwininfo -children` 确认 mpv 的子窗口（`"x11" "mpv"`）嵌在我们的窗口里；
  - 根窗口截图是彩色测试画面（不是黑屏）；
  - mpv JSON IPC 统计正常，循环正常，掉帧 0。
  - 限制：box 上**没有窗口管理器**，所以 mutter 对 DESKTOP 类型窗口的层级处理、与 DING 桌面图标扩展的上下关系，都**没有验证**。
- 按显示器分别建窗口；坐标从 RandR 获取（xgb 有 `randr` 子包【源码】`jezek/xgb@v1.3.1/randr`）。
- 暂停信号：`_NET_ACTIVE_WINDOW` 加上 `_NET_WM_STATE_FULLSCREEN/MAXIMIZED_*`；遮挡计算和 Windows 的网格算法一样【推断】。
- 许可证：mpv 的 LGPL 构建会禁用 X11 VO【文档】mpv Copyright。所以 Linux 版用发行版自带的 mpv（GPL，作为独立进程调用；11 章 ADR-004 的同一逻辑），不由我们分发【推断】。

## 5.3 GNOME Wayland（46 / 48 / 49 / 50）
mutter **没有** wlr-layer-shell，普通客户端无法把自己放到壁纸层。唯一可行的路线是写 GNOME Shell 扩展。Hanabi 的实现要点【源码】`gnome-ext-hanabi`（main 分支，TypeScript，2026-10-09 提交 c18dd76）：

| 要点 | 位置 | 说明 |
|---|---|---|
| 以 “Wayland 客户端子进程” 方式启动渲染器 | `src/waylandSubprocess.ts:47` `Meta.WaylandClient.new_subprocess(global.context, launcher, argv)` | 由 mutter 直接拉起，这样扩展可以确认窗口确实属于自己的渲染器 |
| 注入背景 | `src/gnomeShellOverride.ts:111-112` override `Background.BackgroundManager.prototype._createBackgroundActor` | 每个背景 actor（每块显示器一个，也包括概览视图里的那些）后面挂一个 LiveWallpaper |
| 显示画面 | `src/wallpaper.ts:184` `new Clutter.Clone({source: renderer})` | 克隆渲染器窗口的 actor，不需要拷贝像素 |
| 隐藏渲染器窗口 | `gnomeShellOverride.ts:178-244`，按 `meta_window.title` 是否包含 `APPLICATION_ID` 过滤（`constants.ts:21`：`io.github.jeffshee.HanabiRenderer`） | 从 Alt-Tab、概览、工作区缩略图里去掉 |
| 渲染器 | `src/renderer/renderer.ts:43-75`：GTK4 + GStreamer `GstPlay`（GStreamer 1.20+），没有就回退到 `GtkMediaFile`；可选 `clappersink`、VA 解码器 | |
| 控制 | `src/dbus.ts:48-52`：渲染器暴露 D-Bus 方法 `setPlay`/`setPause` | |
| 自动暂停 | `src/autoPause.ts`：最大化或全屏（任一屏 / 全部屏）、电池（`UPowerGlib`，第 45 行的 `PauseOnBatteryModule`） | |
| 版本支持 | README：`javascript` 分支支持 GNOME 45–50（X11 和 Wayland），维护模式；`main` 分支是 TypeScript，只支持 GNOME 50+ 和 Wayland | 【文档】|

**对我们的含义**【推断】：
- 需要单独发一个 GNOME 扩展（JS/TS，按 GNOME 版本分支维护）。扩展只做三件事：用 `Meta.WaylandClient` 启动渲染器、Clone 它的窗口、隐藏它。
- 渲染器可以**直接用 mpv**：`mpv --title=<我们的ID> --wayland-app-id=<ID> --vo=gpu-next --hwdec=vaapi …`。扩展按标题识别它，Go 守护进程通过 mpv IPC 控制暂停。这样 Go 一侧完全不用写 Wayland 代码。对应接口是 `Caps.ExternalSurfaceOnly=true`、`Surface.Native()=0`（02 章 2.7）。
- Go 守护进程和扩展之间用 D-Bus 通信（扩展负责报告显示器和可见性事件）。纯 Go 的 D-Bus 库（如 godbus）**选型待定**。
- 风险：GNOME 每个大版本都可能改内部 API（Hanabi 为 47、48 都单独开过分支），维护成本高。所以 Linux 版晚于 MVP 是合理的。

## 5.4 KDE Plasma 6（X11 / Wayland 同一路线）
- **推荐：写 Plasma 壁纸插件**（QML）。
  - Plasma 6 的要求：`ui/main.qml` 的根元素是 `WallpaperItem`（`import org.kde.plasma.plasmoid`），设置通过 `root.configuration` 读写；`metadata.json` 必须有 `"X-Plasma-API-Minimum-Version": "6.0"` 和 `"KPackageStructure": "Plasma/Wallpaper"`；安装到 `~/.local/share/plasma/wallpapers/<Id>/`【文档】api.kde.org WallpaperItem、develop.kde.org “Porting Plasmoids to KF6”；`KPackageStructure` 缺失会导致静默黑屏，这一点来自第三方的踩坑记录（TuxPapers 博客）。
  - 已有的实现：plasma-smart-video-wallpaper-reborn，支持按显示器检测窗口、最大化或全屏时暂停、电池阈值、锁屏【文档】README（**源码未 clone 核对**）。
- **为什么不用 layer-shell**：资料互相矛盾。有第三方说 KWin 不支持 wlr-layer-shell，但同一篇文章又描述了 BACKGROUND 层 surface 被压在 Plasma 桌面容器下面、导致桌面图标消失的现象。无论哪种说法成立，结论都是 **KDE 上不能走 layer-shell**【文档，第三方；KWin 实际的协议支持情况待核实，Q-L3】。
- 和守护进程的集成【推断】：插件内部用 QtMultimedia 播放（Plasma 6 / Qt6 的 FFmpeg 后端，能否走 VA-API 待核实）。守护进程只负责写配置和切换壁纸，方式是 D-Bus `org.kde.PlasmaShell.evaluateScript`（桌面脚本 API）【文档，未在本轮核对 API 细节】。暂停逻辑放在插件内部完成，所以 07 章的状态机在 KDE 上**只有一部分适用**。

## 5.4.1 第 13 轮补充：KWin layer-shell 与 KDE 播放后端
- **KWin layer-shell**：KWin 实现 `zwlr_layer_shell_v1` v5（`src/wayland/layershell_v1.cpp:22`）。`LayerShellV1Window::belongsToLayer` 把 BACKGROUND 映射到 `DesktopLayer`、BOTTOM 映射到 `BelowLayer`（`src/layershellv1window.cpp:216-222`）；Plasma 桌面本身是 PlasmaShellSurface `Role::Desktop`，也落在 `DesktopLayer`（`src/wayland/plasmashell.cpp:176`、`src/window.cpp:553-554`）。两者同层、层内顺序不固定，所以 mpvpaper 等 layer-shell 方案在 Plasma 上会出现盖住图标或被桌面盖住的情况【源码，KWin cd0c253，2026-10-21】。**结论：KDE 继续走壁纸插件（关闭 Q-L3）。**
- **播放后端**：Qt Multimedia（FFmpeg 后端）可用 `QT_FFMPEG_DECODING_HW_DEVICE_TYPES=vaapi` 硬解，但 VAAPI 下硬件纹理转换默认关闭（帧回读到 CPU），需 `QT_XCB_GL_INTEGRATION=xcb_egl` 才能开启【文档：https://doc.qt.io/qt-6/advanced-ffmpeg-configuration.html 】。按 box 实测，回读会额外增加 19–41% 解码开销【实测，软解参考】。MpvQt（KDE，LGPL，`MpvAbstractItem` = QQuickFramebufferObject + libmpv render_gl）更可能零拷贝【推断】，但需要编译 C++ QML 插件，打包成本高；Smart Video Wallpaper Reborn 的草稿 PR #133 正在加可选的 MpvQt 后端【issue】。**推荐**：默认 Qt Multimedia（纯 QML，便于分发），MpvQt 作为性能选项；真机对比见 bench/DEVICE_HANDOFF.md 6.3。
- **GNOME D-Bus 接口（Q-L6）**：见 [linux-dbus/README.md](linux-dbus/README.md)，godbus 纯 Go 已在 box 会话总线验证【实测】。

## 5.4.2 第 17 轮补充：GNOME 扩展原型与 mutter API 差异
- 最小扩展见 [gnome-ext/README.md](gnome-ext/README.md)。mutter 46–48 用 `new Meta.WaylandClient(context, launcher)` + `spawnv()`，并且有 `hide_from_window_list()`/`make_desktop()`；**mutter 49 起改为 `new_subprocess()`，这两个函数被移除**【源码，mutter `src/meta/meta-wayland-client.h` 各版本标签】。所以 GNOME 49/50 要沿用 Hanabi 的 `get_window_actors` 覆盖方案，维护成本更高（新增 Q-L7）。
- box 上用 gjs 1.82.3 对接 Go 守护进程，代理、信号、4 个方法全部打通【实测】；Shell 内部逻辑（Meta/Clutter）要上真机。

## 5.5 wlroots / Hyprland（一行）
用 wlr-layer-shell 的 BACKGROUND 层，加上 libmpv render API 和 EGL（mpvpaper `main.c:579,890,949`【源码】）；直接调用外部 mpvpaper 进程即可。

## 5.6 无 cgo 的 Go 库
| 库 | 用途 | 现状 | 依据 |
|---|---|---|---|
| `github.com/jezek/xgb` v1.3.1 | 纯 Go 的 X11 协议，包括 randr、composite、shape 等扩展 | 已用在原型里，box 实测可用 | 【源码】【实测】 |
| `github.com/rajveermalviya/go-wayland/wayland`（伪版本 2023-01-30） | 纯 Go 的 Wayland **客户端**：stable 里有 xdg-shell、viewporter、presentation-time；staging 里有 content-type、drm-lease；**不包含 layer-shell**，但自带的 `go-wayland-scanner` 可以从 XML 生成 | README 写的是 “pure Go implementation”，依赖只有 `golang.org/x/sys`；最后更新是 2023 年 | 【源码】module cache |
| purego（Linux amd64/arm64） | 用 dlopen 调用 libmpv、libEGL、libwayland-egl | 一级支持平台，`CGO_ENABLED=0` 可用 | 【文档】purego README |

关键限制【推断】：纯 Go 的 Wayland 客户端只能提交 `wl_shm` CPU 缓冲区。要做 GPU 零拷贝视频，必须用 EGL 或 dmabuf，也就是通过 purego 调用 libEGL 和 libmpv。这和 06 章 “不回读到内存” 的原则一致。所以在 Wayland 上我们**不自己画**，而是交给 mpv、mpvpaper 或 GNOME 扩展里的渲染器去画。Go 一侧只用 xgb 和 D-Bus 做控制。

## 5.7 对架构的要求（回写 02 章）
- `Caps.ExternalSurfaceOnly`：GNOME Wayland（由扩展托管）和 KDE（由插件托管）时为 true。此时 `Renderer` 只是一个远程控制代理，`SetLevel` 可能只支持 L0/L1。
- `Host.Events()` 的事件来源可能是扩展或插件发来的 D-Bus 信号，而不是本地探针。
- 包格式（10 章）在 Linux 上只支持 video 和 image；web 类型需要 WebKitGTK 宿主，以后再做。
