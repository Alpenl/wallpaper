# 14 未决问题（第 20 轮后；本章从第 10 轮起只做增量修改）

> 说明：第 6 轮重写本章时，Q-W2–W4、Q-M3、Q-D2 的原文被覆盖，没有留存（备份 r6 里也已经是覆盖后的版本）。下面这几项是**根据各章正文里仍然开放的事项重建的**，编号沿用，但措辞可能和最初的版本不同。

## 已关闭
| ID | 结论 | 章节 |
|---|---|---|
| Q-W5 | TrySuspend 要求 `IsVisible=false`，是 best-effort | 08 |
| Q-W6 | TDR 恢复机制已明确：mpv 用停滞检测加重启；自绘渲染器处理 DEVICE_REMOVED；WebView2 GPU 进程自动恢复。**真机验证**转为 W-T9 | 02 §2.4、03 |
| Q-M2 | occlusionState 对 desktop level 窗口会来回跳变 → 3 次采样去抖 + CGWindowList | 04、07 |
| Q-M5 | purego 传 Go string 是安全的 | 04 §4.5 |
| Q-B1 | box 上 WebKitGTK 与 Chromium 的参考数据已测；真机对比转为 Q-B2 | 08 §8.4 |
| Q-D1 | 硬解支持表 | 06 §6.7 |
| Q-W9（第 12 轮） | 24H2 挂载的父窗口应该是 Progman（插在 DefView 下面），不是 WorkerW；已核对 Lively 源码 `WinDesktopCore.cs:1041-1080` 并更正 03 章和原型 | 03 §3.1 |
| Q-A1（第 7 轮提出） | Lively 的 grid 阈值含义：未覆盖 tile ≤ 5%，或者有窗口最大化，或者单个窗口覆盖 ≥ 95% | 07 §7.4 |

| Q-L3（第 13 轮关闭） | KWin 实现 `zwlr_layer_shell_v1` v5（`src/wayland/layershell_v1.cpp:22`）；BACKGROUND 层映射到 `DesktopLayer`（`src/layershellv1window.cpp:216-222`），与 Plasma 桌面（PlasmaShellSurface Role::Desktop → `DesktopLayer`，`src/window.cpp:553`）同层，层内先后不固定——这解释了第三方“图标被盖住/消失”的报告。KDE 继续走壁纸插件【源码，KWin cd0c253】 | 05 §5.4 |
| Q-L6（第 13 轮关闭） | 接口 `io.github.wpd.Shell1`（XML 见 `linux-dbus/`）；godbus/dbus v5.2.4 纯 Go，在 box 会话总线上 selftest 通过，`dbus-send` 互通【实测】。GJS 端真机联调列入交接清单 6.2 | 05 §5.3、linux-dbus |
| Q-P2（第 13 轮关闭） | 原型重挂改为 goroutine 计时 + PostMessage 回 UI 线程单次尝试；startMpv 移出 UI 线程。真机响应性在交接清单 1.1 确认 | prototype/README |
| Q-P3（第 13 轮关闭） | 卡住判定改为 `(playback-restart 计数, time-pos)`；box 上 3 s 片/3 s 采样复现了旧实现的误报条件（time-pos 连续 4 次相同），新实现未误报【实测】 | prototype/README、02 §2.4 |

| Q-SM1（第 18 轮新增并关闭） | 07 章状态机规格有 13 处漏洞或矛盾（向量 1/2 与参数表冲突、L2 无动作、计时起点、只升不降、L4 的 Done、Recovering 行为、睡眠补升等），已写成 `visibility/` 包、28 个测试通过（覆盖率 98.2%），07 章修订为 v1.1（7.8）【实测】 | 07 §7.8 |
| Q-L6 补充（第 17 轮） | GJS 端代理已用 gjs 1.82.3 对接 Go 守护进程打通【实测】；XML 注释中的 `--` 已修正 | gnome-ext/README |

| Q-P4（第 19 轮新增并关闭） | MF 后端在主进程内运行，与 mpv 子进程的开销不可比 → 改为 `-mfhost` 子进程，继承 Job Object，IPC v1 通信；Wine 中跑通【实测，Wine】 | prototype/README、02 §2.3 |
| Q-P5（第 19 轮新增并关闭） | 原型每次遮挡轮询/重挂都新建 `syscall.NewCallback`（永不释放、上限约 2000），约 30 分钟后会 panic → 改为只建一次【源码，本仓库】。产品代码同样要遵守 | prototype/README |
| Q-P6（第 19 轮新增并关闭） | 原型暂停逻辑只看前台全屏、单屏 → 已接入 visibility 状态机、全部 Windows 信号源，按屏独立；Wine 中验证了 User/Fullscreen/Covered 三类信号、L1→L3→L4→恢复、supervisor 重启【实测，Wine】 | prototype/README |

## 部分解决，需要真机
| ID | 问题 | 下一步 |
|---|---|---|
| Q-W1 | 24H2/25H2 上的稳定性（睡眠、explorer 重启、驱动重置） | W-T1、W-T6、W-T9（原型已可直接运行） |
| Q-M1 | 窗口层级用 desktop、desktop-1、desktop+1 还是 desktopIcon-1 | 在 M 系列真机上测试图标是否可点、Mission Control 和 Stage Manager 的表现 |
| Q-M4 | WebM 解复用器用 libavformat（LGPL）还是 BSD 实现 | 评估 BSD 实现的成熟度；比较体积和许可证义务 |
| Q-L1 | mpv.exe 作为 GPL 独立程序分发的合规性（ADR-004）| 律师（非法律意见） |
| Q-L2 | HEVC Advance 免费条款是否涵盖 GPU 硬解；我们只调用系统解码器时承担什么义务 | 律师 |

| Q-L5（第 13 轮降为部分解决） | Qt Multimedia FFmpeg 后端支持 vaapi 等硬解（`QT_FFMPEG_DECODING_HW_DEVICE_TYPES`），但 VAAPI 下默认把帧拷回 CPU，需 `QT_XCB_GL_INTEGRATION=xcb_egl` 才走纹理转换【文档，Qt 6.12】；MpvQt（libmpv render_gl，需 C++ QML 插件）零拷贝更可能【推断】，SVWR 草稿 PR #133 正在加入【issue】。推荐默认 Qt Multimedia、MpvQt 作为性能选项 | 交接清单 6.3 |
| Q-W10（第 14 轮新增；第 16 轮更新：MF 后端原型已完成，`-backend mf`，COM 链路、解码、暂停恢复已在 Wine 下自检通过；循环和统计在 Wine 下不可用，需真机） | Windows 渲染器 mpv 与 MF Media Engine 的 A/B：ADR-003 统一转 H.264 后，MF 的格式覆盖劣势基本消失，且可消除 GPL 依赖（非法律意见） | TEST_PLAN W-T3b；交接清单 2.2；README ADR-002 |
| Q-PB1（第 14 轮新增） | PB-video ≤ 0.03 核没有同类实测支撑（OBS 2% 为整机百分比，折算约 0.6 逻辑核【推断】） | 交接清单 2.1/2.4；17 章 |

## 开放
| ID | 问题 | 章节 |
|---|---|---|
| Q-W2（重建） | 在 WorkerW 下直接用 DComp visual（W-T2b），能否省掉一次子 HWND 合成 | 03 §3.4 |
| Q-W3（重建） | 每个虚拟桌面一张壁纸：依赖未公开的 IVirtualDesktopManager，MVP 不做 | 03 §3.4 |
| Q-W4（重建） | Focus Assist（专注模式）没有公开 API | 07 §7.5 |
| Q-W7（新） | MSIX 包内能否挂载 WorkerW，startupTask 是否生效（W-T8） | 11 §11.1 |
| Q-W8（新） | Go 程序 panic 退出时，WER 是否会触发 RegisterApplicationRestart | 02 §2.5 |
| Q-M3（重建） | 锁屏动态壁纸：只有非公开的 Aerial 替换路径，只在直接分发版做实验功能 | 04 §4.3 |
| Q-M6 | CGWindowList 单次调用的开销；多个全屏 Space 下的采样频率。原型已经会记录 `cgCostUs`，等真机数据 | 07 §7.6、prototype |
| Q-D2（重建） | mpv 的 `video-sync` 取值，以及循环点卡顿的实测 | 06 §6.5 |
| Q-D3（新） | MAS 审核 2.4.5(iv)：Web 壁纸里的 JS 是否算 “额外代码” | 11 §11.3 |
| Q-B2 | 真机上 WebView2 和 WKWebView 跑同一 WebGL 页面，GPU 和功耗对比 | 08 |
| Q-B3（新） | TrySuspend 期间画面显示什么（最后一帧还是黑屏）（B-T2） | 08 §8.2 |
| Q-S1（新） | Web 壁纸的 CPU 看门狗阈值（0.5 核持续 60 s）是否合理 | 10 §10.6 |
| Q-T1 | powermetrics 的 tasks 列格式随系统版本变化，脚本需要校正 | bench |
| Q-L3（第 10 轮）（**→ 已关闭（第 13 轮），见上表**；原文保留） | KWin 是否实现 wlr-layer-shell，以及 BACKGROUND 层和 Plasma 桌面容器的上下关系（第三方资料互相矛盾）。已决定 KDE 走壁纸插件，此问题只影响文档的准确性 | 05 §5.4 |
| Q-L4（第 10 轮） | mutter（GNOME X11）对 `_NET_WM_WINDOW_TYPE_DESKTOP` 窗口的层级处理，以及与 DING 桌面图标的上下关系（box 上没有窗口管理器，没能验证） | 05 §5.2 |
| Q-L5（第 10 轮）（**→ 第 13 轮转入“部分解决”，见上表**；原文保留） | Plasma 6 壁纸插件里的 QtMultimedia（FFmpeg 后端）能否走 VA-API 硬解；`evaluateScript` 切换壁纸的 API 细节 | 05 §5.4 |
| Q-L6（第 10 轮）（**→ 已关闭（第 13 轮），见上表**；原文保留） | GNOME 扩展和 Go 守护进程之间的 D-Bus 接口设计，以及纯 Go D-Bus 库的选型 | 05 §5.3 |
| Q-P1（第 11 轮；第 13 轮更新：交接清单阶段 0 逐平台关闭；第 19 轮更新：Windows 原型已在 Wine 中运行，但 Wine 不能替代真机） | `prototype/` 三个平台都只完成了编译，Windows/macOS 还**没有在真机上运行**；Linux 只在 Xvfb、没有窗口管理器的条件下验证过 | prototype/README |
| Q-P2（第 11 轮）（**→ 已关闭（第 13 轮），见上表**；原文保留） | Windows 原型的重挂退避在 UI 线程上同步执行，最长会阻塞约 15 s；产品实现要改成异步（02 章的 supervisor） | prototype/README |
| Q-P3（第 11 轮）（**→ 已关闭（第 13 轮），见上表**；原文保留） | mpv 循环时，time-pos 可能恰好在两次采样时取到相同的值，造成 STALL 误报（概率低）。产品实现应该改用 `estimated-frame-number` 或 `playback-time` 加上帧计数【推断】 | 02 §2.4 |
| Q-W11（第 16 轮） | 分层（WS_EX_LAYERED）子窗口能否接受 FLIP_DISCARD 交换链（MF 后端；mpv 默认也走 flip）。原型提供 `-swap blt` 备选 | TEST_PLAN W-T3b 步骤 3 |
| Q-W12（第 16 轮） | 真 Windows 上 `SetLoop(TRUE)` 是否无缝、是否仍会发 ENDED；`GetStatistics` 返回的 PROPVARIANT 类型 | W-T3b 步骤 5 |
| Q-L7（第 17 轮） | GNOME 49+ 去掉了 `hide_from_window_list`，扩展需改为覆盖 `get_window_actors`（Hanabi 方案）；GNOME 46–48 扩展的 Clone 方案还没在真 Shell 中运行过 | 05 §5.4.2；交接清单 6.2 |
| Q-V1（第 19 轮） | 状态机接入后的真机行为：锁屏、远程、电源、电量、省电、显示器开关、睡眠唤醒这些通知在 Wine 中只验证了注册成功，事件本身需要真机；多屏独立需要真实双屏 | TEST_PLAN W-T10；交接清单 3.1 |
| Q-V2（第 19 轮） | 遮挡/全屏判定开销：Wine 中平均约 1.1 ms、最大约 4.2 ms，超过 07 章 7.6 的 1 ms 目标；真机数值未知。若真机也超标，改为只在事件后判定、兜底轮询降到 2–5 s | TEST_PLAN W-T10 步骤 6；交接清单 3.4 |
| Q-V3（第 19 轮） | MF 后端从 L3 恢复时从头播放（syscall 不能传 double，无法调用 SetCurrentTime），mpv 能续播；若 Q-W10 选 MF，需要小段 C/汇编桩或改用 IMFMediaEngineEx 的其他接口 | prototype/README |
