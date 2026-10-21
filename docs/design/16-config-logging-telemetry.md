# 16 配置、持久化、日志与遥测

## 16.1 目录布局
| 用途 | Windows | macOS 直接分发版 | macOS MAS 版 |
|---|---|---|---|
| 配置与状态 | `%APPDATA%\<App>\` | `~/Library/Application Support/<bundle-id>/` | 沙盒容器里的同名路径（`~/Library/Containers/<id>/Data/...`），系统自动重定向【文档】App Sandbox |
| 壁纸库和转码缓存 | `%LOCALAPPDATA%\<App>\library\`、`cache\` | `~/Library/Application Support/<id>/library`；缓存放 `~/Library/Caches/<id>/` | 容器内 |
| 日志 | `%LOCALAPPDATA%\<App>\logs\` | `~/Library/Logs/<id>/` | 容器内 |

- **MSIX 注意**：打包成 MSIX 的桌面应用写 `%APPDATA%`/`%LOCALAPPDATA%` 时，会被重定向到每个包私有的位置；卸载包时这些数据会一起删除【文档】MS Learn “Understanding how packaged desktop apps run on Windows”。所以路径一律通过 `os.UserConfigDir()` 这类 API 获取，不要写死；也不要假设 mpv 能读到 `%APPDATA%\mpv`，mpv 一律加 `--no-config`，参数全部显式传入【推断】。
- 缓存（转码产物）可以随时删掉再重建，不放在会漫游的 `%APPDATA%` 里（以免体积大的文件被漫游配置同步）【推断】。

## 16.2 文件
| 文件 | 内容 | 写入频率 |
|---|---|---|
| `config.json` | 用户设置：暂停策略（07 章 7.4）、性能档位、自启开关、遥测开关、语言 | 用户修改时写入 |
| `library.json` | 已导入壁纸的索引：`id`、包路径、`schema` 版本、来源（native/lively/we）、转码状态 | 导入或删除时写入 |
| `assignments.json` | 显示器稳定 ID → 壁纸 id，以及每块屏的覆盖设置 | 用户修改时写入 |
| `state.json` | 运行状态：每块屏的 `time-pos`（用于续播）、`cleanShutdown`、最近崩溃的时间戳 | 节流后最多每 30 s 一次，退出时再写一次 |

```json
// config.json（示例，schemaVersion 是整数，只增不减）
{ "schemaVersion": 1,
  "perf": {"profile": "balanced", "fpsCap": 30, "renderScale": 0.75},
  "pause": {"batteryPolicy": "pause", "lowBatteryPct": 20, "resumeDebounceMs": 300},
  "startup": {"launchAtLogin": true},
  "privacy": {"telemetry": false, "crashReports": "ask"},
  "ui": {"lang": "zh-CN"} }
```

## 16.3 一致性与迁移
- **原子写**：先写 `x.json.tmp`，`fsync`，再 `os.Rename` 覆盖原文件。Go 的 `os.Rename` 在 Windows 上用 `MoveFileEx(MOVEFILE_REPLACE_EXISTING)` 实现，可以覆盖已有文件【源码】Go 1.27.3 `internal/syscall/windows/syscall_windows.go:366` `Rename → MoveFileEx(from, to, MOVEFILE_REPLACE_EXISTING)`。保留一份 `x.json.bak`（上一个能正常解析的版本），主文件解析失败时回退到它。
- **写节流**：同一个文件 1 s 内的多次修改合并成一次写入【推断】。
- **迁移**：读取时如果 `schemaVersion < 当前版本`，依次执行 `migrate_N_to_N+1`，执行前先备份为 `x.json.v<N>`；如果 `schemaVersion > 当前版本`（用户降级了应用），以**只读方式**加载已知字段，并且不回写，防止丢失新版本的数据【推断】。
- **显示器标识**：
  - Windows 用 `QueryDisplayConfig` + `DisplayConfigGetDeviceInfo(DISPLAYCONFIG_DEVICE_INFO_GET_TARGET_NAME)` 拿到 `monitorDevicePath`，取哈希作为 ID【文档】；
  - macOS 用 `CGDisplayCreateUUIDFromDisplayID`【文档】；
  - 匹配不上时，按 “分辨率 + 相对位置” 模糊匹配，再匹配不上就用默认壁纸【推断】。

## 16.4 日志
- 使用 Go 标准库 `log/slog`，输出 JSON 行。**热路径（每帧或每个事件）不写日志**，只写状态变化（例如 7.3 节的每次转移，一行）、错误、生命周期事件。
- 异步写：日志条目进入一个 1024 条容量的有界 channel，由后台 goroutine 用 `bufio.Writer` 写文件，每 2 s 或每 64 条刷一次盘。channel 满了就**丢弃**，并给 `dropped` 计数加一，绝不阻塞调用方【推断，设计】。
- 滚动：单个文件 2 MiB，保留 5 个，总量 ≤ 10 MiB。
- 级别默认 `info`；诊断模式下开 `debug`，30 分钟后自动关闭。
- 子进程：mpv 用 `--log-file=<logs>/mpv-<display>.log --msg-level=all=warn`，单独滚动；我们会从中解析出 `device was removed` 等关键字，供 02 章 2.4 的健康检查使用【文档】mpv manual。
- **隐私规则（必须遵守）**：
  1. 不记录其他应用的窗口标题。遮挡探针只需要矩形；进程名也只记录类别（`fullscreen_app`），不记录具体名字。
  2. 用户素材的路径和文件名只记录 `sha256(path)[:8]`。
  3. 不记录用户名和主目录；路径写进日志前，把主目录替换成 `~`。
  4. 不记录 IP 或网络内容（Web 壁纸的 console 日志默认不收集，只在诊断模式下保留前 200 条）。

## 16.5 本地指标（始终开启，只保存在本地，不上传）
- 计数器和直方图都在内存里，每小时汇总一次写到 `metrics.json`，保留 7 天：
  - 每块屏各级别的累计时长；
  - 暂停延迟分桶（< 100、< 300、< 1000、≥ 1000 ms）；
  - 崩溃和重启次数（按渲染器类型分）；
  - hwdec 是否成功；
  - 首帧耗时。
- 用途：在设置页里展示 “本周省电时长”；用户点 “导出诊断” 时打包成 zip。zip 内容在导出前给用户预览，由用户自己决定发给谁【推断】。
- 开销：只做原子计数加上每小时一次序列化，可以忽略【推断】。

## 16.6 远程遥测（默认关闭，需要用户明确同意）
- 首次启动时询问一次，默认 “不开启”。设置里可以随时关闭，关闭后删除本地队列。
- 内容只有聚合统计：应用版本、OS 版本号、GPU 厂商 ID（PCI vendor，不包含设备序列号）、16.5 节的直方图、崩溃次数。**不包含**壁纸内容、文件名、窗口信息、显示器序列号。
- 标识：一个随机生成的安装 ID，每 90 天轮换一次，并且可以手动重置。
- 上报方式：每天最多一次批量 HTTPS POST，失败就丢弃，不重试堆积；只在接通电源、并且壁纸不是 L0 的空闲时段发送【推断】。
- 崩溃报告：Go panic 由 `recover` 捕获，再加上 `debug.SetCrashOutput` 写到本地文件【源码】Go `runtime/debug/stack.go:49`。下次启动时询问用户是否发送，默认 ask。
- 合规：MAS 要填 App 隐私标签；在 EU 地区按 GDPR 告知用途；直接分发版在隐私政策里列出以上字段【推断，非法律意见】。
