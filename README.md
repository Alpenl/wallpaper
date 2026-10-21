# Wallpaper MVP（wallpaper-app）

> 版本 **0.2.1-mvp**（预发布）。私有仓库，安装包见 GitHub Releases。

动态壁纸 MVP。它是 [`docs/design/`](docs/design/README.md) 里设计文档（第 02–17 章）和原型的正式实现。前期调研报告在 [`docs/research/`](docs/research/)：
- [`wallpaper_research.md`](docs/research/wallpaper_research.md)：市场与竞品调研
- [`dynamic_wallpaper_tech_research.md`](docs/research/dynamic_wallpaper_tech_research.md)：技术方案调研
- [`dynamic_wallpaper_feasibility.md`](docs/research/dynamic_wallpaper_feasibility.md)：可行性验证（早期原型源码在 `docs/research/wpproto_src/`）

界面截图在 [`docs/screenshots/`](docs/screenshots/)。

实现要点：
- 全部用 Go 写，`CGO_ENABLED=0`。
- 托盘和设置窗口用 MyGo v0.6.0 的原生 UI，不用 WebView。
- 壁纸由各平台的原生窗口渲染：Windows 用 mpv，Media Foundation 可选；macOS 用 AVPlayerLayer；Linux 用 X11 窗口加 mpv。

> 依据标注：【源码】读过代码，【文档】出自官方文档或设计文档，【实测】在这个盒子上跑过，【推断】没有验证。
> 除特别注明外，这里写的"验证"都是开发机上的结果：Linux Xvfb、Wine 11 和交叉编译。
> **Windows 真机**：用户反馈 0.1 版安装包能正常运行且流畅（【用户实测】，未做逐项 AC 测试）；0.2.0 的新功能还没有在 Windows 真机上跑过。**macOS 真机没有跑过。**

## 默认决策（按 DECISIONS_PENDING.md 的推荐项）

| 编号 | 默认值 | 代码位置 |
|---|---|---|
| D-01 | MVP 范围 = F-01..F-10、F-15、F-16。F-11 只导入视频，不做网页壁纸（导入网页包时报 `ErrUnsupported`） | `internal/importer` |
| D-04 | Windows 默认用 mpv，Media Foundation（`mf`）可以在设置里切换 | `config.Render.WindowsBackend`，`internal/render/mf` |
| D-06 | Windows 安装器用 NSIS，按用户安装，不需要管理员权限 | `packaging/windows/installer.nsi` |
| D-07 | 用电池时默认**暂停**，低电量阈值 **20%**（低于阈值总是进入 L4） | `config.Defaults()` |
| D-15 | **不做遥测**。`privacy.telemetry` 恒为 false，`Normalize` 会强制改回 | `internal/config/files.go` |
| 签名 | 只提供钩子脚本，没有证书 | `scripts/sign-windows.sh`、`scripts/sign-macos.sh` |
| 编码器 | 不附带任何 GPL 编码器。macOS 调系统自带的 `/usr/bin/avconvert`；Windows 和 Linux 调用户自己安装的 ffmpeg（设置或 PATH），没有就只提示"建议转码" | `internal/transcode` |
| 更新 | Ed25519 签名清单 + 按文件 SHA-256 增量更新 + 启动失败自动回滚。发布版**没有内置公钥**（私钥不在发布者手里之前不嵌入），所以检查更新会提示"未配置签名公钥" | `internal/update`、`cmd/wpupdate` |

## 构建与运行

需要 Go 1.27.3 或更新的版本。以下命令全部用 `CGO_ENABLED=0`。

```bash
go test -race ./...                  # 单元测试
scripts/build.sh                     # 交叉编译 5 个目标 → dist/<os>-<arch>/ + dist/SHA256SUMS
scripts/fetch-mpv.sh x86_64          # 下载 Windows 版 mpv.exe 到 third_party/（不提交）
scripts/package-windows.sh           # NSIS 安装器 → dist/Wallpaper-<ver>-windows-<arch>-setup.exe（需要 makensis）
scripts/package-macos.sh             # dist/Wallpaper-<ver>-macos-<arch>.app.zip
scripts/e2e-linux.sh                 # Linux Xvfb 端到端（需要 xvfb-run、mpv、ffmpeg）
scripts/e2e-update.sh                # 增量更新 / 回滚端到端（7 项）
scripts/release.sh                   # 构建 + 打包全部安装包和 SHA256SUMS
go run ./cmd/uishots                 # 用假控制器渲染所有设置页截图（浅色/深色）
go run ./cmd/wpupdate keygen|manifest|verify   # 更新签名工具
```

运行方式：
- `wallpaperd`：托盘加设置窗口，正常使用。只允许一个实例（`mygo.App.RequestSingleInstanceLock`）。再启动一次会把已有实例的设置窗口调出来。
- `wallpaperd -headless [-standalone] [-fake-monitors N] -import clip.mp4 -duration 10s -status st.json`：无 UI 模式，用于 e2e 和 Wine。macOS 不支持这个模式，因为 AppKit 需要 MyGo 的主线程循环。无 UI 模式**没有单实例锁**。
- `wallpaperd -export-diagnostics`：导出诊断包（F-16）。
- `wallpaperd -update-apply` / `-update-rollback` / `-feed URL`：应用暂存的更新、回滚到上一版、指定更新源。
- 内部子命令：`-mfhost`（MF 渲染子进程）和 `-helper probe -in f`（在子进程里解析媒体文件，把解析器崩溃隔离在主进程之外）。
- 环境变量 `WPAPP_HOME=dir` 会把配置、库、缓存和日志都放到这个目录下（便携模式和测试用）。

mpv 的查找顺序：`config.render.mpvPath` → `<exe 目录>/mpv/mpv(.exe)` → `<exe 目录>/mpv(.exe)` → `PATH`。
仓库**不提交任何二进制**，`dist/` 和 `third_party/` 都在 `.gitignore` 里。

## 目录结构（与第 02 章 2.6 的差异）

```
cmd/wallpaperd/            唯一的可执行文件：UI / headless / -mfhost / -helper
internal/app               编排器（单 goroutine 事件循环 + 每屏一个 worker goroutine）
internal/desktop{,/win,/mac,/linux}  桌面宿主接口和三个平台的实现
internal/visibility        状态机（07 章）
internal/render{,/mpv,/mf,/avf,/image}
internal/supervisor        退避策略
internal/ipc               IPC v1（NDJSON）
internal/wpk               manifest v0 + 安全解压
internal/importer          文件 / .wpk / Lively / WE 导入（02 章里拆成 importer/lively 和 importer/we，这里合成一个包）
internal/media             纯 Go 的 MP4/MOV 探测和 magic bytes 识别（02 章没有这个包，新增）
internal/transcode         转码决策 + 系统编码器钩子（02 章的 wp-transcode helper 没有单独做）
internal/config            16 章：原子写、.bak、迁移、节流
internal/telemetry         异步日志、轮转、诊断包（不上报）
internal/ui                MyGo 托盘和设置窗口（新增）：settings/library/displays/pages/shell/theme/icons
internal/ui/uidemo         截图和 UI 测试用的假控制器
internal/update            签名更新、增量计划、回滚（新增）
cmd/wpupdate               更新密钥/清单工具
cmd/uishots                设置页截图生成器
docs/design                设计文档 00–17 章、决策、bench、原型源码
docs/research              三份前期调研报告
internal/platform/win32    Job Object、重启注册、命名管道
internal/platform/macos    purego/objc 胶水
```

与第 02 章的其他差异：
- `wp-webhost`、`render/webhost`、`render/wk`、`render/shader` 没有做（属于 v1）。
- 可见性探针由各平台的宿主实现，通过 `Events()` 上报，没有单独做一个 Probe 对象。
- 02 章约定"`unsafe` 只能出现在 `platform/*`"，这条**没有做到**：`desktop/win`、`desktop/mac`、`render/mf` 里也有 FFI 代码，是从原型直接移过来的。【源码】

## 验证矩阵（除"真机"列外都是开发机上的结果）

| 项目 | Linux Xvfb | Wine 11（Windows exe） | Windows 真机 | macOS 真机 |
|---|---|---|---|---|
| `go test -race ./...`（全部包） | 通过【实测】 | 不适用（race 需要 cgo） | — | — |
| 纯逻辑包的 Windows 测试二进制（visibility、ipc、config、wpk、media、importer、supervisor、telemetry、transcode、app） | — | 全部 PASS【实测】 | 待测 | — |
| 导入 → 首帧截图 → 渲染 → 画面在动 | 通过，`scripts/e2e-linux.sh` 9 项检查全部 PASS【实测】 | `-standalone` 普通窗口下 mpv.exe 能播放并填满，首帧截图能生成【实测】 | 待测 | 待测 |
| kill 渲染器 → 退避 0.5 s 后重启 | 通过【实测】 | 通过（kill mpv.exe）【实测】 | 待测 | 不适用（AVF 在进程内运行） |
| 挂到 Progman/WorkerW 下面 | 不适用 | **验证不了**：Wine 没有 Progman，日志报 `Progman not found`【实测】 | 待测（W-T1/W-T6） | — |
| MF 后端 | 不适用 | 子进程能启动，状态是 active（Wine 用 gstreamer 实现，参考价值有限）【实测】 | 待测 | — |
| 多屏（2 块虚拟屏，`-fake-monitors 2`） | 不适用 | 2 块屏各自分配壁纸，状态都是 active【实测】 | 待测（混合 DPI） | 待测 |
| 系统壁纸兜底 | 没有 gsettings，跳过【实测】 | 走 SPI 回退路径（IDesktopWallpaper 返回 0x80040154）【实测】 | 待测 | 待测 |
| 设置窗口（MyGo 原生 UI） | 能显示（软件绘制）【实测】，并有 `ui.NewTester` 单元测试 | **不显示**：d3d11 swap chain 报 0x80004001【实测】 | 待测 | 待测 |
| 托盘 | 盒子上缺 libayatana-appindicator3，退化为直接打开窗口【实测】 | 没有验证 | 待测 | 待测 |
| NSIS 安装和卸载 | — | `/S` 静默安装：文件和 HKCU 卸载项都写入了；`/S` 卸载后目录被删除【实测】 | 待测（SmartScreen） | — |
| .app 包 | — | — | — | 只生成了 zip（权限位保留），**没有运行过** |
| 增量更新 / 回滚（`scripts/e2e-update.sh`） | 7/7 PASS【实测】 | — | 待测 | 待测 |
| 新版设置 UI（10 个页面，浅色/深色） | `ui.NewTester` 渲染截图 + 11 个交互测试通过，Xvfb 下真实窗口截图【实测】 | 不显示（同上） | 待测 | 待测 |
| 交叉编译 windows/amd64、windows/arm64、darwin/amd64、darwin/arm64、linux/amd64 | 全部成功，`go vet` 干净（`-unsafeptr=false`）【实测】 | | | |

## 第 17 章 MVP 功能完成情况

| 功能 | 状态 | 说明 |
|---|---|---|
| F-01 桌面挂载、自检、重挂 | Win 已实现，Linux 已实现，mac 已实现但没跑过 | Win：24H2+ 走 Progman 下的 WS_EX_LAYERED 子窗口，插在 DefView 下面；旧版走 WorkerW。TaskbarCreated 和每 3 s 的健康检查都会触发 ShellRestarted，然后退避重挂。AC（≤3 s 恢复、5 次都成功）**需要真机** |
| F-02 渲染器监管 | 已实现 | 退避序列 0.5/1/2/4/8… s，连续崩溃超过上限后进入 Failed 并显示首帧截图；Job Object（KILL_ON_JOB_CLOSE）；mpv 卡住检测。TDR 恢复**需要真机** |
| F-03 每屏分配、热插拔 | 已实现（0.2.1 修正，待 Win 真机复测） | 每屏壁纸和别名按**稳定 ID** 持久化：Win 取 DisplayConfig 的 monitorDevicePath → EnumDisplayDevices(EDD_GET_DEVICE_INTERFACE_NAME) → IDesktopWallpaper 路径，最后才用 GDI 名 `\\.\DISPLAYn`；mac 用 CGDisplay UUID；Linux 只支持单屏。0.2.0 的 ID 会自动迁移。名称：monitorFriendlyDeviceName → 显示器 DeviceString（"通用即插即用监视器"这类泛称不用）→ "显示器 N"；N 按物理排列（左→右、上→下）。WM_DISPLAYCHANGE 合并 500 ms 内的变化。混合 DPI**需要真机** |
| F-04 自动暂停 | Win 已实现，mac 部分实现，Linux 部分实现 | Win：前台全屏、50 px 网格遮挡、锁屏、远程桌面、显示器关闭、睡眠、屏保，按屏判定。mac：CGWindowList 采样，连续 3 次一致才翻转，锁屏和睡眠由 mygo.Power 转发。Linux：没有全屏和遮挡探针。延迟和 CPU 的 AC**需要真机** |
| F-05 电源策略 | 已实现（mac/MF 未真机验证） | 电池时可选 暂停 / 继续 / **限 15 fps**，低电量（默认 20%）进入 L4，Windows 省电模式、性能档位"省电"也限 15 fps。fps 上限：mpv 用 `vf @wpcap:fps=N` + hwdec 切到 `-copy` 变体（Linux 真 mpv 测得约 15 fps【实测】）；MF 后端节流 Transfer+Present；AVF 用 AVMutableVideoComposition（【推断】未真机）。不支持的后端会回退并在界面注明原因。mac：IOKit 电源类型 + NSProcessInfo 低电量模式和过热（serious/critical）每秒轮询（未真机）。省电效果**需要真机**测功耗 |
| F-06 视频播放 | 已实现 | mpv 参数：Win 用 d3d11va + gpu-next/d3d11，`loop-file=inf`，静音，panscan 填满。硬解、丢帧、循环点**需要真机** |
| F-07 导入转码 | 已实现（mac 未真机） | 按规则判断"需要/建议转码"；详情面板一键"优化"：macOS 调 avconvert，Win/Linux 调用户自己的 ffmpeg（H.264、30 fps、去音轨、GOP 30），生成 optimized.mp4/.mov 并重启受影响的屏幕。Linux 真 ffmpeg 测试通过【实测】 |
| F-08 静态图和 GIF | 部分实现 | 静态图不启动渲染器，直接设为系统壁纸，CPU 约为 0。GIF 在 Win 和 Linux 上由 mpv 当视频播放（盒子上抽首帧通过了）。mac 的 GIF 要先转码，avconvert 能不能处理 GIF【推断】大概率不行 |
| F-09 托盘和菜单 | 已实现 | 打开壁纸库、暂停全部（勾选）、下一张、性能档位子菜单、设置、退出。设置窗口关掉后按需重建 |
| F-10 开机自启 | 已实现 | 用 MyGo 的 `SetOpenAtLogin`（Win 写 HKCU Run，mac 用 SMAppService 或 LaunchAgent），默认关闭；首次运行弹模态对话框询问 |
| F-11 Lively/WE 导入（只做 video） | 已实现 | 有单元测试和样例包。web 类型会给出明确原因 |
| F-15 安装与更新 | 已实现（真机未验证） | NSIS 安装器（Wine 静默安装/卸载通过）；Ed25519 签名清单、SHA-256 增量下载、暂存到 `.update/`、重启应用、连续 2 次启动未标记成功即自动回滚，可手动回滚；`scripts/e2e-update.sh` 7/7 PASS【实测】。发布版没有内置公钥，所以线上更新默认不可用；macOS 按文件替换后代码签名是否仍有效**未验证**；签名证书只有钩子 |
| F-16 诊断包 | 已实现 | 导出的 zip 里路径换成 sha256 前缀，标题类字段被去掉，mpv 日志只保留警告和错误；有自动化测试检查不泄露 |

## 多显示器规则（0.2.1）

- **导入只加入壁纸库**，不会改动任何显示器。可在"外观 → 导入后"改成"应用到当前显示器"（显示器页选中的那块，没选时为主屏）。
- "应用到"一块显示器 = 写该屏的单独设置；"所有显示器" = 设为默认壁纸并清除所有单独设置；"默认壁纸"只影响没有单独设置的屏和以后新接入的屏。
- 托盘"下一张"：每块屏各自换到下一张。
- 别名按稳定 ID 存在 `assignments.json` 的 `aliases` 里，换接口顺序或重启后仍对应同一块屏；保存空名或与系统名称相同的名字等于恢复默认。

## 设置界面（0.2.0 重做）

对标调研文档里 Wallpaper Engine、Lively、Wallnetic 等产品的布局：左侧导航 + 状态卡，右侧内容页。

| 页面 | 内容 | 状态 |
|---|---|---|
| 壁纸库 | 网格/列表视图、类型筛选、搜索、详情面板（"应用到"：某一块显示器 / 所有显示器，默认是当前显示器；双击 = 应用到当前显示器；转码优化、删除确认）、拖放导入、"更多导入"菜单（文件 / .wpk / Lively / WE） | 已实现 |
| 显示器 | 多屏布局画布 + 编号选择条（编号按物理排列）、每屏卡片：名称/重命名/恢复默认名称、单独壁纸或跟随默认、状态、暂停原因、fps 上限 | 已实现 |
| 发现 | 网页 / Shader 壁纸、创意工坊等 v1 规划介绍 | 占位（"开发中"） |
| 性能 | 性能档位、渲染后端、ffmpeg 路径 | 已实现；渲染分辨率等占位 |
| 暂停规则 | 全屏、遮挡、锁屏、远程桌面等 | 已实现 |
| 电源 | 电池策略（暂停/继续/15 fps）、低电量阈值、当前电源状态 | 已实现 |
| 外观 | 主题（跟随系统/浅色/深色）、强调色、库视图 | 已实现 |
| 启动与更新 | 开机自启、检查更新、自动检查、回滚 | 已实现（无公钥时提示原因） |
| 诊断 | 导出诊断包、打开日志目录、重置设置 | 已实现 |
| 关于 | 版本、许可、隐私（不做遥测） | 已实现 |

规划在 v1 的功能先做了界面，带"开发中"标签，点击后弹出"正在开发中"提示。目前有：跨屏拼接、填充方式、单独暂停某块屏、选择文件夹导入、网页壁纸、Shader 壁纸、创意工坊、壁纸属性面板、渲染分辨率、壁纸声音、导入时自动转码、按应用暂停、英文界面、启动时不打开设置窗口。

## MyGo workaround（【源码】mygo@v0.6.0）

1. 默认关掉最后一个窗口就会退出应用。这里注册了空的 `App.OnWindowAllClosed` 让托盘常驻，设置窗口关掉后按需重建。
2. 托盘菜单的 `Checked` 要整份 `SetMenu` 才会刷新，所以只在"暂停全部"状态变化时重建菜单。
3. 托盘图标要 PNG 字节。程序运行时用 `image/png` 画一个 32×32 图标，不提交二进制资源。
4. 退出顺序：不能在 `OnWillQuit`（主线程）里同步等待事件循环收尾，因为 mac 上收尾要 `RunOnMain`，会死锁。托盘里点"退出"时，先 cancel，等收尾完成（最多 5 s），再 `App.Quit()`。
5. Windows 桌面宿主用自己的 `LockOSThread` 线程和消息循环，不借用 MyGo 的主线程（MyGo 的窗口是 DirectComposition 的顶层窗口，不能做 WS_CHILD，见 02 章）。mac 的 AppKit 调用全部通过 `macos.Main = mygo.RunOnMain`。
6. 同一个 Switch 和 Text 用同一个标签时，`ui.Tester.Click` 会点到 Text。所以开关的无障碍标签都加了后缀"开关"。
7. MyGo 在 init 阶段就会从 `os.Args` 里去掉 `--mygo-opened-at-login`，所以 Go 的 `flag` 解析不会因为这个参数报错。

## 已知限制和真机待办

- **Windows 真机**：Progman/WorkerW 挂载（23H2、24H2、25H2）、Explorer 重启后重挂、遮挡和全屏探针的延迟、d3d11va 硬解、MF 后端、混合 DPI、IDesktopWallpaper 按屏设置、安装器的 SmartScreen 提示、开机自启。
- **macOS 真机**：整条路径都没跑过，包括 desktop level 窗口、AVPlayerLooper、CGWindowList 探针、`setDesktopImageURL`、qlmanage 截图、avconvert 转码、.app 启动、LSUIElement。
- Linux 只做到接口级：单屏，没有全屏和遮挡探针，Wayland 下窗口不会变成壁纸（05 章）。
- mpv 在 Xvfb 下退回到 SDL vo（没有 GL），画面不缩放，这是盒子环境的问题，不是产品问题。【实测】
- AVF 渲染器的 L3 没有释放 AVPlayer。AVF 和 MF 的 fps 上限、mac 电源轮询都没有真机验证；mpv 的 `-copy` 解码 + fps 滤镜是否真的更省电需要真机测功耗。
- 更新公钥没有嵌入发布版：需要发布者用 `wpupdate keygen` 生成密钥、`WPAPP_UPDATE_PUB` 构建后才能启用在线更新。
- Windows 下 MyGo 设置窗口在 Wine 里无法显示（d3d11），真机上的 0.2.0 新界面待用户确认。
- 许可（**非法律意见**）：第三方 mpv 构建（shinchiro）的许可证和它包含的组件，发布前必须核对，见 DECISIONS_PENDING 的 L-1..L-4。`fetch-mpv.sh` 会把下载来源和 sha256 记到 `SOURCE.txt`，安装器也会带上这个文件。
