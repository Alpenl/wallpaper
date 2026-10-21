# 真机测试交接清单（第 15 轮）

> 用法：按“阶段”顺序跑；每项都写明用什么工具、通过标准、结果会关闭 14 章的哪个问题。原型二进制需从 `prototype/` 源码构建（`bin/` 不入 Git），采集脚本在 `bench/`。结果请连同日志（`-log` 输出）、`windows_capture.ps1` / `macos_powermetrics.sh` 的 CSV、录屏一起回传。
> 本清单里的阈值都来自 17 章 F-xx / PB-xx，属于【推断，目标】，首轮数据后修订。

## 1. 机型与系统版本
| 编号 | 机型（最低要求） | 系统版本 | 用途 |
|---|---|---|---|
| W-A | Intel 核显笔记本（第 11 代以后，Iris Xe） | Windows 11 24H2 → 升到 25H2 再测一次 | MVP 门槛机；24H2 Progman 分支；电池策略 |
| W-B | 台式机 + NVIDIA 独显（RTX 20 系以后），双屏 100% + 150% | Windows 11 23H2 | 旧版 WorkerW 分支、多屏混合 DPI、TDR |
| W-C | 任意 PC 或虚拟机 | Windows 10 22H2 | 旧系统兼容（F-01） |
| W-D（可选） | Snapdragon X 笔记本 | Windows 11 24H2 ARM64 | `wpproto_windows_arm64.exe`、d3d11va 在 Adreno 上是否可用 |
| M-A | M1 MacBook Air/Pro | macOS 15 | MVP 门槛机 |
| M-B | M 系列（M3 以后）+ 外接屏 | macOS 26 | level 选择、Stage Manager、多屏 |
| M-C（可选） | Intel Mac | macOS 14 | `wpproto_darwin_amd64` |
| L-1 | 任意 PC | Ubuntu 24.04（GNOME 46 X11 + Wayland） | 兼容参考 |
| L-2 | 同上 | Ubuntu 25.10 / 26.04（GNOME 49/50 Wayland） | Hanabi 路线 + D-Bus 接口 |
| L-3 | 同上 | Kubuntu 或 KDE neon（Plasma 6，Wayland） | KDE 插件、VA-API |

素材：同一组片源（4K30 H.264、1440p30 H.264、3 s 短循环片，用 06 章参数转码），以及 `bench/webview/webgl_test.html`。

## 2. 运行顺序与关闭的问题
**阶段 0：冒烟（每台机器 10 分钟）**
| # | 机器 | 操作 | 通过标准 | 关闭 |
|---|---|---|---|---|
| 0.1 | 全部 | 核对 SHA256；运行 `wpproto -video 4k.mp4 -duration 60s -log x.log` | 壁纸出现在图标之下、图标可点击；日志无 FATAL | Q-P1（每个平台各自关闭一部分） |

**阶段 1：挂载与恢复（W-A、W-B、W-C；M-A、M-B）**
| # | 机器 | 操作 | 通过标准 | 关闭 |
|---|---|---|---|---|
| 1.1 | W-A/W-B/W-C | 重启 explorer 5 次（W-T6） | 每次 ≤ 3 s 恢复；日志有 `REATTACH ok`；**期间托盘/窗口仍可响应**（验证异步重挂） | Q-W1（部分）、Q-P2 |
| 1.2 | W-A | 睡眠/唤醒 5 次、锁屏/解锁 5 次（W-T1） | 无黑屏、无残留窗口 | Q-W1（部分） |
| 1.3 | W-B | 触发 TDR（W-T9） | ≤ 10 s 画面恢复，日志有重启记录 | Q-W6 的真机部分、Q-W1 |
| 1.4 | M-A/M-B | `-level` 依次取 4 种值，各测图标点击、Mission Control、Stage Manager、全屏 Space（M1） | 记录每种 level 的表现表 | Q-M1 |

**阶段 2：性能（核心数据，W-A、W-B、M-A）**
| # | 机器 | 操作 | 通过标准 | 关闭 |
|---|---|---|---|---|
| 2.1 | W-A/W-B | 4K30 播放 10 分钟，同时跑 `windows_capture.ps1`（W-T3） | `hwdec-current=d3d11va`；增量 CPU、Video Decode/3D 占用、内存与 PB-video 对照；丢帧 ≤ 0.1% | PB-video 首个真值；ADR-002 |
| 2.2 | W-A（W-B、W-D 有条件也跑） | **同一二进制加 `-backend mf` 重复 2.1**（第 19 轮起 mf 也是子进程，按进程分别记录），完整步骤见 TEST_PLAN “W-T3b”（含 `-pausetest`、3 s 循环片、`-swap blt` 备选、W-T6/W-T9 对比） | 按 W-T3b 的判定规则 | Q-W10、可能连带 Q-L1；W-D 上还能关闭 “MF 在 ARM64 上是否硬解” |
| 2.3 | 全部视频机 | 3 s 短循环片播放 5 分钟，录屏逐帧看循环点 | 循环点重复帧 ≤ 1；日志无 STALL | Q-D2、Q-P3 的真机确认 |
| 2.4 | M-A | 4K30 播放 10 分钟 + `sudo macos_powermetrics.sh`（M3） | 与 PB-video 对照；记录 VT 硬解 | PB-video（mac）；Q-T1（校正 tasks 列格式） |
| 2.5 | M-A/M-B | 打开 1/3/5 个全屏 Space 时看 `cgCostUs` | 记录单次 CGWindowList 耗时分布 | Q-M6 |

**阶段 3：暂停与电源（W-A、M-A）**
| # | 操作 | 通过标准 | 关闭 |
|---|---|---|---|
| 3.1 | 按 TEST_PLAN **W-T10** 步骤 1–5（第 19 轮起原型已接入状态机和全部信号源；W-B 双屏上加 `-monitor -1`） | `SIGNAL`→`VIS` 延迟：L1 ≤ 500 ms；暂停时 ≤ 0.005 核、GPU ≈ 0；屏间互不影响 | PB-paused、F-04、Q-V1 |
| 3.2 | 拔电源、低电量、省电模式（W-T10 步骤 3） | ≤ 1 s 生效 | F-05、PB-battery |
| 3.4 | W-T10 步骤 6–7：判定开销与各级别开销（两个后端） | `PROBE_COST` 平均 < 1 ms；L3/L4 的内存与 GPU 释放量有记录 | 07 §7.6、Q-V2 |
| 3.3 | 开专注模式 | 记录有无可用信号 | Q-W4 |

**阶段 4：Web 壁纸（W-A、M-A）**
| # | 操作 | 通过标准 | 关闭 |
|---|---|---|---|
| 4.1 | `webgl_test.html` 在 WebView2 与 WKWebView 中各跑 10 分钟 | 记录 GPU、功耗、内存 | Q-B2、PB-web |
| 4.2 | L1 时调用 TrySuspend（B-T2），截图 | 记录画面是最后一帧还是黑屏；CPU 是否下降 | Q-B3、ADR-005 推翻条件 |
| 4.3 | 跑一个 CPU 密集页面 | 0.5 核持续 60 s 触发提示是否合理 | Q-S1 |

**阶段 5：打包（W-A）**
| # | 操作 | 通过标准 | 关闭 |
|---|---|---|---|
| 5.1 | MSIX 打包原型（W-T8），注销再登录 | 能挂载；startupTask 自动运行 | Q-W7、ADR-006 推翻条件 |
| 5.2 | 让 Go 程序主动 panic | WER 是否触发 RegisterApplicationRestart | Q-W8 |

**阶段 6：Linux 兼容参考（L-1/L-2/L-3，优先级最低）**
| # | 操作 | 通过标准 | 关闭 |
|---|---|---|---|
| 6.1 | L-1 X11：`wpproto_linux_amd64`，打开 DING 桌面图标 | 窗口在图标之下、不被 mutter 抬到上面 | Q-L4 |
| 6.2 | L-1（GNOME 46 Wayland）与 L-2 里的 GNOME 48：安装 `gnome-ext/wpd-shell@wpd.github.io`，运行 `wpdbus daemon`，注销重登后启用扩展 | mpv 画面出现在桌面背景上、图标/窗口在其上方；不出现在 Alt-Tab 和概览窗口列表；`journalctl` 里没有 `[wpd]` 报错；换壁纸、插拔显示器后画面恢复 | Q-L6 的真机部分、Q-L7 |
| 6.3 | L-3：Smart Video Wallpaper Reborn，设 `QT_FFMPEG_DECODING_HW_DEVICE_TYPES=vaapi`，对比是否设 `QT_XCB_GL_INTEGRATION=xcb_egl` | 用 `intel_gpu_top`/`radeontop` 看解码引擎；对比 CPU | Q-L5 的真机部分 |

## 3. 不需要真机、但需要律师的
- Q-L1、Q-L2（非法律意见）。若 2.2 后 Windows 改用 MF，Q-L1 可以撤销。
