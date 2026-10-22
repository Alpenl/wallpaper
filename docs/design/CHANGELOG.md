# CHANGELOG（调研文档迭代记录）

## 2026-10-22 UTC v0.3.0-mvp 预发布准备

- 应用与打包默认版本同步为 0.3.0-mvp；保留原生应用和 UI 分支仅设计参考的边界。
- 新增仅由本版本发布声明或手动触发的检查/打包/预发布流程，发布前验证精确提交、已有 tag 和资产摘要，不强推或覆盖已发布资产。
- 未签名、未配置生产更新密钥；真机运行与后续 Web/Shader/工坊仍按发布说明披露。

## 2026-10-21 UTC 原生 MVP 后续实现接续

- 更新 F-03/F-07/F-08/F-17/F-18 与数据 schema 2；Win/mac 空间跨屏、macOS 原生隔离 AVFoundation 转码、严格输出验证和 UTC 周暂停指标单列来源与边界。
- 16:09 之前的七页 UI 检查保留为检查点，不覆盖之后新增源码。截图不证明宿主、编解码、硬解或节电量。
- 新增本轮研究补充与目标平台验收项；保留原历史研究和 F-12/F-14 v1、F-25 以后、F-26 v0 不支持的分期，完整后续范围仍待确认。

## 2026-10-21 按 UI 设计稿完善主分支应用与文档

- 明确产品只能使用原生 MyGo；React 仅保留原有 UI 设计参考，不作为功能实现、桥接或验收入口。
- 新增统一文档导航、七页 UI / F-01…F-26 实现验收矩阵、单独的本轮验证台账与证据约定。
- 改造前根 README 保存在 `docs/history/IMPLEMENTATION_0.2.1.md`，不将旧 Wine/Xvfb/单元测试报告当作本轮 PASS。
- 研究三报告、01–17 章和独立实验代码保留；注明当前产品、历史原型、缺失实验资产与真机门槛的区别。
- 对齐 D-07 的生产 FPS cap 已有实现、D-15 的生产遥测强制关闭；未把建议自动视为用户批准。
- 新增离线 Markdown 相对链接/锚点与矩阵编号校验。


以下历史研究轮次时间均为 America/Los_Angeles（PDT）；本轮实现接续明确使用 UTC。

## 2026-10-20 第 1 轮：结构整合与问题盘点
**审阅了三份旧文档**：`wallpaper_research.md`、`dynamic_wallpaper_tech_research.md`、`dynamic_wallpaper_feasibility.md`。发现以下问题：
1. 【矛盾】tech_research 的推荐表写“MyGo 壳 + 把 MyGo 窗口 SetParent 到 WorkerW”，但 feasibility 查到 MyGo 的 Windows 窗口是 DComp 顶层 target，SetParent 有黑屏风险。两份文档没有统一结论。
2. 【缺失】没有许可证合规（mpv/FFmpeg 的 LGPL/GPL、HEVC 专利）、壁纸包格式、导入转码规范、测试方法这几章。
3. 【空泛】“暂停与省电”只列了 API，没有状态机，也没有优先级和迟滞（防抖）规则。
4. 【未核实】Lively 暂停时到底挂起了什么，此前没有逐行确认（tech_research 里写的是“对 mpv 具体用了哪一级没有逐行核实”）。
5. 【矛盾】macOS 层级：有的地方写 `kCGDesktopWindowLevel`，有的写 `-1`。
6. 【缺失】没有说明 MyGo 已经提供了哪些可复用的系统 API（Screen、Power）。

**修改**：新建 `wallpaper_docs/`，包含主文档 README 和 01–15 共 15 个章节；旧文档保留不动。架构上统一为“**MyGo 只负责控制面和设置 UI；壁纸画面由各平台原生代码自建窗口或表面**”（解决问题 1，详见 02、03 章）。

## 2026-10-20 第 2 轮：源码逐行核实与 box 实验
1. 问题 4 已解决（【源码】）：
   - Lively 的 mpv 暂停只是通过 IPC 发 `set_property pause true`（`Core/Wallpapers/VideoMpvPlayer.cs:177-180`），不挂起进程。
   - WebView2 壁纸暂停时，会对 **Chromium 的 D3D 渲染（GPU）子进程调用 `DebugActiveProcess`** 来冻结它，恢复时调用 `DebugActiveProcessStop`；另外还会 `TryPauseMedia`，并向页面发 `livelyWallpaperPlaybackChanged` 事件（`Lively.Player.WebView2/Form1.cs:395-420, 582-600`）。
   - 外部程序壁纸曾经试过 DebugActiveProcess、NtSuspendProcess、挂起全部线程三种办法，代码注释分别写着“不是所有程序都有效”、“恢复耗时”、“死锁”，现在都被注释掉了（`ExtPrograms.cs:95-115`）。
   - 这一条写进了 07、08 章。
2. 问题 6 已解决（【源码】mygo）：
   - `Screen.Displays()` 返回 Bounds、WorkArea、ScaleFactor、Rotation、Primary，**不含刷新率和 HDR 信息**（`modules.go:210-232`）。
   - 有 `Screen.OnDisplaysChanged`。
   - `Power` 提供 `OnSuspend`、`OnResume`、`OnLockScreen`、`OnUnlockScreen`、`IsOnBattery()`、`IdleTime()`（`power.go`）。**电池状态没有变化事件，远程会话也没有 API**，这两项需要各平台自己补。
3. 新实验（【实测】box）：做了 CPU 软解基线（`bench/sw_decode_bench.sh`），结果写进 06 章。box 没有 GPU，**硬解没法在 box 上测**。
4. 新发现（【源码】）：Lively 用一个独立的 Watchdog 进程，在主程序被杀后清理壁纸子进程（`Lively.Utility.Watchdog/Program.cs`），已写进 02 章的进程模型。

## 2026-10-20 第 3 轮：编码、循环、许可证、格式
1. 【文档】Wallpaper Engine 官方说明：视频壁纸走 **Windows Media Foundation**，循环点卡顿“来自 Windows/显卡驱动”，建议把循环点放在画面几乎静止的位置，或者换用 LAV/DirectShow 和 webm。据此给 06 章加了“无缝循环”规范。
2. 【文档】AVPlayerLooper 要无缝循环，**音轨、视频轨和整个文件的时长必须一致**，否则会退回有缝循环（Apple 工程师在 SO 5361145 的回复）。因此导入规范规定：去掉音轨，统一时长。
3. 【文档】M3 系列芯片支持 AV1 硬件解码（Apple Newsroom 2023-10）。M1/M2 没有，所以 AV1 不能作为 macOS 的默认编码。
4. 【文档】mpv 的 `Copyright` 文件：`-Dgpl=false` 可以构建 LGPL 版 libmpv，但这个开关“本身不构成 LGPL 授权”；LGPL 模式下会关掉 X11 VO、vdpau 等功能；链接的 FFmpeg 如果是 GPL 构建，整体就变成 GPL。FFmpeg legal 页给了 18 条合规清单。已写进 11 章。
5. 【源码】壁纸包格式：
   - Lively 用 `LivelyInfo.json`（字段见 `Lively.Models/LivelyInfoModel.cs`）和 `LivelyProperties.json`。
   - Wallpaper Engine 用 `project.json`（`type` 取 scene/video/web，`file`、`general.properties`；见 linux-wallpaperengine `Data/Parsers/ProjectParser.cpp`）。scene 包以 `PKGV` 开头（`PackageParser.cpp:18`）。
   - 已写进 10 章。
6. 问题 5 处理：两种层级值都有开源项目在用。实测项 M1 两种都要测，已记入 14 章的未决问题。

## 第 4 轮（2026-10-20）Windows/macOS 调研
Q-W1、Q-W5、Q-M1、Q-M2、Q-M5 结论写入 03/04/08 章。
## 第 5 轮 编解码与许可
VP9/WebM 路径、硬解支持表写入 06；FSF 与专利（非法律意见）写入 11。
## 第 6 轮 实测与测试包
WebKitGTK 与 Chrome 的 box 实测写入 08；新增 bench/windows_capture.ps1、macos_powermetrics.sh、TEST_PLAN.md、webview/*；重写 14 章，补充 15 章。

## 第 7 轮（2026-10-21）架构细化
- 先审阅，发现的问题：
  - 02 章只有伪代码，没有 IPC 协议和崩溃恢复；
  - 07 章缺少完整的状态表和参数；
  - 配置、日志、遥测完全空白；
  - 有一个 watchdog 进程，但没有说明它怎么防止孤儿进程。
- 解决：
  - 重写 02 章：进程表；IPC v1（NDJSON 信封、方法、错误码、命名管道 ACL）；supervisor 表（mpv 卡住检测、WebView2 ProcessFailed 各类处理、DEVICE_REMOVED）；退避规则；控制进程崩溃处理（Job Object、系统壁纸兜底、RegisterApplicationRestart、SMAppService agent、安全模式）；Go 包结构和接口签名。
  - 重写 07 章：14 个 Reason、状态转移表、参数表、各平台信号来源、开销预算、测试向量。
  - 新增 16 章：目录布局（包括 MSIX 的重定向）、原子写（已核对 Go 源码）、迁移、显示器稳定 ID、异步日志、隐私规则、本地指标、opt-in 遥测。
- 新的实证【源码】：
  - Lively grid 阈值的实际含义（`WindowUtil.cs:13-63`）；
  - Go `os.Rename` 在 Windows 上用 `MoveFileEx(REPLACE_EXISTING)`；
  - Go 有 `debug.SetCrashOutput`。

## 第 8 轮 包格式、安全与分发
- 先审阅，发现的问题：
  - 10 章只有草案，没有 schema、安全措施和导入映射；
  - 11 章缺少安装形式、SmartScreen、MAS 差异。
- 解决：
  - 10 章：manifest v0 字段表、版本策略、安全解压（Go zip 默认不拒绝不安全路径【源码】，所以用 `os.OpenRoot`）、Web 沙箱表（来源隔离、默认禁网、导航、权限、下载、宿主桥接）、权限清单、不支持 app 类型、Lively 和 WE 的导入映射、属性注入兼容层。新增 `schema/manifest.v0.schema.json` 和两个示例，已在 box 上用 python-jsonschema 校验通过【实测】。
  - 11 章：NSIS 与 MSIX 对比（startupTask 的限制、AppData 重定向）；EV 证书不再自动获得 SmartScreen 信誉；Artifact Signing 的价格和地区限制；SMAppService；Sparkle 在沙盒下的要求；App Review 2.4.5 逐条核对；ADR-006。

## 第 9 轮 一致性审查与收敛
- 发现的矛盾：
  - README 写着 “mpv 必须用 LGPL 构建”，和 11 章的 GPL 独立进程方案冲突 → 统一为 ADR-004；
  - 04 章写着 occlusion “没有实证”，但后面已经找到实证；
  - 08 章写着 “没有对照数据”，但 box 已测，TrySuspend 也已确认；
  - 06 章的支持表标着 “待整理”，但表已经有了；
  - 06 章出现两个 6.5 小节；
  - 13 章 R4 和 R6 已过时。
- 处理：去掉所有 “第 N 轮补充” 段落，内容并入正文（03/04/06/08/11/15）；README 开头加上 ADR 表（001–010）；13 章新增 R13–R17；12 章和 TEST_PLAN 新增 W-T8、W-T9、B-T2 和崩溃兜底测试；14 章重写为表格。
- 跟进 Q-W6（TDR）：查到 MS 的 device-lost 文档、mpv #3469/#7190、WebView2 process-related events，结论写入 02 §2.4 → 关闭，真机验证转为 W-T9。
- 自我更正：
  - “任务管理器结束进程会触发 WER 重启” 是错的，已在 TEST_PLAN 里改正；
  - Go panic 是否触发 WER 列为 Q-W8；
  - 14 章 Q-W2–W4、Q-M3、Q-D2 的原文在第 6 轮丢失，已按正文重建，并在章首注明。
- 备份：修改前的文档已完整复制到 研究机上的本地备份 r6（未入库）。

## 第 10 轮（2026-10-21）Ubuntu / Linux 兼容
- 先审阅，发现的问题：05 章只有 3 行表格，没有区分 GNOME 版本，没有代码层面的要点，也没有 Go 库的结论。
- 解决：重写 05 章（这是正文章节，不是开放问题章节）。
  - 会话矩阵：Ubuntu 25.10 移除了 GNOME 的 Xorg 会话；GNOME 50 删除了 mutter 的 X11 后端【文档】。
  - Hanabi 的代码要点和行号：`Meta.WaylandClient.new_subprocess`、override `_createBackgroundActor`、`Clutter.Clone`、按标题隐藏渲染器、GstPlay、D-Bus、autoPause【源码】。
  - 设计：GNOME 扩展 + mpv 作为渲染器；KDE 走 Plasma 6 壁纸插件（WallpaperItem、`X-Plasma-API-Minimum-Version`、`KPackageStructure`）【文档】；不在 KDE 上用 layer-shell。
  - 无 cgo 的库：xgb（实测可用）；go-wayland（纯 Go，但没有 layer-shell，只能提交 shm 缓冲区）。
- box 实测：Xvfb 下用 `_NET_WM_WINDOW_TYPE_DESKTOP` 窗口加 `mpv --wid` 嵌入成功（xprop、xwininfo、截图像素、IPC 统计都确认了）【实测】。安装 mpv 0.40.0 和 x11-apps 时使用 `--force-confold`，没有改动现有配置文件。

## 第 11 轮 功能清单与原型
- 新增 17 章：痛点 → 功能映射；F-01…F-26 按 MVP / v1 / 以后分级，每项都有验收标准；性能预算 PB-idle/video/paused/battery/web/shader；MVP 发布门槛。
- 新增 `prototype/`：
  - Windows：24H2/旧版两条挂载分支、Job Object、命名管道 IPC、TaskbarCreated、健康检查、前台全屏暂停、STALL 重启；
  - macOS：4 种 level、AVPlayerLooper、occlusionState 与 CGWindowList 对照记录、调用耗时、3 次采样去抖；
  - Linux-X11：xgb。
  - 5 个目标平台全部用 `CGO_ENABLED=0` 交叉编译通过，附 SHA256SUMS；附带 README 和测试项对应表。
- 已知限制记入 14 章：Q-P1–P3。

## 第 12 轮 全文审阅
- **重要更正**：24H2 上子窗口的父窗口是 **Progman**（插在 DefView 下面，WorkerW 保持在最底），不是 WorkerW。依据是 Lively `WinDesktopCore.cs:1041-1080`。已更正 03 章、02 章、11 章和原型，并记为 Q-W9。
- 对齐阈值：TEST_PLAN 的 “mpv < 0.1 核”、“≤ 1 s 暂停” 改为 17 章的 PB-video（≤ 0.03 核增量）和 F-04（≤ 500 ms）。
- 更新原型路径的引用（03、04、README、12、TEST_PLAN）；README 增加 17 章、原型、两份备份路径；15 章补充参考文献。
- 14 章：**只做增量修改**（新增 Q-L3–L6、Q-P1–P3、Q-W9，更新 Q-W1、Q-M6），没有整篇覆盖。
- 备份：修改前的文档已复制到 研究机上的本地备份 r9（未入库）。

## 第 13 轮 关闭 Linux 问题与原型修正（2026-10-21）
- 备份：修改前的文档复制到 研究机上的本地备份 r12（未入库）。
- Q-L3 关闭：读 KWin 源码，确认 layer-shell v5，BACKGROUND 与 Plasma 桌面同在 DesktopLayer【源码】。05 §5.4.1。
- Q-L5 降为部分解决：Qt Multimedia 能 VAAPI 硬解但默认回读，MpvQt 需 C++ 插件【文档/issue】。
- Q-L6 关闭：D-Bus 接口 XML + godbus 守护进程，box 上 dbus-run-session selftest 与 dbus-send 互通【实测】（`linux-dbus/`）。
- 原型：Q-P2 异步重挂（goroutine 退避 + PostMessage 回 UI 线程），Q-P3 卡住检测改为 (playback-restart 计数, time-pos)，Xvfb 复现旧误报条件并验证修复【实测】；5 个目标重新交叉编译，SHA256SUMS 更新。

## 第 14 轮 反向审阅
- README 新增“ADR 备选方案与否决理由”，逐条质疑 ADR-001–010。
- **ADR-002 Windows 部分降为暂定**：因 ADR-003 已统一转 H.264，MF Media Engine 的格式劣势基本消失且可消除 GPL 依赖；新增 Q-W10（与 mpv A/B）。macOS Metal 自渲染否决（纯视频无收益【推断】）。
- 性能预算：发现 PB-video 对照数据单位不一致（OBS 2% 为整机百分比），新增 Q-PB1；PB-idle 澄清为 UI 关闭时。17 章同步。

## 第 15 轮 交接与收尾
- 新增 `bench/DEVICE_HANDOFF.md`：机型/系统版本、6 个阶段的运行顺序、每项关闭的问题。
- 14 章增量修改：关闭 Q-L3/L6/P2/P3，Q-L5 转部分解决，新增 Q-W10、Q-PB1；旧行保留原文并标注去向。
- README 状态改为第 15 轮；15 章补参考文献。

## 第 16 轮 Media Foundation 渲染器原型（2026-10-21）
- 备份：修改前的文档复制到 研究机上的本地备份 r15（未入库）。
- 原型新增 `-backend mf`：纯 Go（无 cgo）调用 IMFMediaEngine 帧服务器模式 + D3D11 + 自建子 HWND 的 DXGI 交换链；支持循环（SetLoop + ENDED 兜底）、暂停/恢复（渲染线程完全停止 present）、掉帧统计（GetStatistics + 本程序 presented/appSkipped）、DEVICE_REMOVED 重建、卡住检测。与 mpv 共用挂载代码（`renderBackend` 接口）。新增 `-standalone`、`-pausetest`、`-swap`。
- vtable 下标与 GUID 核对自 mingw-w64 头文件；在 Wine 10.0 中自检：COM 链路、解码显示、暂停恢复通过，循环和统计在 Wine 下不可用【实测，Wine】。
- 5 个目标重新交叉编译，SHA256SUMS 更新。TEST_PLAN 新增 W-T3b，交接清单 2.2 更新。14 章新增 Q-W11、Q-W12。

## 第 17 轮 最小 GNOME 扩展
- 新增 `gnome-ext/wpd-shell@wpd.github.io`（GNOME 46–48）：XML 生成代理、Meta.WaylandClient 启动 mpv、Clone 到背景 actor、上报显示器/全屏/概览/渲染器状态。
- 发现 mutter 49 移除了 `hide_from_window_list`/`make_desktop`、改用 `new_subprocess`【源码】→ Q-L7。
- 静态检查：node --check、ESLint 9、metadata/XML/签名一致性脚本全部通过；gjs 1.82.3 对接 Go 守护进程打通【实测】。修正 XML 注释中的 `--`。

## 第 18 轮 状态机单元测试
- 新增 `visibility/` Go 包：28 个测试（含 7.7 全部 5 个向量和 23 个边界用例），覆盖率 98.2%，`-race` 通过【实测】。
- 由此修订 07 章为 v1.1（7.8 列出 13 处漏洞及新规则），并在向量 1/2 原文下方加注更正，原文保留。
- 14 章增量：新增并关闭 Q-SM1；补充 Q-L6；更新 Q-W10；新增 Q-W11、Q-W12、Q-L7。README 状态改为第 18 轮。

## 第 19 轮 状态机接入与 MF 子进程化（2026-10-21）
- 备份：修改前的文档复制到 研究机上的本地备份 r18（未入库）。
- Windows 原型重写为按屏结构（`-monitor -1` 全部显示器），暂停由 visibility 状态机驱动；接入 WinEventHook 前台/全屏 + 网格遮挡、WTS 锁屏与远程、电源设置通知（交流/电池、电量、省电、显示器开关）、睡眠唤醒、屏保。visibility 新增 `Controller.NextDeadline()`（一次性定时器，空闲不周期唤醒）与测试。
- MF 后端改为 `-mfhost` 子进程：继承 Job Object，按 02 章 IPC v1 通信（新 `ipc.go` + 单元测试）；卡住/DEVICE_REMOVED 由子进程报错退出，与 mpv 同一条 supervisor 路径。
- 修复：`syscall.NewCallback` 泄漏（约 30 分钟后 panic）；mpv 暂停/释放期间误判 IPC 断开。
- 新调试参数：`-fakemonitors`、`-covertest`；`-pausetest` 改为驱动 User 原因并缩短 t23/t34。
- Wine 验证：两个后端的 L1→L3→L4→恢复、supervisor 重启、按屏独立的 Fullscreen/Covered（含进入去抖 150 ms、恢复去抖 300 ms）【实测，Wine】。5 个目标重新交叉编译，SHA256SUMS 更新。
- TEST_PLAN 新增 W-T10，W-T3b 记录表改为按进程；交接清单 2.2、3.1、3.2 更新，新增 3.4。02 章补充 IPC v1 实现说明。

## 第 20 轮 最终收尾审阅
- README 新增“当前状态与下一步”：离线调研已收敛；剩余问题按真机 / 决策 / 律师分类。一页结论更新。
- 全部 Markdown 相对链接检查通过。
- 14 章增量：新增并关闭 Q-P4、Q-P5、Q-P6；更新 Q-P1；新增 Q-V1、Q-V2、Q-V3。

## 2026-10-21 实现阶段：MVP 0.1 → 0.2.0-mvp
- 0.1：`wallpaperd` 正式实现（Windows 优先，mac/Linux 宿主、MyGo UI、NSIS/.app 打包、Linux e2e）。用户在 Windows 真机上安装运行，反馈正常且流畅【用户实测】。
- 0.2.0-mvp：
  - F-05：fps 上限接入三个后端（mpv vf+copy 解码、MF 节流、AVF 视频合成），电池策略新增"限 15 fps"，"省电"档位限 15 fps；mac 低电量模式/过热/AC 每秒轮询。
  - F-07：Win/Linux 调用户自己的 ffmpeg 一键转码，mac 调 avconvert。
  - F-09：托盘新增下一张、性能档位子菜单。F-10：首次运行模态询问开机自启。
  - F-15：Ed25519 签名的增量更新与自动/手动回滚（`internal/update`、`cmd/wpupdate`），e2e 7/7。
  - 设置界面重做：10 个页面，浅色/深色，v1 功能占位标注"开发中"。
  - 仓库整理：设计文档移入 `docs/design/`，三份调研报告移入 `docs/research/`，原型二进制不入库。
- 未真机验证：AVF/MF fps 上限、mac 电源轮询、省电实际功耗、mac 按文件更新后的代码签名、0.2.0 全部新 UI 在 Win/mac 上的显示。

## 2026-10-21 0.2.1-mvp：多显示器修正（来自 Windows 双屏真机反馈）
- 用户在 Windows 双屏真机上测 0.2.0【用户实测】，发现：导入即应用到所有屏；卡片编号和 `\\.\DISPLAYn` 对不上（"2 \\.\DISPLAY1"、"1 \\.\DISPLAY2 · 主屏"）；显示原始设备名；重命名行为异常。
- 修正：
  - 导入只加入库（可选"应用到当前显示器"）；详情面板"应用到"支持某一块 / 所有显示器。
  - 稳定 ID：DisplayConfig monitorDevicePath → EDD interface name → IDesktopWallpaper 路径 → GDI 名兜底；0.2.0 的 ID 自动迁移；同型号无序列号的屏去重（#2）。mac 同样按排列排序并去重。
  - 名称：友好名 → DeviceString（排除泛称）→ "显示器 N"；编号按物理排列，布局图、选择条、卡片、壁纸库"使用中"统一"编号  名称"。
  - 重命名：别名按稳定 ID 持久化、可恢复默认；切屏时放弃未保存的编辑，避免写到另一块屏。
  - 新增单元测试：ID 解析/回退/迁移、排序与去重、别名、导入不改屏、单屏/全部分配、下一张。
- 盒子上：Wine 下 DisplayConfig 调用成功、结构体大小断言通过【实测】。**Windows 真机双屏待复测。**

## 2026-10-21 待决策清单
- 新增 DECISIONS_PENDING.md：15 项产品决策（D-01–D-15）、4 个律师问题（L-1–L-4）。没有改动其他章节内容。
