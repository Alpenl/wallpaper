# 09 多显示器 / DPI / HDR

| 项 | Windows | macOS | 依据 |
|---|---|---|---|
| 枚举 | `EnumDisplayMonitors` + `GetMonitorInfo`；刷新率用 `EnumDisplaySettings`；MyGo `Screen.Displays()` 没有刷新率和 HDR 字段 | `NSScreen.screens`、`backingScaleFactor`、`maximumFramesPerSecond`；HDR 看 `maximumExtendedDynamicRangeColorComponentValue` | 【源码】mygo `modules.go:210`；其余为【文档】API |
| 坐标 | WorkerW 覆盖整个虚拟屏幕，子窗口要用 WorkerW 客户区坐标（`MapWindowPoints`），副屏可能在负坐标 | 每块屏一个 NSWindow，`setFrame:` 用全局坐标（原点在左下） | 【推断】；W-T6 验证 |
| DPI | 进程设为 Per-Monitor-V2；mpv 子 HWND 尺寸用物理像素 | 渲染缓冲区 = frame × backingScaleFactor | 【推断】 |
| 热插拔 | `WM_DISPLAYCHANGE` + MyGo `OnDisplaysChanged`，防抖 500 ms 后重建表面 | `NSApplicationDidChangeScreenParametersNotification` | 【源码】mygo |
| HDR | 24H2 的桌面改动就是为了支持 HDR 壁纸；mpv gpu-next 可以用 `--target-colorspace-hint`（Lively 有这个选项） | EDR：AVPlayerLayer 在 HDR 屏上会自动处理 HDR 视频【推断】 | 【源码】Lively `VideoMpvPlayer.cs` |
| 多屏同一个视频 | 同分辨率时可以共用一份转码文件，各屏各开一个播放器（这样能按屏暂停）；Fresco 支持同步播放 | 同左 | 【文档】Fresco README |

**HDR 默认策略**【推断】：SDR 内容在 HDR 屏上交给系统做 SDR→HDR 映射；HDR 源文件只在检测到 HDR 屏时使用，否则在导入时做色调映射，输出 SDR 版本。
