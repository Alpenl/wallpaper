# 07 暂停与省电状态机（完整规格 v1.1；v1.1 的修订见 7.8，可运行实现见 `visibility/`）

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

## 7.1 原则
1. **多个原因取 OR，按显示器判定**。每个 Reason 占一位，分全局和按显示器两类；某块显示器的有效原因集合 = 全局原因 ∪ 该显示器自己的原因。只有当 “目标级别” 变化时才向渲染器下发命令（参照 mpvpaper `update_mpv_pause_state`，`src/main.c:284-307`【源码】）。
2. **快停慢启**：进入暂停的条件稳定后立即执行；恢复要等稳定 `resumeDebounce`。理由：暂停晚一点只是多耗一点电，而恢复抖动会让用户看到闪烁【推断】。
3. **分级升级**：暂停时间越长，释放的资源越多（L1→L4）。
4. **必须按显示器实例化渲染器**。span 模式下 Lively 做不到按屏暂停：两屏都被遮挡时 GPU 占用 3.7%，按屏模式是 0%【实测，第三方】signalrgb-wallpaper b1addc3。

## 7.2 Reason 定义
| 位 | Reason | 范围 | 目标级别下限 | 说明 |
|---|---|---|---|---|
| 0 | `Fullscreen` | 显示器 | L1 | 前台全屏应用（游戏、视频） |
| 1 | `Covered` | 显示器 | L1 | 被普通窗口遮挡的面积 ≥ 阈值 |
| 2 | `FocusedApp`（可选） | 全局 | L1 | 用户规则：某个应用在前台时暂停 |
| 3 | `Locked` | 全局 | **L3** | 锁屏 |
| 4 | `Remote` | 全局 | L1 | Windows 远程桌面会话 |
| 5 | `Battery` | 全局 | 由策略决定（默认 L1） | 用电池供电 |
| 6 | `BatterySaver` | 全局 | L1 | 省电模式 / 低电量模式 |
| 7 | `ThermalSerious` | 全局 | 不暂停，帧率封顶 15 | |
| 8 | `ThermalCritical` | 全局 | L1 | |
| 9 | `DisplayOff` | 显示器或全局 | **L3** | 显示器休眠 |
| 10 | `Suspend` | 全局 | **L3** | 系统睡眠前进入；唤醒后先做健康检查 |
| 11 | `User` | 全局或显示器 | L1 | 托盘里的手动暂停，**优先级最高**，不会被自动逻辑解除 |
| 12 | `Screensaver` | 全局 | L3 | |
| 13 | `LowBattery` | 全局 | **L4** | 电量 ≤ 20% 并且在用电池（阈值可配置） |

目标级别 = 所有生效原因的 “目标级别下限” 取最大值，再和计时升级的结果取最大值（7.4）。

## 7.3 状态与转移
状态 `S ∈ {Running(L0), Paused(L1), Frozen(L2), Released(L3), Snapshot(L4), Recovering, Failed}`。

| 当前 | 事件 / 条件 | 下一个 | 动作 |
|---|---|---|---|
| L0 | **下限 ≥ L1 的**有效原因非空（v1.1：只限帧的原因不触发暂停），并且通过 7.5 的去抖 | `max(下限, L1)` | 视频：mpv `pause=yes` / AVPlayer `rate=0`；Web：`IsVisible=false`，再 `TrySuspend`（v1.1：`TrySuspend` 移到 L2，见 7.8 #3）；Shader：停止 present |
| L1 | 在 L1 持续 `t12` | L2 | Web：已在 L1 做过 TrySuspend，无额外动作；视频：无（L2 对视频不起作用，直接按时间升到 L3）|
| L1/L2 | 持续 `t23`，或者下限 ≥ L3 | L3 | mpv `stop`，保留进程（`--idle=yes`）；AVPlayer `replaceCurrentItem(nil)`；Web：结束宿主进程，显示截图 |
| L3 | 持续 `t34`，或者下限 = L4 | L4 | 结束播放进程，显示截图（`render/image`） |
| L1–L4 | 有效原因变空，并且稳定 `resumeDebounce` | L0 | 按级别逆序恢复；L3/L4 恢复需要重新 `load` 并 seek 到保存的 `time-pos` |
| 任意 | 渲染器 `Done()` | Recovering | supervisor 退避重启（02 章 2.4） |
| Recovering | 重启成功 | 回到当时的目标级别 | |
| Recovering | 10 分钟内 5 次 | Failed | 显示截图，提示用户 |
| 任意 | `DisplayRemoved` | 销毁 | 关闭渲染器和 Surface |

每次进入 L1 之前，先截一帧图（如果距上次截图超过 10 分钟），给 L3/L4 和崩溃兜底使用【推断】。

## 7.4 参数（v1 默认值，全部是【推断】起点，用 TEST_PLAN 的实测校准；全部可以在 `config.json` 的 `pause` 节覆盖）
| 参数 | 默认 | 说明 |
|---|---|---|
| `resumeDebounce` | 300 ms | 恢复前要求的稳定时间 |
| `coverEnterDebounce` (Win) | 150 ms | 拖动窗口时，覆盖率在阈值附近来回跳 |
| `coverSamples` (mac) | 3 次一致 | 采纳 Wallnetic 的做法【源码】`WallpaperVisibilityPolicy.swift`；采样间隔见下 |
| `macPollInterval` | 500 ms（遮挡）/ 事件驱动（Space 切换后立即采样一次） | MacLiveEngine 用 500 ms【文档】 |
| `winPollFallback` | 1000 ms | WinEventHook 漏掉事件时的兜底轮询；Lively 用 500 ms 定时器【源码】|
| `coverThreshold` | 未覆盖 tile ≤ 5% 视为被遮挡 | 与 Lively 一致：未覆盖 tile 比例 ≤ 0.05 就判定为遮挡；另外，任一窗口最大化（`IsZoomed`）或单个窗口覆盖 ≥ 95% 时直接判定为遮挡【源码】`Lively.Common/Helpers/WindowUtil.cs:13-63`，默认值在 `SettingsModel.cs:169-173` |
| `tile` (Win 网格) | 50 px | Lively 的默认值【源码】 |
| `t12` | 2 s（Web 立即） | |
| `t23` | 60 s | |
| `t34` | 10 min | |
| `batteryPolicy` | `pause` / `fps15` / `ignore`，默认 `pause` | |
| `lowBatteryPct` | 20 | |

## 7.5 信号来源
| Reason | Windows | macOS | 依据 |
|---|---|---|---|
| Fullscreen | `EVENT_SYSTEM_FOREGROUND` 时取前台窗口：矩形等于显示器矩形、不是 cloaked、不属于 explorer/dwm/LockApp/ShellExperienceHost；另外 `SHQueryUserNotificationState == QUNS_RUNNING_D3D_FULL_SCREEN`（只表示独占全屏，而且是全局的） | 当前 Space 是全屏 Space：收到 `NSWorkspaceActiveSpaceDidChangeNotification` 后，后台跑 `CGWindowListCopyWindowInfo`，看是否有 layer 0 的窗口覆盖整个屏幕（误差 ≤ 2 px） | 【文档】QUNS；【源码】Lively `Playback.cs:339-352`；Wallnetic |
| Covered | 网格覆盖算法（tile 50），排除 `DWMWA_CLOAKED` 和最小化窗口；由 `EVENT_OBJECT_LOCATIONCHANGE/SHOW/HIDE/MINIMIZESTART|END` 触发，兜底轮询 | CGWindowList 面积覆盖率 + 3 次采样去抖；**不用 `occlusionState` 作为唯一依据**，因为它对 desktop level 窗口会来回跳变 | 【源码】Lively、Wallnetic 注释 |
| Locked | `WTSRegisterSessionNotification`（`WTS_SESSION_LOCK/UNLOCK`）/ MyGo `Power.OnLockScreen` | MyGo `Power.OnLockScreen`（`com.apple.screenIsLocked` 分布式通知） | 【源码】mygo `power.go:30` |
| Remote | `WTS_REMOTE_CONNECT` + `GetSystemMetrics(SM_REMOTESESSION)` | 不暂停（屏幕共享本来就该能看到桌面） | 【源码】Lively `Playback.cs:83-91` |
| Battery | `RegisterPowerSettingNotification(GUID_ACDC_POWER_SOURCE)`；电量用 `GUID_BATTERY_PERCENTAGE_REMAINING` | `IOPSNotificationCreateRunLoopSource` + `IOPSCopyPowerSourcesInfo` | 【文档】；MyGo 只有 `IsOnBattery()`，没有事件【源码】 |
| BatterySaver | `GUID_POWER_SAVING_STATUS` | `NSProcessInfoPowerStateDidChangeNotification` / `isLowPowerModeEnabled`（12+） | 【文档】 |
| Thermal | 没有通用 API，不做 | `NSProcessInfoThermalStateDidChangeNotification`：serious 时帧率封顶 15，critical 时 L1 | 【文档】 |
| DisplayOff | `GUID_CONSOLE_DISPLAY_STATE`（全局）| `NSWorkspaceScreensDidSleep/WakeNotification` | 【文档】 |
| Suspend | MyGo `Power.OnSuspend/OnResume`（底层是 `WM_POWERBROADCAST`）| 同左（`NSWorkspaceWillSleep/DidWake`） | 【源码】 |
| Screensaver | `SPI_GETSCREENSAVERRUNNING` 轮询（仅在 idle 时） | `com.apple.screensaver.didstart/didstop` 分布式通知 | 【文档/推断】 |
| Focus Assist | **没有公开 API**（只有未公开的 WNF）；MVP 不做 | — | 【文档】node-notification-state-ffi |

## 7.6 开销预算
- 探针空闲时必须是事件驱动；兜底轮询每次 < 1 ms CPU（Windows 网格计算，窗口数量 < 200）。目标是探针总开销 < 0.002 核【推断，目标】，由 TEST_PLAN 的 baseline 对比验证。
- macOS 的 CGWindowList 每次调用的开销还**没有实测数据**（Q-M6）。所以只有在可能发生变化的事件之后（Space 切换、应用激活或停用、窗口移动结束），才按 500 ms 节奏采样 3 次，平时不轮询【推断】。

## 7.7 测试向量（历史规格；当前生产测试见 [internal/visibility](../../internal/visibility/)）
1. Covered 抖动：0→1→0→1，间隔 100 ms → 进入 L1（快停）；最后一次 0 之后 300 ms 内不恢复。
   **v1.1 澄清**：Covered 有 150 ms 进入去抖，间隔 100 ms 的抖动不会进入暂停；最后一次变 1 并稳定 150 ms 后进入 L1。之后变 0：299 ms 时仍为 L1，满 300 ms 恢复。
2. Locked 时间超过 t23 → 直接 L3；解锁后 300 ms 回到 L0，并且 seek 到保存的位置。
   **v1.1 更正**：Locked 的下限就是 L3（7.2），所以锁屏**立即**进入 L3，不需要等 t23（原文与 7.2 矛盾）。
3. User 暂停加上全屏退出 → 保持 L1。
4. 渲染器在 L3 时崩溃 → Recovering 后回到 L3（不应该开始播放）。
5. LowBattery → L4；接上电源 → L0。

## 7.8 v1.1 修订（第 18 轮：把规格写成 `visibility/` Go 包时发现的漏洞）
历史实验的 `visibility/machine.go` / `visibility/machine_test.go` 曾记录 28 个测试全部通过、语句覆盖率 98.2%、`-race` 通过【历史实测，box】。副本已删除，当前 checkout 不能重跑这些旧结果；源码与恢复方法见[历史索引](visibility/README.md)及 Git 历史。当前生产实现与测试位于 [`internal/visibility`](../../internal/visibility/)，须独立验证，不继承上述覆盖率。
| # | 漏洞 | v1.1 规则 | 对应测试 |
|---|---|---|---|
| 1 | 向量 1 与 `coverEnterDebounce=150ms` 冲突：100 ms 抖动下“快停”无法成立 | 进入去抖按原因计算；抖动期间不暂停，稳定后进入 | `TestVector1*` |
| 2 | 向量 2 写“超过 t23 才 L3”，与 7.2 的 Locked 下限 L3 矛盾 | 下限立即生效 | `TestVector2Locked` |
| 3 | L2 对视频、Web、Shader 都没有动作（Web 的 TrySuspend 已在 L1 做了） | **Web：L1 = `IsVisible=false`，L2 = `TrySuspend`**（t12 默认 0，所以实际上紧接着执行）；视频和 Shader 跳过 L2 | `TestEscalationWeb*`、`TestEscalationVideo` |
| 4 | t12/t23/t34 从哪里起算没写 | 都从“离开 L0”起累计；恢复到 L0 后清零 | `TestEscalationVideo`、`TestEscalationResetsAfterResume` |
| 5 | 原因部分解除时（例如 LowBattery 解除但 Battery 仍在）级别是否回落没写 | 暂停中**只升不降**，只有原因全清才恢复到 L0（回落到 L1 要重新加载视频，只为了再暂停，不划算） | `TestNoDowngradeWhilePaused` |
| 6 | ThermalSerious、Battery(`fps15`) 让“有效原因非空”，按原文会进入暂停 | 这两种只发 `FPSCap`，不暂停 | `TestThermalSeriousCapsOnly`、`TestBatteryPolicies` |
| 7 | L4 时渲染进程是我们自己结束的，`Done()` 会被当作崩溃 | L4 下的 `Done()` 忽略 | `TestDoneAtL4NotCrash` |
| 8 | Recovering 期间原因变化时下不下指令、重启后放到哪一级没写 | Recovering 中只记录目标级别、不下指令；重启成功发 `RestartedAt(level)`：L0 播放、L1/L2 以暂停状态启动、L3 只起空闲进程、L4 不起进程 | `TestVector4CrashAtL3`、`TestRecoveringTracksTarget` |
| 9 | Failed 如何退出没写；“10 分钟 5 次”是否滑动窗口没写 | 滑动窗口（重启失败也计数）；Failed 只能由用户“重试”或换壁纸退出 | `TestCrashLimit` |
| 10 | 睡眠一小时后唤醒：按墙钟计时会在唤醒瞬间补升到 L4、杀掉进程，然后马上恢复 | Suspend 期间冻结升级计时；Suspend 解除时把睡眠时长从暂停累计时间里扣除，并先发 `HealthCheck` | `TestSuspendHealthCheck`、`TestSuspendThenLockedNoCatchUp` |
| 11 | User、DisplayOff 可以同时来自全局和显示器，关闭其中一个来源时是否解除没写 | 任一来源为开即为开 | `TestDualScopeReason` |
| 12 | 一次推进跨越多个截止时间时的处理顺序没写 | 按时间先后逐个处理（例如遮挡去抖完成后 60 s 才升 L3，而不是一起发生） | `TestAdvanceOrdering` |
| 13 | 新接入的显示器是否继承全局原因没写 | 继承 | `TestAddDisplayInheritsGlobal` |
其余补充测试：恢复计时中原因回来时取消恢复、10 分钟内只截一次帧、显示器移除后忽略一切事件、重复上报幂等、时间倒退 panic、范围错误（例如把 Covered 当全局原因上报）返回错误。
