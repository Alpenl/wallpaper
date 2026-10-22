# 动态壁纸：可行性深挖与风险清单（Windows / macOS 优先）

> **历史研究原文**：来源日期和结论按原报告保留，本轮未重跑旧测试或重核外部数字。当前使用范围、后续修正与导航见 [调研索引](README.md)。

> 2026-10-20（PDT）。依据标注：**【源码】** 是本次 clone 后读过的代码；**【文档】** 是官方或项目文档；**【实测】** 是第三方或本次 box 上的实际测试；**【推断】** 未经验证。上一份报告见 `dynamic_wallpaper_tech_research.md`。
> 本次 box 是 Linux 机器，**只能交叉编译，不能运行 Windows/macOS 二进制**。所有运行时行为都标为待原型实测。

---

## 1. MyGo 实际验证

### 1.1 源码结论（egoist/mygo，`git clone --depth 1`，go.mod 要求 go 1.27.1，依赖 `github.com/ebitengine/purego v0.11.1`）
| 问题 | 结论 | 依据 |
|---|---|---|
| 能拿到原生句柄吗 | **能**。`func (w *Window) NativeHandle() uintptr`，macOS 返回 NSWindow*，Linux 返回 GtkWindow*，Windows 返回 HWND | 【源码】`window.go:1093-1095` |
| 无边框 | 可以。`WindowOptions.Frameless`，另有 `SkipTaskbar`、`Transparent`、`DisableShadow`、`Hidden` | 【源码】`window.go:80-150` |
| 子窗口 | `WindowOptions.Parent *Window` 的含义是 "child window that stays on top of it"，也就是 **owned 窗口，不是 WS_CHILD**，**不能直接指定 WorkerW 当父窗口** | 【源码】`window.go:~150` |
| SetParent 到 WorkerW | MyGo 没有提供这个 API，要**自己拿 HWND 调 `user32!SetParent`**（原型已经写好并编译通过，见 1.2） | 【源码】+【推断】运行效果待测 |
| **潜在冲突** | MyGo 的 Windows 后端用 **DirectComposition** 画窗口：`internal/windows/compositor.go:32,122`（`DCompositionCreateDevice`），`:321` 用 `IDCompositionDevice::CreateTargetForHwnd(c.hwnd, topmost=1)`；`win32.go:316` 用了 `WS_EX_NOREDIRECTIONBITMAP`。顶层窗口被 SetParent 变成子窗口后，DComp target 是否继续有效、会不会黑屏、需不需要重新建 target，**都必须实测** | 【源码】+【推断】 |
| NSWindow level / collectionBehavior | MyGo 内部本来就通过 purego/objc 调 `setLevel:` / `setCollectionBehavior:`（`internal/darwin/window.go:189-191,240`），但**公开 API 只有 AlwaysOnTop**。desktop level 要我们自己对 NativeHandle 发 objc 消息（原型已写） | 【源码】 |
| objc 类与回调 | MyGo 用 `objc.RegisterClass(name, super, protocols, ivars, methods)` 在运行时创建 objc 子类（`internal/darwin/objc.go:503`），所以**不用 cgo 也能写 delegate 子类**（例如 `windowDidChangeOcclusionState:`）。限制见 1.3 | 【源码】 |

### 1.2 交叉编译实测（本次 box，Linux amd64，`CGO_ENABLED=0`，`-ldflags="-s -w"`）
原型源码在 `docs/research/wpproto_src/`（`main.go`、`desk_windows.go`、`desk_darwin.go`、`desk_other.go`）。内容：
- 建一个 Frameless + SkipTaskbar 窗口，加载一个纯色页面，然后调用 `attachDesktop(win.NativeHandle())`。
- Windows：`SendMessageTimeout(Progman,0x052C,0xD,0x1)`；检测到 `WS_EX_NOREDIRECTIONBITMAP` 就走 24H2 分支，否则走 EnumWindows 分支；最后 `SetParent`。只用了标准库 `syscall`。
- macOS：purego 调 `CGWindowLevelForKey(kCGDesktopWindowLevelKey=2)` 取层级，然后 `setLevel:`、`setCollectionBehavior:`（CanJoinAllSpaces|Stationary|IgnoresCycle）、`setIgnoresMouseEvents:`；接着 **dlopen AVFoundation，创建 AVPlayer 和 AVPlayerLayer 并挂到 contentView.layer 上**。

| 目标 | 结果 | 大小 |
|---|---|---|
| windows/amd64 | **编译成功** | 7,065,088 B（约 6.7 MiB） |
| darwin/arm64 | **编译成功**（`go vet` 无输出） | 6,890,770 B（约 6.6 MiB） |
| linux/amd64 | 编译成功 | 7,004,423 B |

【实测】只说明**能编译**，体积和官方说的约 7 MB 一致。**运行时行为（SetParent 后能不能显示、AVPlayer 能不能出画面）没有验证**。另外原型里 `stringWithUTF8String:` 传的是 Go 字符串，purego objc 会不会把它正确转成 C 字符串**待验证**，必要时改成手工生成以 NUL 结尾的 `*byte`。第一次编译时 `golang.org/x/sys/windows` 不在 mygo 的 go.sum 里，所以改用了标准库 `syscall`。

### 1.3 purego 调 AVFoundation 的限制
- 【文档/源码】MyGo 架构文档写明：`SyscallN` 不能传 float 或结构体；`RegisterFunc` 走反射，要远离热路径；`NewCallback` 每进程上限约 2000 个且不释放。
- AVFoundation 里常用的 `setFrame:`（CGRect）、`CMTime`（结构体）、`setRate:`（float）都要用 `RegisterFunc` 或 purego objc 的结构体支持。**这些调用都是低频的（只在设置时调），没有性能问题**【推断】。
- 循环播放：要么用 KVO/NotificationCenter 回调（`AVPlayerItemDidPlayToEndTimeNotification`），要么用 `AVPlayerLooper`（不需要回调，推荐）。
- 回调必须在**主线程**处理 AppKit；MyGo 的窗口操作本来就在主线程（"blocking calls that are safe from any goroutine"）。

---

## 2. Windows 逐项核实

### 2.1 各版本差异
| 版本 | 结构 | 依据 |
|---|---|---|
| Win10 / Win11 ≤23H2 | 0x052C 之后，WorkerW 是**顶层**窗口，作为含 SHELLDLL_DefView 那个顶层窗口的下一个同级窗口 | 【源码】Lively `WinDesktopCore.cs` 的 Spy++ 注释 |
| Win11 24H2（26100） | Progman 带 `WS_EX_NOREDIRECTIONBITMAP`；DefView 是 layered 子窗口；WorkerW 是 Progman 的子窗口；按微软建议要自建 layered 子窗口。原因是**为了支持 HDR 壁纸** | 【源码】同文件注释；【文档/issue】lively #2746 作者回复 |
| 24H2 早期 | 正式版有 bug：**必须开启桌面图标才能正常工作**，KB5050009（2025-01，26100.2894）修复 | 【issue】lively #2374、discussions #2464 |
| 25H2（26200） | **没找到结构变化的官方说明**；有用户报告 “Explorer 刷新后壁纸消失”（26200.8457） | 【issue】lively #3195（open）。是否有结构变化**待实测** |

### 2.2 已知问题（来自 issue）和规避办法
- **Explorer 反复重载桌面导致 WorkerW 被销毁、循环重建**：#2777（2025-06/07），作者说是 Windows 更新加上自己强制刷新桌面的方式引起的，后来在新版本修了；#2746 的触发原因是 WinDynamicDesktop 等调用系统 API 换壁纸。规避：
  - 监听 WorkerW 销毁（用 `EVENT_OBJECT_DESTROY` WinEventHook 或定期 `IsWindow`）和 `TaskbarCreated` 消息，然后**退避重挂**（限制重试次数，失败就降级成静态壁纸）；
  - 不要频繁强制刷新桌面；
  - 检测到冲突软件时给出提示。
- **安全或桌面类软件改动层级**：#3103（360 桌面助手，日志 "Unexpected WorkerW Z-order"），作者判定为第三方问题。在中国市场这是**高风险**：360、腾讯桌面整理这类工具很常见，需要自建兼容性测试矩阵。
- **24H2 下的边框和对齐**：Lively 为 mpv 加了 `--no-border`（#2415）【源码】。
- **DPI**：WebView2 作为 WorkerW 的子窗口时 DPI 不对【源码注释】`WallpaperPluginFactory.cs:219`。
- **多显示器坐标**：WorkerW 覆盖整个虚拟屏幕，子窗口坐标要换算成 WorkerW 客户区坐标（主屏以外的屏幕可能在负坐标）。这一条**没找到具体 issue**，属于【推断】，要测。
- **HDR**：24H2 的改动就是为了 HDR 壁纸；Lively 有 `--target-colorspace-hint-mode`（gpu-next）【源码】。HDR 屏上 SDR 内容的色彩和亮度**需要实测**。
- **黑屏**：前面几种情况（WorkerW 被销毁、24H2 图标 bug）都表现为黑屏或壁纸消失。

### 2.3 mpv `--wid` 和 d3d11va 零拷贝
- 【文档】mpv 手册说 `d3d11va` 可以配合 `--vo=gpu/gpu-next --gpu-api=d3d11` 直接使用解码纹理，`d3d11va-copy` 才会拷回内存。`--wid` 只决定 mpv 把自己的子窗口挂在哪里，**不改变解码和渲染路径**，所以理论上仍然是零拷贝【推断】。需要实测确认：任务管理器里 Video Decode 引擎有负载，同时 CPU 低。
- mpv 子进程的内存和启动时间：**没有找到可信实测**，需要自己测。

### 2.4 全屏、cloak、专注助手
- `SHQueryUserNotificationState`【文档 MS Learn】：
  - `QUNS_RUNNING_D3D_FULL_SCREEN(3)` 只代表**独占全屏**；
  - `QUNS_BUSY(2)` 代表全屏应用或演示模式；
  - 结果是**全局的，不区分显示器**。
  - Lively 的 gamemode 算法**只认 3**【源码】。
- **无边框全屏游戏不会触发 3**（SoundSwitch 文档【文档/源码】，CS2 这类 flip-model 游戏），要用 “前台窗口矩形 == 显示器矩形” 来判断，同时排除 explorer、dwm、LockApp、ShellExperienceHost 等系统进程（StandbyAndTimer 的 `GameDetectionService.cs`【源码，网页查看】）。
- DWM cloak：用 `DwmGetWindowAttribute(DWMWA_CLOAKED=14)` 排除其他虚拟桌面上或被挂起的 UWP 窗口【文档】，否则遮挡判断会误判。
- **Focus Assist**：没有公开 API，只能走 WNF（未公开文档，随时可能失效）【文档】node-notification-state-ffi README。**建议不依赖**，只用 QUNS_BUSY 作为参考信号。

---

## 3. macOS 逐项核实

| 场景 | 结论 | 依据 |
|---|---|---|
| 层级 | 常见做法是 `kCGDesktopWindowLevel` 或 `-1`（phonto 用 desktop-1 = -2147483624，MacLiveEngine 也用 `-1`）；Dominic Cooney 的回答提到还需要 `orderBack:` 才能排到图标下面 | 【源码】museslabs/phonto `src/backend/macos/mod.rs`；【文档】MacLiveEngine README；SO 4982584 |
| 多 Space | 用 `CanJoinAllSpaces|Stationary|IgnoresCycle` | 【源码】LuminWallpaper、phonto |
| 全屏 Space | phonto 额外加了 `FullScreenAuxiliary`；在全屏 Space 里壁纸**是否可见、要不要可见**都需要实测（通常全屏时就应该暂停） | 【源码】+【推断】 |
| Stage Manager | WWDC22：collectionBehavior 含 auxiliary/moveToActiveSpace/**stationary**/transient 的窗口**不会被 Stage Manager 挤开** | 【文档】WWDC22 10074 |
| Mission Control | LuminWallpaper 用 “Dock 拥有、名字为空的窗口” 来识别 Mission Control，是启发式，比较脆弱 | 【源码】 |
| 外接屏热插拔 | 每块屏一个窗口，监听 `NSApplicationDidChangeScreenParametersNotification`；Vantage 宣称支持热插拔 | 【文档】Vantage README；具体实现未读 |
| 锁屏（Sonoma+） | **普通窗口盖不住锁屏**。LiveDesk 的做法是**覆盖系统 Aerial 视频文件**：14–15 在 `/Library/Application Support/com.apple.idleassetsd/Customer/4KSDR240FPS/`，需要管理员权限；26（Tahoe）在 `~/Library/Application Support/com.apple.wallpaper/aerials/videos/`。Vantage 走的是原生 `.saver` 屏保 | 【文档】LiveDesk、Vantage README。属于非公开机制，**有风险** |
| mpv `--wid` | 旧 cocoa 后端说明写着 “从命令行用会崩溃”，已被 deprecated，相关文档已删；新的 cocoa-cb 后端**不支持 wid**（Akemi，mpv 维护者）；2026-08 有一个重新启用 wid 的 PR #17221（CI 通过，**是否合并未确认**）；mpv-examples 建议用 render API | 【文档/issue】mpv-examples #29、mpv #10406、提交 9618aa3、PR #17221 |
| 结论 | **macOS 不用 mpv 子进程**。视频走 **AVPlayerLayer**（VideoToolbox 硬解，系统合成），phonto、MacLiveEngine、LiveDesk 都是这个路线；需要更多格式（例如 VP9/AV1 webm）时，先离线转码成 HEVC/H.264 | 【源码/文档】。**AVPlayerLayer 和 mpv 的 CPU 对照没有找到实测，需要自己测** |
| occlusionState 对 desktop level 窗口有没有效 | Apple 文档只说 “部分可见即 visible，全透明窗口也可能被认为可见”，**没有说 desktop level 窗口的行为**。**没找到实证**（搜索引擎给的 “有效” 结论没有来源，不采信）。已知开源项目（LuminWallpaper、MacLiveEngine）都是用 `CGWindowListCopyWindowInfo` 轮询（MacLiveEngine 每 500 ms 一次，覆盖率 >95% 就暂停），这间接说明大家不依赖 occlusionState | 【文档】【源码】；**必须原型实测** |

### 3.1 沙盒、签名、公证
- 【推断，基于 Apple 机制】：
  - desktop level 窗口和 AVPlayer 本身在 App Store 沙盒里应该没问题；
  - **覆盖 Aerial 文件、写系统目录**在沙盒里不可行，也很可能过不了审核，只适合非 App Store 版本；
  - 使用 Hardened Runtime 并公证后，**dlopen 第三方 libmpv.dylib 要求它用同一个 Team ID 签名**，否则需要 `com.apple.security.cs.disable-library-validation` 授权；dlopen 系统框架不受影响；
  - `CGWindowListCopyWindowInfo` 读取窗口标题在 10.15 以后需要屏幕录制权限，**只读 bounds 和 owner 是否需要权限要实测**。
- MyGo 自带签名和公证流程（`mygo build` 只在 macOS 上执行签名）【文档】。

---

## 4. 可行性矩阵
| 功能 | Windows | macOS | Ubuntu（参考） |
|---|---|---|---|
| MyGo 壳 + 无 cgo 编译 | ✅ 已实测编译 | ✅ 已实测编译 | ✅ |
| 挂到图标后面 | ⚠️ 有风险：0x052C 未公开，24H2/25H2 有变化，第三方软件冲突。规避：版本分支、监控重挂、降级静态 | ✅ desktop level 窗口是成熟做法；⚠️ 需要验证 `orderBack` 和点击穿透 | GNOME X11 ✅；KDE ✅（layer-shell）；GNOME Wayland ⚠️ 需要 Shell 扩展 |
| MyGo 窗口直接当壁纸载体 | ⚠️ DComp 顶层 target 加 SetParent 的兼容性未知。规避：壁纸窗口不用 MyGo 窗口，自己 `CreateWindowEx` 一个子 HWND 给 mpv/D3D | ✅ 拿到 NSWindow 改 level 就行（待实测） | — |
| 视频硬解零拷贝 | ✅ mpv d3d11va（文档）；⚠️ 待实测 | ✅ AVPlayerLayer；❌ mpv --wid | ✅ mpv vaapi/nvdec |
| 无边框全屏游戏暂停 | ✅ 前台窗口矩形加排除列表 | ✅ CGWindowList 加 Space 判断 | ⚠️ 依赖桌面环境 |
| 遮挡暂停 | ✅ 网格算法（Lively） | ⚠️ occlusionState 未验证，退回 CGWindowList 轮询 | ⚠️ |
| 电池/省电/锁屏/远程 | ✅ 文档 API | ✅ 文档 API | ✅ |
| Focus Assist | ❌ 没有公开 API，只能用 QUNS_BUSY 近似 | — | — |
| 多显示器、每屏独立暂停 | ✅（不要用 span） | ✅ 每屏一个窗口 | ✅ |
| 锁屏动态壁纸 | ❌ 不在范围内 | ⚠️ 只能靠覆盖 Aerial 文件或做屏保，非公开机制，App Store 不可行 | — |
| 网页壁纸 | ✅ WebView2（⚠️ DPI） | ✅ WKWebView | ✅ |

---

## 5. 风险清单（按影响排序）
1. **Windows 层级被系统更新或第三方软件破坏**（概率高，影响高）：做自检、重挂、降级静态，加上遥测；每个 Windows 版本回归测试。
2. **MyGo 的 DComp 窗口被 SetParent 后黑屏**（概率未知，影响高）：备用方案是自建原生子 HWND。
3. **macOS 遮挡检测不可靠**（概率中，影响中）：CGWindowList 每 0.5–1 s 轮询作为兜底，加上 Space 和全屏通知。
4. **macOS 格式覆盖**（AVFoundation 不支持 webm/VP9）：导入时转码；需要能在沙盒里转码的方案（AVAssetExportSession 只能处理它支持的输入格式，**待确认**）。
5. **purego 的回调上限和结构体调用**：在启动时集中注册，所有 delegate 共用一个类。
6. **国内安全软件冲突**：兼容性矩阵里要包含 360 和腾讯电脑管家。
7. **锁屏壁纸**：只在非 App Store 版本里提供，作为实验功能。

---

## 6. 原型实测计划

### 6.1 Windows（Win10 22H2、Win11 23H2、24H2 26100.2894+、25H2 26200；Intel iGPU 和 NVIDIA 各一台；1 屏 + 2 屏混合 DPI；一台 HDR 屏）
| 测试项 | 方法和工具 | 通过标准 |
|---|---|---|
| T1：MyGo 窗口 SetParent 到 WorkerW 后是否正常显示 | 运行 `wpproto_windows.exe`；用 Spy++ 或 WinSpy 看层级；截图 | 图标在上、壁纸在下；4 个系统版本都不黑屏 |
| T2：自建子 HWND + mpv `--wid` | `mpv --hwdec=d3d11va --vo=gpu-next --gpu-api=d3d11 --wid=` | 同上 |
| T3：零拷贝和 CPU | 播放 4K30/4K60 的 H.264/HEVC 各 10 分钟；用任务管理器 GPU 引擎、PresentMon、`hwdec-current`、typeperf 采集进程 CPU | hwdec 是 d3d11va（不是 copy）；4K30 进程 CPU 平均 <3%【目标】 |
| T4：功耗 | 用 HWiNFO 看封装功率，和静态壁纸的基线对比 | 记录增量（W），作为后续优化基准 |
| T5：mpv 子进程开销 | 启动到第一帧的时间（ETW/WPR 或日志时间戳）、Private Bytes | 记录数据；第一帧 <1 s【目标】 |
| T6：鲁棒性 | `taskkill explorer` 后重启、切换系统壁纸、显示/隐藏图标、睡眠唤醒、拔插显示器、修改 DPI、开 360 桌面助手 | 5 s 内自动恢复，或者降级成静态壁纸且不崩溃 |
| T7：暂停 | 独占全屏游戏、无边框全屏游戏（例如 CS2）、最大化窗口、Win+Tab 切到其他虚拟桌面、锁屏、RDP 连入、拔电源、开节能模式 | 每种情况都能在 ≤500 ms 内正确暂停或恢复；按屏判断；暂停后 GPU 约 0% |

### 6.2 macOS（macOS 14、15、26；Apple Silicon M 系列 + 一台 Intel；内建屏 + 外接 4K）
| 测试项 | 方法和工具 | 通过标准 |
|---|---|---|
| M1：desktop level 窗口的位置 | 运行 `wpproto_darwin`（需要签名或 `xattr -d` 去掉隔离）；分别试 level = desktop、desktop-1，加不加 `orderBack:` | 在 Finder 图标下面、系统壁纸上面；不抢焦点；点击能穿透到桌面 |
| M2：各种场景 | 多 Space 切换、全屏 App 的 Space、Stage Manager 开关、Mission Control、外接屏热插拔、合盖、锁屏 | 不闪烁，不出现在 Cmd-Tab 里；热插拔后 ≤2 s 内重建 |
| M3：AVPlayerLayer 的 CPU 和功耗 | 4K60 HEVC 10 分钟；`sudo powermetrics --samplers cpu_power,gpu_power`、Activity Monitor 的 Energy、Instruments | CPU <3%【目标】；记录 GPU 功率；作为对照，同一视频用 mpv `--hwdec=videotoolbox` 窗口模式播放（不嵌入）做对比 |
| M4：occlusionState 对 desktop level 窗口是否有效 | 用 objc.RegisterClass 写 delegate，记录 `windowDidChangeOcclusionState:`；再最大化或全屏其他窗口 | **完全覆盖时能收到 not-visible** 才算通过；不通过就用 CGWindowList 轮询（≤1 s）兜底，并测轮询本身的 CPU 开销 |
| M5：CGWindowList 的权限 | 在没有屏幕录制权限的状态下读取 bounds 和 owner | 不需要权限就能完成遮挡判断 |
| M6：签名、公证、沙盒 | `codesign --options runtime` 加 notarytool；做一个沙盒版本跑 M1–M4 | 沙盒版本核心功能正常；明确列出哪些功能只能放在非沙盒版本 |
| M7：电源和过热 | 拔电源、开低电量模式、用压力负载让机器过热 | 按策略降帧或暂停 |

### 6.3 Ubuntu（参考，只做冒烟测试）
GNOME X11 用 DESKTOP 类型窗口；KDE 6 用 layer-shell + mpv；GNOME Wayland 确认 Hanabi 扩展路线的可行性。
