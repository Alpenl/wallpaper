# 真机测试包（Windows / macOS）

## 当前原生产品新增验收项（不是测试结果）

以下项目针对 `wallpaperd` 当前源码；后面保留的 `wpproto` 步骤和旧 MF 从头恢复说明仍是历史原型。必须记录实际提交/二进制散列、系统/驱动、素材、结果与日志，不能用交叉编译代替。

1. F-03：独立/跨屏往返保存；双/三屏负坐标、横竖屏/错位/混合 DPI、同屏数换位/缩放、主屏切换、热插拔、Explorer/Space/睡眠恢复；视频及静态首帧的裁切接缝。保持按屏暂停，不以逐帧同步作已实现能力。见 [空间跨屏](../../SPANNING.md)。
2. F-07/08：mac 无 ffmpeg 时用原生隔离 helper；Win 可选 ffmpeg 必须有 h264_mf。选主屏物理尺寸及 1080p/1440p/2160p，检查 H.264/30fps/无音轨/偶数边界/首 IDR/GOP；测非整数帧率、旋转/竖屏、透明 GIF/disposal/末帧、取消、编码器不可用/资源上限/失败保留旧输出。avconvert 的有限回退不能关闭严格 AC。当前按屏多 rendition/cover 缓存仍需实现。
3. F-17：真实 L0–L4 与后端确认一致，跨 UTC 周一、重启、睡眠、断屏、失败和系统时钟调整不虚增；核对 state schema 2 的摘要指标。暂停时长不是节电量；MF/AVF 的 unknown 硬解状态不能冒称成功。
4. F-18：mpv/MF/AVF 支持能力分别测试 fit/fill/stretch、0.25–4 倍速度、暂停时修改、不重载保位置、L3/L4 恢复、失败反馈及 ≤200 ms 实际时延。以 [当前矩阵](../../IMPLEMENTATION_STATUS.md) 的最终源码接口为准；Web 属性仍依赖未实现 F-12。


> **这是测试计划，不是测试结果。** 本轮执行情况见 [验证记录](../../VALIDATION.md)。这里的 `wpproto` 参数属于研究原型，不直接等同于 `wallpaperd`。原文提及的 `bench/webview/webgl_test.html` 未包含在当前 checkout 中，Web 基准执行前需先补齐并记录来源，不能声称按原素材复现。
所有阈值为【推断】起始目标，首次数据出来后再校准。

## 通用
1. 重启，空闲 5 分钟。跑 baseline 120 s，再启动壁纸，等 30 s，跑 wallpaper 120 s。
2. 素材：1080p30 H.264、4K30 HEVC、4K30 AV1、bench/webview/webgl_test.html（以及 ?fps=30）。
3. 场景：桌面可见 / 全屏游戏或无边框全屏视频 / 最大化窗口遮挡 / 电池 / 锁屏 / 睡眠后唤醒 / 多屏混合 DPI / explorer 重启（Windows）。

## Windows（`windows_capture.ps1`）
- 通过：在 iGPU 机器上，视频壁纸可见时，4K30 的增量 CPU ≤ 0.03 核（17 章 PB-video；第 12 轮从 “mpv < 0.1 核” 收紧并统一）；VideoDecode 引擎 > 0（说明硬解生效，同时核对 mpv json 里的 hwdec-current）；遮挡或全屏后 ≤ 500 ms 进入 L1，暂停期间 ≤ 0.005 核（17 章 F-04 / PB-paused）；explorer 重启后 ≤3 s 恢复；睡眠唤醒后画面恢复（参照 Lively #3078/#3195）。
- 记录 Windows 版本号（24H2 / 25H2 build）以及显卡驱动版本。

## macOS（`sudo macos_powermetrics.sh`）
- 通过：在 M 系列芯片上，视频壁纸可见时宿主进程 CPU 很低（以 baseline 为对照）；全屏 App 覆盖后经过 3 次采样去抖再暂停（每次采样间隔记录在日志里）；切换 Space 和 Mission Control 时无闪烁。
- 依次测试窗口层级 desktop-1 / desktop / desktopIcon-1，记录图标是否可点、是否被遮挡（Q-M1）。

## 第 9 轮新增验证项
- **W-T8（MSIX）**：用 MSIX 打包原型（`runFullTrust`）后，WorkerW 挂载是否正常；`windows.startupTask` 在首次启动后、重新登录时是否自动运行。
- **W-T9（TDR）**：播放中人为触发 TDR（工具待确认，例如 `dxcap -forcetdr`）。通过标准：10 s 内画面恢复；日志里有 `device removed` 和重启记录；supervisor 计数 +1。
- **B-T2（TrySuspend）**：Web 壁纸进入 L1 后，桌面显示的是最后一帧还是黑屏；WebView2 子进程 CPU 是否约等于 0。
- **崩溃兜底**：用任务管理器结束 wallpaperd。通过标准：mpv/webhost 子进程 1 s 内全部消失（Job Object 生效）；桌面露出的系统壁纸和动态壁纸的首帧一致；另外，用调试开关触发一次真正的崩溃（例如访问违例），而不是用任务管理器结束进程：任务管理器结束不算崩溃，WER 不会处理【推断】。检查运行超过 60 s 后崩溃时，WER 能否把应用拉起【文档】RegisterApplicationRestart。

## 第 12 轮：原型
- 运行方法和日志字段见 [../prototype/README.md](../prototype/README.md)。W-T1/W-T3/W-T6/W-T9、崩溃兜底、M1–M4、Q-M6 都可以直接用 `wpproto` 完成。采集 CPU、GPU 和功耗时，与 `windows_capture.ps1` / `macos_powermetrics.sh` 同时运行。
- 验收阈值以 [17 章](../17-features-mvp.md) 的 AC 和 PB 为准；本文件开头的通过标准已在第 12 轮与 17 章对齐。

## 第 16 轮：W-T3b mpv 与 Media Foundation 后端 A/B（关闭 Q-W10）
前提：同一台机器、同一片源（06 章参数转码的 4K30 与 1440p30 H.264，外加 3 s 短循环片）、插电、关闭其他程序、同一显示器。每个配置跑 3 次取中位数。
1. 基线：不运行壁纸，`windows_capture.ps1` 采 5 分钟。
2. `wpproto -backend mpv -video 4k30.mp4 -duration 10m -stats 5s -log mpv.log`，同时 `windows_capture.ps1`。
3. `wpproto -backend mf  -video 4k30.mp4 -duration 10m -stats 5s -log mf.log`，同时 `windows_capture.ps1`。若日志出现 `CreateSwapChainForHwnd` 失败，加 `-swap blt` 重跑并记录。
4. 暂停期开销：两个后端各跑 `-pausetest 60s -duration 6m`，只取暂停段的 CPU/GPU。
5. 循环点：两个后端各用 3 s 片跑 5 分钟并录屏，逐帧看循环点；mf 日志里 `endedFallback` 应为 0（否则说明 SetLoop 没生效）。
6. 恢复：两个后端各做一次 explorer 重启（W-T6）和 TDR（W-T9）。
记录表（每个后端一行）：增量 CPU（核）、GPU 3D %、Video Decode %、私有内存/工作集（第 19 轮起两个后端都是子进程：mpv 后端统计 `mpv.exe`，mf 后端统计 `wpproto.exe -mfhost` 子进程，主进程单独一行）、丢帧率（mpv：`frame-drop-count`+`decoder-frame-drop-count`；mf：`dropped`+`appSkipped`，相对 `presented`）、循环点重复帧数、暂停期 CPU/GPU、W-T6/W-T9 恢复时间。
判定：MF 在 CPU、GPU、内存上都不比 mpv 差超过 10%，且循环点、恢复都通过 → 改 ADR-002 Windows 部分为 MF（并撤销 ADR-004 的 GPL 依赖，非法律意见）；否则维持 mpv。10% 这个容差是【推断】，可以在看到数据后调整。

## 第 19 轮：状态机接入后的暂停测试（W-T10，替换此前“只看前台全屏”的暂停测试）
所有项目都看日志里的 `SIGNAL`（信号到达）和 `VIS`（状态机动作）两类行，时间差即判定延迟；CPU/GPU 用 `windows_capture.ps1` 同时采集。
1. 多屏独立：`wpproto -monitor -1 -video 4k30.mp4 -log vis.log`。在屏 0 全屏播放一个视频 → 只有屏 0 出现 `Fullscreen=true` 和 `Pause`；屏 1 照常播放。再在屏 1 上最大化一个窗口 → 屏 1 `Covered=true`，约 150 ms 后 `Pause`。
2. 锁屏 Win+L → `Locked=true` → 立即 `Release`（L3）；解锁 → 300 ms 后 `Resume(L3)`，画面从原位置继续（mpv）或从头（mf，已知简化）。
3. 远程桌面连入/断开 → `Remote`；拔插电源 → `Battery`（默认策略暂停）；电量 ≤ 20% 且用电池 → `LowBattery` → `Snapshot`（L4，渲染进程被结束）；开省电模式 → `BatterySaver`。
4. 睡眠 → `Suspend=true` → L3；唤醒 → 先 `HealthCheck`，后 `Resume`；睡眠时长不会触发补升 L4（07 章 7.8 #10）。
5. 关屏（电源设置里 1 分钟关闭显示器）→ `DisplayOff=true`。
6. 开销：空闲 10 分钟记录 `PROBE_COST` 的平均/最大值（目标每次 < 1 ms，07 章 7.6）和主进程 CPU（PB-idle ≤ 0.002 核）。
7. 级别开销：`-pausetest 180s` 跑 10 分钟，分别取 L1（暂停后 0–60 s）、L3（60–120 s）、L4（120–180 s）三段的 CPU/GPU/内存。两个后端各跑一次。
