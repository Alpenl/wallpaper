# 跨平台高性能动态壁纸 —— 设计文档

> **文档角色：设计与研究历史。** 第 20 轮（2026-10-21）的 ADR、研究结论与实验记录在下文保留；其中“已采纳”“已实测”“下一步”属于该轮上下文，不等于本轮产品全部实现或验收。当前入口见 [文档导航](../README.md)、[实现矩阵](../IMPLEMENTATION_STATUS.md)、[本轮验证](../VALIDATION.md)；迭代记录见 [CHANGELOG.md](CHANGELOG.md)。
> 平台优先级：**MVP 先做 Windows 和 macOS**；Ubuntu（GNOME X11/Wayland、KDE）只作为架构兼容参考。性能是首要目标。
> 依据标注：**【源码】** = 已 clone 并读过的代码；**【文档】** = 官方或项目文档；**【实测】** = 本 box 或第三方的测量；**【推断】** = 未经验证；【issue】= issue 或讨论帖。许可证和专利相关内容一律**不是法律意见**。
> 旧文档保留未删：`docs/research/wallpaper_research.md`、`dynamic_wallpaper_tech_research.md`、`dynamic_wallpaper_feasibility.md`。

## 关键决策摘要（ADR）
| ADR | 决策 | 状态 | 主要理由与依据 | 推翻条件 | 章节 |
|---|---|---|---|---|---|
| 001 | MyGo 只负责控制面；壁纸表面由平台层用原生 API 自建（Win 子 HWND、mac NSWindow） | 已采纳 | MyGo 的 Windows 窗口是 DComp 顶层窗口，Parent 是 owned 窗口，SetParent 后的行为没有保证【源码】 | MyGo 提供可嵌入的子窗口表面 | 02 |
| 002 | Windows 视频用 mpv 子进程（第 14 轮起 Windows 部分为**暂定**，与 MF Media Engine 渲染器 A/B，见下方备选方案）（`d3d11va` + `gpu-next/d3d11`，`--wid`）；macOS 用 AVQueuePlayer + AVPlayerLooper + AVPlayerLayer，放在进程内 | 已采纳 | 进程隔离，不需要 cgo；mac 上 mpv `--wid` 不可用【文档/issue】 | W-T3 发现 mpv 硬解不稳定 → 换 MF | 06 |
| 003 | 性能的关键在**导入时转码**：目标分辨率、30fps、无音轨、系统编码器（MF/VT），产物按显示器缓存 | 已采纳 | 4K30 软解要 0.51–0.70 核，回读还要多 19–41%【实测】；缩放是 GPU 的主要可控开销【实测，Fresco】 | — | 06 |
| 004 | mpv.exe 作为独立的 GPL 程序分发，只通过 IPC 和命令行通信；附源码要约。产品不分发 GPL 编码器（libx264） | 暂定，待律师确认 | FSF FAQ（聚合）；Lively 的先例；FFmpeg legal【文档】 | 律师否决 → 改用 MF 播放器或 LGPL libmpv | 11 |
| 005 | Web 壁纸用系统 WebView（WebView2/WKWebView），不捆绑 CEF；暂停用 `IsVisible=false` + `TrySuspend`，不用调试器冻结 | 已采纳 | 官方 API【文档】；WebKit 内存比 Chromium 少 70–140 MiB（box 参考数据）【实测】 | B-T2 发现 TrySuspend 无效 | 08 |
| 006 | 分发：Windows 先做 NSIS 按用户安装 + 签名（Artifact Signing/OV），Store MSIX 放第二阶段；macOS 先做 Developer ID 直接分发版，MAS 版放第二阶段且功能裁剪 | 已采纳 | EV 不再自动获得 SmartScreen 信誉；Store 应用不弹警告；MAS 2.4.5 的限制【文档】 | — | 11 |
| 007 | 暂停：多个原因取 OR、按显示器判定、快停慢启、L1–L4 分级；macOS 遮挡不能只靠 `occlusionState` | 已采纳 | mpvpaper、Lively、Wallnetic【源码】 | — | 07 |
| 008 | 进程监管：Windows 用 Job Object（KILL_ON_JOB_CLOSE）代替 watchdog 进程；渲染器按退避规则重启；健康检查覆盖 “卡住但没退出” 的情况；用系统壁纸截图兜底 | 已采纳 | MS 文档、mpv #3469【文档/issue】 | — | 02 |
| 009 | 壁纸包 v0：zip + manifest（JSON Schema）；不支持可执行文件类壁纸；Web 包默认禁网，权限需要声明并经用户同意 | 已采纳 | 安全性；Go zip 默认不拒绝不安全路径【源码】 | — | 10 |
| 010 | 遥测默认关闭（opt-in），只上报聚合统计；日志异步写、可丢弃、不记录窗口标题和素材路径 | 已采纳 | 隐私优先 | — | 16 |


## ADR 备选方案与否决理由（第 14 轮反向审阅）
> 方法：对每条 ADR 假设“我们选错了”，找最强的替代方案，写出为什么现在不选、什么证据会让我们改选。结论里凡是没有实测的都标【推断】。

**ADR-001（MyGo 只做控制面，原生自建壁纸表面）**
- 备选 A：整个壁纸表面也用 MyGo 窗口。否决：MyGo Windows 窗口是 DComp 顶层窗口，SetParent 后行为无保证【源码】。
- 备选 B：控制面也放弃 MyGo，改用各平台原生 UI。否决：控制面不在性能热路径上（PB-idle 只计常驻后台，UI 关闭时可退出 UI 部分），换掉只增加两套 UI 成本【推断】。
- 结论：维持。

**ADR-002（Windows：mpv 子进程；macOS：AVPlayerLayer）** ← 本轮最重要的反向结论
- 备选 A（Windows）：**Media Foundation 自渲染**，例如 `IMFMediaEngine` 的 DXGI 模式（`TransferVideoFrame` 到 D3D11 纹理、`SetLoop`），放在独立的渲染器进程里（Go + 纯 syscall 调 COM，或一个很小的 C++ exe）【文档：https://learn.microsoft.com/en-us/windows/win32/api/mfmediaengine/nn-mfmediaengine-imfmediaengine 】。
  - 优点：①**不分发 GPL 程序**，ADR-004 / Q-L1 的法律风险直接消失（非法律意见）；②不用打包 mpv（体积小一个数量级【推断】）；③与系统解码器、HDR、DRM 路径一致。
  - 过去的否决理由是“格式覆盖差”（HEVC/VP9/AV1 需要商店扩展）。**但 ADR-003 已规定导入时统一转成 H.264**，MF 对 H.264 的内置支持没有这个问题——旧的否决理由在我们自己的架构下基本不成立【推断】。
  - 仍然存在的代价：要自己写循环无缝、缩放、帧同步、TDR 恢复（mpv 已有）；Go 调 COM 无 cgo 的工作量与稳定性未验证【推断】。
  - **结论：ADR-002 Windows 部分由“已采纳”降为“暂定”**。MVP 继续用 mpv 起步（原型已就绪），但把 “MF Media Engine 渲染器” 列为 A/B 候选，用 W-T3 的同片源对比 CPU/GPU/内存/循环点后定型；若两者性能相当，则倾向 MF（消除 GPL 依赖）。新增 Q-W10。
- 备选 B（Windows）：LGPL 构建的 libmpv 进程内嵌入。否决：需要 cgo 或 purego 加载 DLL，崩溃会带倒主进程，违背 ADR-008 的隔离【推断】。
- 备选 C（macOS）：**Metal 自渲染**（`AVPlayerItemVideoOutput` → `CVMetalTextureCache` → 自己的 Metal pass）。否决：对纯视频播放，AVPlayerLayer 交给系统合成，VT 解码出的 IOSurface 可直接被合成器使用，自渲染只会多一次我们自己的绘制 pass 和一个常驻渲染循环，没有可预期的性能收益【推断，依据 AVFoundation 文档的架构描述】。Metal 自渲染只在需要特效/着色器叠加时才有意义——这部分已经由 Shader 壁纸（F-14）覆盖。
- 备选 D（macOS）：`AVSampleBufferDisplayLayer` + 自己解复用。只在需要 AVFoundation 不支持的容器（WebM，Q-M4）时才考虑；因 ADR-003 统一转 MP4/H.264，MVP 不需要。
- 推翻条件更新：W-T3 显示 MF 不差于 mpv → Windows 改 MF；M3 显示 AVPlayerLayer 循环点有可见停顿 → 评估 D。

**ADR-003（导入时转码）**
- 备选：播放时实时 GPU 缩放、不转码。否决：解码成本由**源**分辨率决定，4K 软解 0.51–0.70 核、硬解也占解码引擎带宽，实时缩放省不掉解码【实测】。代价是导入耗时和磁盘缓存，已在 F-07 的验收中约束。维持。

**ADR-004（mpv.exe 作为独立 GPL 程序分发）**
- 备选：MF 渲染器（见 ADR-002 备选 A）——这是消除该风险最直接的方案。结论：保持“暂定”，并把 Q-L1 的优先级与 Q-W10 绑定：若 W-T3 后改用 MF，Q-L1 自动关闭（非法律意见）。

**ADR-005（系统 WebView）**
- 备选 A：捆绑 CEF。否决：体积大、内存比 WebKit 高 70–140 MiB（box 参考）【实测】，更新责任转到我们。
- 备选 B：Web 壁纸离屏渲染后作为纹理合成。否决：WebView2 的离屏/合成控制器路径复杂度高，且无法减少 Chromium 本身的开销【推断】。维持。

**ADR-006（分发：NSIS 先行、MSIX 第二阶段）**
- 备选：Store/MSIX 先行（无 SmartScreen 警告）。否决理由不是性能而是未知量：Q-W7（MSIX 内能否挂载、startupTask）未验证。W-T8 若通过，可以把 MSIX 提到首发。维持，推翻条件写明 W-T8。

**ADR-007（暂停策略）**
- 备选：Windows 用 DWM cloak 属性 / `SHQueryUserNotificationState` 单一信号。否决：单一信号漏报多（游戏无边框全屏、多屏）【源码，Lively】。维持。

**ADR-008（Job Object 监管）**
- 备选：独立 watchdog 进程。否决：Job Object 已覆盖“主进程死 → 子进程死”；watchdog 多一个常驻进程【文档】。本轮补充：原型的重挂已改为异步退避（Q-P2），卡住检测改为 (loops, time-pos)（Q-P3）。维持。

**ADR-009（包格式 zip + manifest）**
- 备选：直接兼容 Wallpaper Engine 包格式。否决：WE 的 scene 格式私有且包含可执行类壁纸，与安全目标冲突；只做导入器（10 章）。维持。

**ADR-010（遥测 opt-in，历史设计）**
> 当前产品 `config.Normalize` 强制关闭遥测；下述是早期备选方案，未转化为本轮远程服务承诺。见 [D-15 当前说明](DECISIONS_PENDING.md) 和 [证据差异](../EVIDENCE.md)。
- 备选：完全不做遥测。否决：失去真机性能分布数据（PB 预算的唯一长期来源）。维持 opt-in，聚合上报。

**性能预算反向检查（17 章）**
- PB-video 的对照数据“OBS 硬解约 2%（3950X）”是**占整机 CPU 的百分比**，3950X 有 32 线程，折算可能达到约 0.6 个逻辑核【推断，单位换算】，与 “≤ 0.03 核” 相差一个数量级以上。两者场景不同（OBS 还在编码），但这说明 0.03 核目前**没有任何同类实测支撑**。改为：保留为目标值，标注“首个真机数据后修订”，并在 W-T3 同时记录 mpv 与 MF 两个数值。
- PB-idle 的 “≤ 60 MB” 只对“UI 关闭、只剩后台控制进程”成立；MyGo UI 打开时不计入。已在下面澄清，17 章同步。
- PB-web “每屏 ≤ 300 MB” 低于 box 上 WebKitGTK 的 442–445 MiB（软件 GL）；真机 GPU 合成下应更低，但目前没有真机数据，保持目标、标注待 Q-B2。

## 一页结论
1. **可行**。剩下的主要风险要靠真机验证：WorkerW 在 24H2/25H2 上的长期稳定性，以及驱动 TDR、睡眠唤醒后的恢复（Windows）；desktop level 窗口的层级选择（macOS）。
2. MVP 范围和验收标准见 17 章（F-01…F-16，以及 PB-idle/video/paused 预算）。实现规格已经可以直接开工：进程模型和 IPC v1、Go 包结构和接口（02 章）；状态机规格和测试向量（07 章 v1.1，已有可运行实现 `visibility/` 并接入 Windows 原型）；包格式 schema（10 章）；配置和日志（16 章）。
3. 量化目标（【推断】，首次实测后修订）：4K30 播放时增量 CPU < 0.03 核（目前没有同类实测支撑，见 Q-PB1）；L1 暂停 ≤ 500 ms 生效；暂停期间 GPU 约 0%；控制进程常驻内存 < 60 MB（12 章）。

## 第 20 轮的状态与下一步（历史研究结论）
**结论：不上真机、不做决策的前提下，离线调研已经收敛。** 第 13–20 轮之后，再做文档审阅或原型扩展，能消除的不确定性已经很少；剩下的问题都落在下面三类里。

**已经就绪、可以直接交给真机测试的东西**
- 原型 `prototype/`（预编译的 `bin/` 不入 Git，可用原型源码重新构建）：Windows 端有两种挂载分支、异步重挂、Job Object、按屏独立的 07 章状态机和全部 Windows 信号源、mpv 与 Media Foundation 两个子进程后端（IPC v1）；macOS 端有 4 种窗口层级和遮挡采样；Linux X11 参考实现。
- 测试步骤：[bench/TEST_PLAN.md](bench/TEST_PLAN.md)（W-T1…W-T10、W-T3b、M1–M4），交接清单：[bench/DEVICE_HANDOFF.md](bench/DEVICE_HANDOFF.md)（机型、版本、顺序、每项关闭哪个问题）。
- 验证过的离线部分【实测】：状态机 29 个测试，覆盖率 98.3%；IPC v1 单元测试；D-Bus 接口（godbus + gjs）；Wine 下两个后端的完整暂停/释放/重启链路和按屏独立判断（Wine 结果不代表真 Windows）。

**要靠真机的（按交接清单顺序）**
| 类别 | 问题 |
|---|---|
| Windows 挂载与恢复 | Q-W1（24H2/25H2 稳定性）、Q-W6 真机部分（TDR）、Q-W11（分层子窗口 + flip 交换链） |
| Windows 渲染器选型 | **Q-W10**（mpv 与 MF A/B，决定 ADR-002/004）、Q-W12（MF 循环与统计）、Q-PB1（PB-video 首个真值） |
| 暂停与信号 | Q-V1（状态机 + 信号源真机行为）、Q-V2（判定开销）、Q-W4（专注模式）、Q-M6（CGWindowList 开销） |
| macOS | Q-M1（窗口层级）、Q-T1（powermetrics 格式） |
| 视频与 Web | Q-D2（循环点）、Q-B2、Q-B3、Q-S1 |
| 打包 | Q-W7（MSIX）、Q-W8（WER 重启） |
| Linux 参考 | Q-L4、Q-L5、Q-L7 |
| 原型本身 | Q-P1（Windows/macOS 从未在真机运行） |

**要靠决策的（产品/工程负责人）**
- ADR-002 / ADR-004：W-T3b 出结果后按 TEST_PLAN 的判定规则选 mpv 或 MF（10% 容差本身也需要确认）。
- ADR-006：W-T8 通过后是否把 MSIX 提到首发。
- Q-W3（每个虚拟桌面一张壁纸，依赖未公开接口）、Q-M3（mac 锁屏动态壁纸，只有非公开路径）：做不做、放在哪个分发渠道。
- Q-M4：WebM 解复用选 libavformat（LGPL）还是 BSD 实现（也牵涉律师）。
- Q-D3：Mac App Store 版是否保留 Web 壁纸（审核 2.4.5 风险）。
- Q-W2：是否投入 DComp visual 方案（只有在 W-T3 显示合成开销明显时才值得）。

**要靠律师的（以下均不是法律意见）**
- Q-L1：mpv.exe 作为独立 GPL 程序随产品分发（若 Q-W10 选 MF 则可撤销）。
- Q-L2：HEVC 等专利池对“只调用系统解码器”的产品的义务。
- Q-M4 的许可证部分。

**第 20 轮当时仍可离线做的实验项**：GNOME 49+ 扩展（Q-L7）；研究原型限帧（FPSCap）；管道显式 DACL；macOS 原型接入同一个状态机。注意“原型限帧”不描述当前产品：`internal/render` 的 mpv/MF/AVF 已有 `SetFPSCap`，实际效果与功耗仍需真机，见 [F-05](../IMPLEMENTATION_STATUS.md)。

## 章节目录
| # | 章节 | 关键问题 |
|---|---|---|
| 01 | [产品与竞品](01-product-competitors.md) | 市场、竞品、差异化 |
| 02 | [总体架构](02-architecture.md) | 进程模型、IPC v1、崩溃恢复、包结构、接口 |
| 03 | [Windows 桌面集成](03-windows-desktop.md) | WorkerW、24H2/25H2、恢复 |
| 04 | [macOS 桌面集成](04-macos-desktop.md) | level、Space、Stage Manager、锁屏 |
| 05 | [Ubuntu / Linux 兼容](05-ubuntu-compat.md) | GNOME 46–50 的 X11/Wayland、Hanabi、KDE 插件、无 cgo 库 |
| 06 | [视频管线与编码规范](06-video-pipeline.md) | 硬解、零拷贝、转码参数 |
| 07 | [暂停与省电状态机](07-pause-power-state-machine.md) | 信号源、优先级、分级暂停 |
| 08 | [网页与 Shader 壁纸](08-web-shader.md) | WebView、冻结、帧率控制 |
| 09 | [多显示器 / DPI / HDR](09-multimonitor-dpi-hdr.md) | 坐标、热插拔、HDR |
| 10 | [壁纸包格式 v0](10-package-format.md) | schema、安全、Lively/WE 导入 |
| 11 | [分发、签名、更新、许可证](11-distribution-licensing.md) | NSIS/MSIX、SmartScreen、公证、MAS 差异 |
| 12 | [测试与性能基准方法](12-testing-benchmarks.md) | 工具、指标、通过标准 |
| 13 | [风险登记表](13-risk-register.md) | |
| 14 | [未决问题](14-open-questions.md) | 下一轮的输入 |
| 15 | [参考文献](15-references.md) | |
| 16 | [配置、持久化、日志与遥测](16-config-logging-telemetry.md) | 目录、原子写、隐私、开销 |
| 17 | [功能清单与 MVP 需求规格](17-features-mvp.md) | 痛点映射、分级、验收标准、性能预算 |

## 本地资产
- 克隆的仓库在研究机本地（未入库）：lively、mpvpaper、linux-wallpaperengine、gnome-ext-hanabi、hidamari、komorebi、Fresco、LuminWallpaper、openwallpaper、mygo、Plash（只有 README）。
- **真机测试原型**：[prototype/](prototype/README.md)，包括 Windows、macOS、Linux-X11 三个平台的源码（`bin/` 预编译产物不入 Git）（全部 `CGO_ENABLED=0`）；README 里有和 TEST_PLAN 的对应表。早期的 MyGo 骨架 `docs/research/wpproto_src/` 保留。
- 基准与测试包：`bench/`（sw_decode_bench.sh、webview/、windows_capture.ps1、macos_powermetrics.sh、TEST_PLAN.md）。
- 包格式 schema：`schema/manifest.v0.schema.json`，附两个示例。
- 文档备份：研究机上的本地备份 r6（未入库）（第 6 轮后）、研究机上的本地备份 r9（未入库）（第 9 轮后）。

## 第 13–15 轮新增
- GNOME ↔ 守护进程 D-Bus 接口与 godbus 实测：[linux-dbus/README.md](linux-dbus/README.md)
- 真机测试交接清单：[bench/DEVICE_HANDOFF.md](bench/DEVICE_HANDOFF.md)
- 备份：研究机上的本地备份 r12（未入库）（第 13 轮修改前）

## 第 16–18 轮新增
- Windows Media Foundation 渲染后端（`prototype/ -backend mf`，Q-W10 的 A/B 对象）：[prototype/README.md](prototype/README.md)；对比步骤：[bench/TEST_PLAN.md](bench/TEST_PLAN.md) W-T3b
- 最小 GNOME 扩展（46–48）：[gnome-ext/README.md](gnome-ext/README.md)
- 状态机可运行实现与测试（07 章 v1.1）：[visibility/README.md](visibility/README.md)
- 备份：研究机上的本地备份 r15（未入库）（第 16 轮修改前）

## 第 19–20 轮新增
- Windows 原型接入 07 章状态机与全部信号源、按屏独立；MF 后端改为子进程（IPC v1）：[prototype/README.md](prototype/README.md)；新测试 W-T10：[bench/TEST_PLAN.md](bench/TEST_PLAN.md)
- 备份：研究机上的本地备份 r18（未入库）（第 19 轮修改前）

- 待决策清单：[DECISIONS_PENDING.md](DECISIONS_PENDING.md)（2026-10-21）
