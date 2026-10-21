# 12 测试与性能基准方法

## 12.1 指标定义
| 指标 | 定义 | 工具（Windows / macOS） |
|---|---|---|
| 增量 CPU | 壁纸运行时的 CPU 减去静态壁纸基线，取 10 分钟平均，单位是“核” | typeperf / WPR+WPA；`powermetrics --samplers tasks`、Activity Monitor |
| 增量封装功率 | 同上，单位 W | HWiNFO、Intel PCM；`sudo powermetrics --samplers cpu_power,gpu_power` |
| GPU 引擎占用 | Video Decode 和 3D 各自的占用；注意：GPU 频率低时百分比会偏高（Lively Wiki 的说明） | 任务管理器、PresentMon；Instruments（Metal System Trace） |
| 丢帧 | mpv `frame-drop-count`、`vo-drop-frame-count`；AVPlayerItem 的 access log | mpv IPC；AVFoundation |
| hwdec | mpv `hwdec-current`（文件打开**之后**再读，Fresco 踩过这个坑【源码】`mpvpaper.rs:856-860`） | IPC |
| 暂停延迟 | 从触发事件到 GPU 回到 0% 的时间 | 自带日志 + PresentMon |
| 内存 | Private Bytes / phys_footprint | Process Explorer；`footprint` |
| 冷启动 | 从进程启动到第一帧 present 的时间 | ETW；`os_signpost` |

## 12.2 基准矩阵
- 素材：真实的 1080p/1440p/4K 风景或动漫循环片段各 3 段，按 06 章规范转码，不要用合成噪声画面（box 的软解基线用的是合成画面，只能看量级）。
- 硬件：Intel iGPU 笔记本、NVIDIA 台式机、AMD（可选）、Apple M1 和 M3。
- 每个场景跑 3 次取中位数，报告时附原始数据。
- 真机原型见 [prototype/README.md](prototype/README.md)（里面有和测试项的对应表）。可执行的测试包见 [bench/TEST_PLAN.md](bench/TEST_PLAN.md)，采集脚本是 `bench/windows_capture.ps1` 和 `bench/macos_powermetrics.sh`（草稿，还没在真机上跑过）。原始的实测计划见 `docs/research/dynamic_wallpaper_feasibility.md` 第 6 节（T1–T7、M1–M7）。
- 新增验证项：W-T8（MSIX 包内能否挂载 WorkerW、startupTask 是否生效）；W-T9（TDR 恢复：用 `dxcap -forcetdr` 或 Windows SDK 里的 TDR 触发工具人为制造设备丢失【推断，工具可用性待确认】，检查 ≤ 10 s 内恢复）；B-T2（TrySuspend 时画面是什么状态）。

## 12.3 量化目标（【推断】，第一次实测后修订）
- 4K30 播放：增量 CPU < 0.03 核；
- L1 暂停 ≤ 500 ms 生效，暂停期间 GPU 约 0%；
- 冷启动到第一帧 < 1 s；
- 常驻内存（不含播放器）< 60 MB。
