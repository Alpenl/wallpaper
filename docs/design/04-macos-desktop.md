# 04 macOS 桌面集成

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

## 4.1 窗口设置
| 项 | 取值 | 依据 |
|---|---|---|
| level | 开源项目各不相同：LuminWallpaper 用 `desktop`；phonto（`-2147483624`）和 MacLiveEngine 用 `desktop-1`；LivePaper 用 `desktop+1`；Wallnetic 用 `desktopIcon-1`。**没有共识**，默认用 `desktop`，其余三种作为配置项保留，用实测决定（Q-M1） | 【源码】LuminWallpaper `desktop_integration_macos.mm:118`、phonto `src/backend/macos/mod.rs`、LivePaper、Wallnetic；【文档】MacLiveEngine README |
| collectionBehavior | `CanJoinAllSpaces|Stationary|IgnoresCycle`，phonto 另加 `FullScreenAuxiliary` | 【源码】同上 |
| 其他 | Borderless；`ignoresMouseEvents=YES`；`hasShadow=NO`；`sharingType=ReadOnly`（允许被录屏）；`activationPolicy=Accessory` | 【源码】phonto、LuminWallpaper |
| Stage Manager | collectionBehavior 含 stationary 的窗口不会挤开舞台上的窗口 | 【文档】WWDC22 10074 |
| 视频 | `AVQueuePlayer` + `AVPlayerLooper` + `AVPlayerLayer` 挂到 contentView 的 layer 上 | 【源码】phonto；【文档】AVPlayerLooper |

- 原型：[prototype/proto_darwin.go](prototype/proto_darwin.go)。用 `-level` 在 4 种层级之间切换；播放用 AVQueuePlayer + AVPlayerLooper + AVPlayerLayer（AspectFill，静音）；每次采样同时记录 `occlusionState`、CGWindowList 的覆盖率和全屏判定、CGWindowList 的调用耗时（µs）；3 次采样一致才暂停。darwin/arm64 和 amd64 都用 `CGO_ENABLED=0` 交叉编译通过，arm64 产物带 ad-hoc 签名（LC_CODE_SIGNATURE）【实测】；**还没在真机上跑过**。旧骨架 `docs/research/wpproto_src/` 保留。

## 4.2 不使用 mpv 的原因
- 旧的 cocoa `--wid` 只能在 libmpv 里用，从命令行用会崩溃，相关文档已删除（提交 9618aa3）。cocoa-cb 后端不支持 wid（维护者 Akemi 在 mpv-examples #29 的说明）。2026-08 有一个重新启用的 PR #17221，**是否合并未确认**。【文档/issue】
- 结论：macOS 用 AVFoundation。webm/VP9 这类 AVFoundation 不支持的格式，在导入时转码（06 章）。

## 4.3 场景问题
| 场景 | 现状 | 依据 |
|---|---|---|
| 全屏 Space | 是否可见取决于 FullScreenAuxiliary；全屏时本来就应该暂停 | 【推断】需要实测 |
| Mission Control | LuminWallpaper 用 “Dock 拥有、名字为空的窗口” 启发式判断 | 【源码】`desktop_integration_macos.mm:175-190` |
| 遮挡检测 | `occlusionState` 对 desktop level 窗口**不可靠**：Wallnetic 的源码注释写着 “occlusionState flaps for desktop-level windows”。它的做法是连续 3 次采样一致，再在后台线程用 CGWindowList 判断全屏窗口（误差 2 px），只有被全屏覆盖才暂停。LivePaper 直接用 occlusion 通知。MacLiveEngine 每 500 ms 轮询，覆盖率 >95% 就暂停。本项目采用 Wallnetic 的策略（07 章） | 【源码】Wallnetic `WallpaperVisibilityPolicy.swift`、LivePaper；【文档】MacLiveEngine |
| 锁屏（14+） | 普通窗口无法显示在锁屏上。LiveDesk 的做法是覆盖 Aerial 文件（14–15：`/Library/Application Support/com.apple.idleassetsd/Customer/4KSDR240FPS/`，需要管理员权限；26：`~/Library/Application Support/com.apple.wallpaper/aerials/videos/`）；Vantage 做成 `.saver` 屏保 | 【文档】README；属于非公开机制 |
| 热插拔 | 监听 `NSApplicationDidChangeScreenParametersNotification`，或用 MyGo 的 `Screen.OnDisplaysChanged` | 【源码】mygo `modules.go:306` |

## 4.4 编解码能力
- M3 系列支持 AV1 硬件解码，M1/M2 不支持【文档】Apple Newsroom 2023-10。所以 macOS 默认编码选 **HEVC**，H.264 作为兼容兜底。
