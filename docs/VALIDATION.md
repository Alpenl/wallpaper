# 本轮验证记录与发布门槛

**正式应用基线为主分支 `2710033ef169852bd7c4a1b5c063d29f3f3e89fd`。原生改动已经移入主分支工作区，React 仅作设计参考且不包含在交付源码中。新增原生 MVP 实现已完成本轮根模块普通/race、vet、无 cgo、统一五目标构建与文档检查；最后 Windows MF 生命周期修正之后，16:48:32–16:49 UTC 又完成整树统一检查及五目标构建，另保留聚焦检查记录。16:08–16:09 UTC 的旧布局检查点独立保留，不能冒充新增源码的结果。Windows/macOS 真机与发行门槛仍未关闭。**

本文件保留本轮已实际执行的阶段验证；旧 README 中的 Wine/Xvfb/安装器/更新 e2e 结果保存在 [历史快照](history/IMPLEMENTATION_0.2.1.md)，不能复制为本轮 PASS。

## Issue #22 托盘真机验收范围

PR #23 描述记录“Mac 界面：用户已真机验收通过”，但原验证文档仅列以下步骤，
未提供逐项实测证据；不把该概括视为每个边界均已通过。Windows 测试机恢复后补测。
2026-10-25 合并包新增实测见下节，其余条目仍是验收步骤。

| 场景 | 操作与预期 |
|---|---|
| Windows 鼠标 | 左键单击托盘图标打开/聚焦壁纸库；右键弹菜单，暂停、切换、性能档位、设置、退出可用 |
| Mac 菜单栏 | 点击菜单栏图标弹菜单，打开库与设置、关闭后重开窗口正常；图标旁无新增文字 |
| 启动与 ToolTip | 全局手动暂停后重启，首次菜单勾选正确；普通提示为“动态壁纸”，全局暂停为“动态壁纸 · 已手动暂停全部”；自动暂停或单屏暂停不冒充全局手动暂停；切换英文复查 |
| 安全模式恢复 | 进入安全模式，顶部禁用提示及分隔线、退出安全模式入口可见；隐藏暂停/切换/显示器项，保留库/性能/设置/退出；提示为“动态壁纸 · 安全模式”，优先于全局暂停；退出安全模式后原全局及按屏手动暂停仍保留 |
| 按屏暂停 | 单屏不出现显示器子菜单；两屏以上在 independent 和 span 下均出现。逐屏暂停/恢复只影响目标输出；全局暂停后恢复不清除按屏暂停；核对编号、别名、勾选状态及其它播放 Options 不变 |
| 菜单展开期间变化 | 菜单展开时从窗口修改别名、按屏 Options、全局暂停或性能档位，再检查刷新；改变屏幕顺序、拔插屏幕后检查编号/顺序/项目；旧菜单回调只按稳定 ID 操作最新 Options，已断开屏不误操作；持续 FPS/暂停原因更新不应反复重建菜单 |

### 主分支合并包 test.5 本机测试（2026-10-25）

[PR #23](https://github.com/Alpenl/wallpaper/pull/23) 状态栏改动与
[PR #20](https://github.com/Alpenl/wallpaper/pull/20) 播放恢复/打包改动均已合入远程
`master`，合并代码提交 `7a13dbf99287502ad1b8aff8b3be9f82bb6163ca`。
合并时仅 `VALIDATION.md` 的同位置新增内容冲突，已保留双方内容；
`shell.go` 自动合并保留菜单改动和文件选择父窗口修复。

合并后 `bash scripts/check.sh`（包括七组 Tray 测试、全量 race/vet/无 CGO 构建、文档检查）
和五目标构建通过；远程该合并提交的 CI、Docs 均成功。
从干净提交构建 `0.3.4-mac-test.5`，arm64 ZIP/DMG 位于
`dist/master-mac-acceptance/packages/`，包内 BUILD-INFO 为上述提交、`dirty=false`。
已验证 ZIP CRC、严格 bundle 签名、DMG 校验和、执行版本；
[包 SHA256](validation/macos-2026-10-25/master-package-sha256.txt) 已保存。
仍为 ad-hoc 包，无 Developer ID/公证，未发布正式版本。

实际运行 ZIP 解出的完整应用，使用同一隔离库/素材；
[完整会话日志](validation/macos-2026-10-25/master-gui.jsonl) 为 15:50:54–15:58:43。

| 项目 | 本轮结果 |
|---|---|
| 窗口内暂停/恢复及底部状态栏 | 真机点击“暂停全部”，显示“已全部暂停、0 块屏播放 · 1 块屏暂停”；点击“继续播放”恢复“正在播放、1 块屏播放 · 0 块屏暂停”。日志 L0→L1→L0 均成功，暂停保存 434.1888 秒，恢复 434.1891 秒 |
| 设置与驻留 | 点击设置打开原生“播放与性能”；关闭窗口后进程继续驻留，桌面视频仍播放 |
| 实际桌面画面 | 本轮关闭设置窗口后工具成功读取到桌面视频表面，连续观察到时间码 478→488；[保存画面](validation/macos-2026-10-25/master-desktop.png) 为 488。仅证明这些时刻有画面且继续播放，不替代恢复瞬间无黑屏的连续观察 |
| 再次启动与退出 | 再次执行相同安装包后第二实例退出，原进程重建设置窗口；从原生 Wallpaper 应用菜单选择退出后，renderer 正常关闭，进程退出，会话无错误 |
| 系统菜单栏状态图标 | 用户手动打开 DMG 中的完整应用，提供展开菜单截图，并逐项确认：暂停全部的勾选/取消正常，“打开壁纸库”“设置…”均能打开。当前为未设置壁纸的普通用户库；本项证明菜单交互，不作为实际播放暂停或其它子菜单的验收 |
| 其他边界 | 单屏设备，双屏 independent/span、热插拔仍未实测；安全模式恢复/英文 ToolTip 本轮仅有单元测试覆盖；Windows 真机仍待测。test.4 遮挡/L3/短锁屏的真人证据保留在后文，本轮不改标为 test.5 新实测 |

测试已退出，隔离库 config/library/assignments 恢复本轮启动前备份；
系统低电量模式恢复“仅使用电池时”，底层电池/适配器读数分别为 1/0。

## 状态菜单增强 test.6（2026-10-25）

针对用户反馈菜单外观简陋、功能不足，参考
[LiveWall 原生菜单实现](https://github.com/roshworldwide/livewall/blob/75d5bd1cc3353077728cc5eec040c9e9e7bb0067/LiveWall/UI/MenuBarController.swift)
的单色模板图标、当前壁纸信息、快速切换和自动暂停入口，以及
[XanderSchmitz/LiveWall](https://github.com/XanderSchmitz/LiveWall)
公开介绍的菜单播放控制与图库入口。仅参考交互结构，没有复制代码或资源。

本轮仍使用原生系统菜单；macOS 改为自动适配明暗外观的单色图标，
按当前状态、壁纸操作、播放策略和应用入口分组。
新增当前壁纸/暂停原因、最近加入的最多 8 张壁纸快速切换（多屏时明确应用到全部显示器）、
导入、显示器设置、遮挡/全屏自动暂停开关、更多电源设置及版本/诊断入口。
空库隐藏无效的播放控制，但保留清除全局手动暂停的入口；安全模式不允许快速应用壁纸。
菜单回调使用最新配置和稳定壁纸 ID，已删除项目不误应用；
FPS、丢帧计数和同一原因下 L1→L3 不触发菜单重建。

`bash scripts/check.sh` 全量检查（含 race、vet、无 CGO 构建和文档检查）通过，
五目标构建通过。新增测试覆盖快速切换、过期菜单项/安全模式、数量与标题边界、
多屏选择标记、暂停原因、最新配置切换、空库和英文菜单。
`0.3.4-mac-test.6` 基于 `9a96e2d` 加 `codex/status-menu` 未提交改动构建，
BUILD-INFO 如实标为 `dirty=true`，arm64 ZIP/DMG 位于 `dist/status-menu/packages/`。
未签 Developer ID、未公证，使用 ad-hoc bundle 签名。

ZIP CRC、解压后严格 bundle 签名及执行版本检查通过，DMG 校验通过；
[包 SHA256](validation/macos-2026-10-25/status-menu-package-sha256.txt) 已保存。
旧 test.5 从应用菜单正常退出后，已实际启动 ZIP 解出的 test.6 完整应用，
原生壁纸库窗口正常显示，沿用普通用户库的空库状态。
用户随后在 test.6 完整应用中确认“都没有问题”：新版单色图标及菜单分组/文字正常，
“导入壁纸…”能打开文件选择框，“自动暂停”子菜单两个选项的勾选显示正常。
这是本轮新版系统菜单的真人验收；当前为空库，不据此宣称快速切换壁纸、
实际播放暂停、多屏操作或自动暂停策略切换后的运行行为已经真机通过。

### macOS 标题栏布局 test.7（2026-10-25）

针对用户截图中交通灯、显示器信息和两排导航拥挤的问题，macOS 主导航移至
标题栏左侧并在交通灯后预留至少 16 点间隔（内容起点至少 96 点），显示器名称及
选择入口移到右侧，合并原重复的“显示器 N / 选择显示器”。设置和显示器页只保留
一行主导航，壁纸库另保留搜索/筛选/导入工具行；窄窗口隐藏可在选择菜单查看的
屏幕长名称。分隔线改为低对比度细线。

新增布局检查覆盖中英文、900/1120/1470 点窗口宽度及三个主页面，确认导航与
显示器选择不覆盖交通灯、彼此或窗口边界，显示器菜单不被拖动区域拦截。
相关布局回归与 `bash scripts/check.sh`（全量 race、vet、无 CGO 构建、文档检查）通过。
本轮 arm64 构建及解压后严格 bundle 签名检查通过，实际打开 test.7 完整应用，
检查壁纸库与设置页，并点击右侧显示器按钮成功展开内建主屏菜单。
[设置页真机截图](validation/macos-2026-10-25/header-settings-test7.png) 已保存。

本地 ad-hoc ZIP 位于
`dist/status-menu/header-packages/Wallpaper-0.3.4-mac-test.7-macos-arm64.app.zip`，
来源为 `9a96e2d` 加当前未提交改动。本轮仅验证布局及交互，不重复声明播放恢复验收。

### macOS 最终紧凑布局 test.13（2026-10-26）

经用户逐轮截图反馈，最终布局替代上方 test.7 的标题栏方案：交通灯与显示器
选择位于标题行，主导航、搜索、筛选和导入共用下一行。显示器标题使用 13 点、
500 字重，在壁纸库左侧面板内居中，不计右侧 320 点详情面板；普通窗口和全屏
均保持相同居中基准。空库卡片与工具栏使用统一的 12 点左右边距。

`go test ./internal/ui` 通过，覆盖 900/1470/2048 点窗口宽度及普通窗口/全屏
反复切换时的标题居中、工具栏同行与交通灯避让。截图生成器补齐 macOS
交通灯占位模拟，普通窗口渲染已检查。test.13 arm64 ZIP 解压后严格签名与
执行版本检查通过，并已打开完整应用供用户查看；不据此增加播放行为验收结论。
测试包位于 `dist/status-menu/header-packages/Wallpaper-0.3.4-mac-test.13-macos-arm64.app.zip`。

## macOS 真机补验（2026-10-25，Singapore UTC+8）

环境：Apple M4 / arm64，macOS 27.0.1（26A434），内建屏 1470×956 点、
2940×1912 backing 像素。基线 `813de4c` 加本次未提交修改；不是已发布版本。
素材为本机 `wp-matrix/timecode.mp4`：H.264、1920×1080、30 fps、600 秒、无音轨，
关键帧最长间隔 250 帧；SHA256
`1bbd8522676ccc994518db03f6d97a305bc8b11c4dfd83b9bb24436e40416091`。

测试通过真实桌面 Host/Surface 和生产 AVF renderer 执行，不是假渲染器。
[修复前记录](validation/macos-2026-10-25/before.jsonl)与
[修复后记录](validation/macos-2026-10-25/after.jsonl)保留逐阶段采样。
复跑入口：`go run ./scripts/mac-acceptance -video /absolute/path/to/timecode.mp4`；
此命令会在主屏显示视频约三分钟，包含 599 秒循环边界检查，要求上述 600 秒素材。
它直接控制渲染等级，不代表系统锁屏/睡眠事件已验收。

| 项目 | 本轮结果与证据边界 |
|---|---|
| 播放、循环 | 播放位置持续推进；599 秒起播后 loopCount=1、位置回到 0.266 秒。未做逐帧录屏分析，不能声称无缝、无丢帧或已确认硬解 |
| L1/L3 | L1 停止推进；L3 后释放播放器资源。初始 IOSurface regions 19→5，线程 19→15；五次循环播放阶段 19–20、L3 阶段 5–6。不是显存字节测量或零显存承诺 |
| L3 恢复 | 初次及五次重复恢复全部成功，最后属性边界恢复也成功（共 7 次）；没有 Done 错误。位置推进观察约 10–1284 ms，包含缓存采样影响，**不是首帧延迟或 ≤500 ms AC** |
| 精确续播修复 | 原默认 seek 落到附近关键帧：修复前最后一次恢复后的暂停位置为 75 秒，而恢复前采样为 77.717 秒。改为前后零容差 seek；13.2、38.15、599 秒的暂停实测位置均与目标一致 |
| L3 中改属性→L1→L0 | Volume=37 更新成功；L1 稳定观察 3 秒，70.226082→70.226082，随后 L0 继续推进 |
| 内存 | 修复后 phys_footprint 约 20.9→23.7 MB；五次循环不足以排除长期泄漏，不能据此关闭长时间资源验收 |
| 应用包启动/导入 | ad-hoc `.app` 实际启动，原生设置窗口正常；修复文件/文件夹选择面板的 Parent 绑定后，原生面板可保持打开并成功导入、应用 timecode 视频 |
| 应用层自动 L3 | 本机系统低电量模式开启，UI 如实显示“使用电池、省电模式”；生产日志确认 L1→L3，set_level 返回成功。没有更改系统电源设置，因此本轮完整应用的自动恢复未验收；恢复证据来自上述真实 renderer 直接控制 |
| 原生优化编码 | 实测发现 macOS 返回 adaptor 的 420v/YUV pool，BGRA 绘制路径拒绝该布局；改为自有、有界 BGRA pool，并检查格式/宽度/步长。真实原生编码集成测试通过 GIF→H.264、重采样、去音轨、HEVC 输入与拒绝覆盖 |
| 打包 | 本机 DuckBrowser 为 ad-hoc bundle；采用相同无证书封装方式，严格校验签名、补齐标准 bundle 元数据，支持可选拖入 Applications 的 DMG；无 Developer ID、无公证、不改变 Gatekeeper |

本轮自动化：`CROSS_BUILD=1 VERSION=0.3.4-mac-test.1 bash scripts/check.sh`
通过（格式、规则、全库 race、vet、命令构建、五目标构建及文档检查）；
`WP_NATIVEAV_INTEGRATION=1 go test ./internal/transcode/nativeav -run TestNativeAVIntegration -count=1 -v`
通过；打包守卫 5 项、发布脚本测试 57 项通过。
真实生产 helper 的 2940×1912 cover 转码也通过：1 秒/30 帧、H.264、30 fps、
无音轨、恒定帧率、最大关键帧间隔 30。

本地产物位于 `dist/mac-acceptance/packages/`：arm64 与 amd64 的 `.app.zip`、
`.dmg` 和 `SHA256SUMS`。两种架构均通过 ZIP CRC/执行权限、去签名后的代码
来源一致性、解压及只读挂载后的 `codesign --verify --strict`、DMG 校验和与
Applications 快捷方式检查；只有 arm64 做过真机执行。签名变化允许、代码
字节损坏拒绝的校验也通过。测试进程已退出，测试挂载均已卸载。
GUI 导入、库和设置使用隔离的 `WPAPP_HOME`，未安装到 Applications、未提交或发布。
桌面层视频窗口未能通过当前 UI 工具取得截图，因此不把播放位置/循环计数
当作独立的可见画面验收。

尚未验收：逐帧画面/黑屏恢复的独立录屏检查、真实系统锁屏/睡眠唤醒、
多屏/Space、功耗、长时间泄漏、Intel 真机、下载隔离属性下的 Gatekeeper、
Developer ID/公证。旧文中“仅交叉编译/全部未测”的概括不得覆盖本节，
本节也不替代其他平台或其他功能的验收。

## macOS 系统事件与完整应用补验（2026-10-25）

继续使用前节 Apple M4、同一 timecode 素材和隔离测试库，执行完整 MyGo
应用而非 renderer harness。临时把系统低电量模式从“仅使用电池时”改为“永不”，
隔离库 batteryPolicy=ignore、fit=fit（使用共享原视频，不插入后台转码），
遮挡规则保持启用；没有关闭锁屏认证或修改系统睡眠/安全策略。
本轮新增 `transition_begin/end.position_s`，记录线程安全的缓存播放位置；
它可能滞后约一个采样周期，不等同于首帧时刻或画面证据。

[完整应用转换记录](validation/macos-2026-10-25/system-app.jsonl)与
[系统睡眠唤醒记录](validation/macos-2026-10-25/sleep-wake.txt)交叉核对：

| 场景 | 记录与结论 |
|---|---|
| 真实合盖睡眠、唤醒 | 系统 14:26:31 Sleep→14:33:19 Wake，睡眠 408 秒；应用 14:26:26.851 收到 Suspend 后 L3，转换耗时 2 ms；睡前缓存 409.834426 秒，唤醒恢复前仍为 409.834426 秒，恢复后 410.075106 秒，无错误。不是睡了数分钟后仍偷偷持续播放 |
| 唤醒后仍锁定 | 14:33:19.510 已唤醒但保持 L3，原因含 Locked；14:33:20.683 解除后才开始 L3→L0。未提前越过锁屏原因 |
| 真人画面观察 | 用户先报告三次恢复后有画面且数字持续变化；后续报告 407→412、锁屏超过两分钟。系统记录证实后一次包含 408 秒合盖睡眠和中间短暂解锁，因此不能把两次读数的全部差值归因于锁屏偷跑，也不能作为纯锁屏时延测量 |

最初取日志早于锁屏操作完成，曾误判“未接收到锁屏信号”；补齐时间线后已纠正。
原有 MyGo 信号接入实际产生了 Locked/Suspend 与对应 L3/恢复，不引入未经实测
支持的替代状态机或系统信号框架。系统锁屏通知进一步与 [loginwindow 通知时间](validation/macos-2026-10-25/system-lock-notifications.jsonl)
逐条核对：14:50:20.171→应用 L3 14:50:20.173、14:50:31.869→31.873、
14:50:58.853→58.855，通知到应用约 2–4 ms，没有本次怀疑的数十秒交付延迟。
14:50:33.219（约 480 秒）到 14:50:58.855（约 505 秒）期间系统为解锁状态；
最后一次实际锁定为 14:50:58.853→14:51:03.208（约 4.35 秒）。因此用户报告的
“纯锁屏约 20 秒，480→506”与系统持续锁定时间不一致，不能据此判定应用偷跑，
也不能据此把持续 20 秒纯锁屏标为通过。保留这一证据差异，等待受控复测。

在此前基础上已完成 `bash scripts/check.sh` 全部检查；本轮代码仅补充转换位置日志，
未替换已确认工作的系统信号接入。生成 `0.3.4-mac-test.2` arm64 ZIP/DMG，
本地位于 `dist/mac-system-acceptance/packages/`；ZIP CRC、可执行权限、
包签名与执行版本检查通过，DMG 校验通过，仍为无 Developer ID/无公证的 ad-hoc 包。

### L3 恢复黑屏修复与开源对照

用户随后在完整应用中报告“遮挡超过两分钟，回到桌面黑屏约两秒后出现动画，
数字约 560”。这是真人可见画面失败，不能因 `set_level` 返回成功而记通过。
旧 AVF 实现释放 AVPlayerLayer 后留下黑色表面，恢复时新图层尚无可见内容。

源码对照（仅阅读，未引入外部代码/依赖）：
[LiveWall WallpaperEngine](https://github.com/roshworldwide/livewall/blob/75d5bd1cc3353077728cc5eec040c9e9e7bb0067/LiveWall/Engine/WallpaperEngine.swift)
在覆盖/暂停策略中保留播放器并调用 pause；
[Lively 性能文档](https://github.com/lively-community/lively/wiki/Performance)
区分 Pause 与 Kill。本项目保留 L3 释放语义，采用独立静态帧过渡。
[Apple readyForDisplay](https://developer.apple.com/documentation/avfoundation/avplayerlayer/isreadyfordisplay)
及本机 SDK `AVPlayerLayer.h` 明确：readyForDisplay=false 时没有用户可见的视频内容，
应等待它再显示图层。

修复：在渲染 worker 上按暂停位置提取最长边不超过 1920 像素的一张 CGImage，
先设为 surface root 的静态内容，再释放播放器/looper/视频图层；L3 恢复保留该图，
新 AVPlayerLayer 起初隐藏，实测 readyForDisplay 后无隐式动画地一次切换，
释放静态图。最终 Close 清理静态内容。仍然需要重新加载/seek，不承诺零恢复延迟。

[原生图层回归记录](validation/macos-2026-10-25/still-handoff.jsonl)：
真实框架下初次及一次重复 L3，`held=true`、视频子图层数为 0；
两次恢复均为 `ready=true`、静态图已移除，精确暂停 seek、末尾循环、
L3 中改属性→L1 不偷跑→L0 继续仍通过。IOSurface regions 播放 24–25、
初轮 L3 为 11、加快版为 6；保留静态图有成本，不再沿用旧实现 5–6 的资源数字，不声称零显存。
全量 `scripts/check.sh` 已通过。用户复测报告“返回桌面后无黑屏，约 2 秒后继续播放动画”；该次对应转换
尚有 L1/L3 时间对齐歧义，未单独据此关闭 L3 画面门槛。随后保持遮挡，
15:13:01.990→15:13:02.106 确认完整应用 L1→L3（115 ms，含静态帧提取），
位置 39.516361 秒，用户最终明确回复“可以，无黑屏、约 2 秒后继续动画”，结合日志与原生图层回归，
关闭本机该素材的遮挡 L3 恢复黑屏问题。之后又观察到 15:15:41 L3、
15:16:36 L3→L0，恢复前后均为 47.002780 秒，无错误；不是零恢复延迟验收。

遮挡延迟属于既有策略：宿主每秒一次、连续三次确认，加状态机 150 ms 防抖，
因而遮挡后通常还会播放约 2–3 秒；解除遮挡也经过采样确认和 300 ms 恢复防抖。
**L1 才是停止播放，60 秒是进一步进入 L3 释放资源，不是等 60 秒才暂停。**
用户指出暂停/播放仍有明显间隔，随后将 macOS 遮挡采样从 1 秒改为 250 ms，
保留连续 3 样本及 150 ms 进入/300 ms 恢复防抖；理论稳定变化到暂停约
0.65–0.9 秒，到发起恢复约 0.8–1.05 秒，不含窗口动画/新视频首帧。
拓扑检测改用单独 5 秒 deadline，避免加快采样导致同秒重复枚举。
AVF 临时 metadata/首帧就绪采样改为 50 ms，正常播放仍为 1 Hz。
更频繁的 CGWindowList 采样有资源成本，本轮未做整机功耗认证。
上述数字为参数推导，不能标记为真机端到端时延实测。
加快后的 [原生框架回归](validation/macos-2026-10-25/responsive-handoff.jsonl)
通过精确 seek、循环边界、L3 静态帧/零视频图层、就绪切回、属性修改与暂停保持；
两次位置推进观察约 1.13/1.17 秒，仍包含位置采样影响，不等同于首帧延迟。
`bash scripts/check.sh` 与五目标构建通过；最终 test.4 arm64 ZIP/DMG
位于 `dist/mac-system-acceptance/responsive-packages/`，CRC、执行版本、
严格 bundle 签名、DMG 校验通过。此检查点尚未复测完整 GUI 主观速度；
随后完整应用复测结果见下一节，仍不把参数推导当作实测。
测试进程已退出，系统低电量模式已恢复“仅使用电池时”，隔离库 config/library
恢复测试前备份；未变更实际用户库、未安装/提交/发布。

用户“80→83、再次遮挡后 88”的观察对应 L1 短遮挡，日志显示暂停时停止推进；
不把这几秒的确认延迟归类为锁屏或 L3 偷跑。

### test.4 完整界面响应复测（2026-10-25 15:26–15:31，UTC+8）

从已校验 ZIP 解出 `0.3.4-mac-test.4`，严格 bundle 签名与执行版本检查通过；
启动完整 MyGo 应用，启动日志 `headless=false`、`safeMode=false`。
沿用本机内建屏、同一 timecode 视频与隔离库，临时关闭系统低电量模式、
设置 batteryPolicy=ignore、fit=fit。由用户操作其他窗口遮挡/显示桌面，
工具只读取应用日志，不在用户观察期间发送桌面输入。

用户明确反馈“快了很多”，随后确认“都无黑屏，动画正常继续”，确认范围包括
短遮挡、长遮挡和本轮锁屏恢复。结合 [本轮完整会话日志](validation/macos-2026-10-25/responsive-gui.jsonl)，
本机该素材的完整界面响应与无黑屏复测通过。本轮没有新增源码修复。

| 项目 | 实测证据与结果 |
|---|---|
| 短遮挡暂停/恢复 | 记录 9 次 Covered→L1，7 次 L1→L0；另一次长遮挡转入 L3，最后一次 L1 在测试结束时退出。已确认的 Covered 信号到暂停完成为 150.6–151.5 ms；解除 Covered 到 L1 恢复调用完成为 300.6–302.1 ms。用户确认体感“快了很多”，无黑屏、动画继续 |
| 长遮挡 L3 | 15:27:21.499 进入 L1；15:28:21.500 开始 L3，21.628 完成，静态帧位置 65.476597292 秒。15:28:23.400 恢复前、23.404 恢复调用完成后位置一致；下一次暂停已推进到 69.681411750 秒。长遮挡恢复画面由用户确认，无黑屏 |
| 本轮短锁屏 | Locked=true→L3 开始分别约 0.34/0.13 ms，含静态帧提取的 L3 完成约 28.2/67.6 ms。两次保存位置 84.123158877、96.982663750 秒，解锁恢复前后分别保持相同值。用户确认恢复后动画正常、无黑屏 |
| 错误与退出 | 会话无非空转换错误、无 ERROR；15:31:03 正常关闭 renderer 并退出 |

上述毫秒数从**去抖后的应用信号**计时，不含前面的 250 ms × 3 次采样确认、
窗口动画或新视频首帧；`SetLevel` 返回不等于动画已呈现。
因此“明显变快”是用户本机对比体验通过，不标记为端到端精确时延、零延迟或 ≤500 ms 首帧验收。
锁屏到解锁信号间隔分别约 0.99/5.58 秒，证实这两次短锁屏保存位置未推进，
仍不替代前文尚未闭环的连续 20–25 秒纯锁屏验收；本轮未重测睡眠唤醒、整机功耗或其他素材。

测试后已退出完整应用，将系统低电量模式恢复“仅使用电池时”，并核对隔离库
config/library/assignments 与本轮启动前备份逐字节一致；未安装、提交或发布。

## 环境与来源

- 工作目录：基于已核对主分支的云端 checkout，不是用户本机。`ui` / `5f00d334` 仅为设计参考；产品通过状态以主分支整体检查为准，历史资料仍单独标明。
- 产品只使用原生 MyGo。React 仅保留原有 UI 设计参考，本轮交付不含新增 React 运行逻辑、浏览器持久化或测试框架。
- Windows/macOS 真机、代码签名证书、商店审核、真实更新发布服务与功耗测量不在当前云端已验证范围。
- 命令如因依赖、网络、系统库、显示服务器或工具链失败，应写“受阻”，不能写“通过”或“未完成原因是产品 bug”。反过来，若是断言失败，也不能假称环境问题。

## 七页布局检查点台账（2026-10-21 16:09 UTC 及以前）

下表是新增原生 MVP 补全之前的已跑检查点，不是当前全部工作区的最终状态。对应日志为交付检查目录中的 master-post-layout-checks.log 等；后续代码需要复跑。

| 检查 | 命令/范围 | 结果 | 证据与限制 |
|---|---|---|---|
| 文档链接与编号 | `python3 scripts/check-docs.py`；`--self-test` | 通过【主分支验证】 | 该检查点 37 份 Markdown，本地路径/锚点与 F-01…F-26、UI-01…UI-07；最后链接计数见最终检查输出；不联网检查外链 |
| 历史快照保全 | 将历史 README 与主分支基线原文比较 | 通过【主分支验证】 | 仅新增历史说明和修复移动后的相对链接，原文保留 |
| 统一原生检查脚本 | `CROSS_BUILD=1 bash scripts/check.sh`，另执行 `go test ./...`、文档 self-test 与 diff 检查 | 该布局检查点完整通过【主分支验证】 | 2026-10-21 16:08:17 UTC 开始，16:09:45 前完成；root tests/race/vet/无 cgo/文档/五目标构建全通过；不含 React。最初链接失败及修复仍保留在下文 |
| 产品 Go 单元测试 | `go test ./...` | 通过【主分支验证】 | 该布局检查点的根模块全部包通过；不包含嵌套研究模块 |
| Go race | `go test -race ./...` | 通过【主分支验证】 | 该布局检查点的根模块全部包通过（UI race 26.684 s）；使用可用 cgo/race 工具链 |
| 产品静态检查 | `go vet -unsafeptr=false ./...` | 通过【主分支验证】 | FFI 路径关闭 unsafeptr 检查，不等于相关风险消失 |
| 原生命令无 cgo 构建 | `CGO_ENABLED=0 go build ./cmd/...` | 通过【主分支验证】 | wallpaperd/wpupdate/uishots；不代表运行或签名 |
| 跨平台构建 | `scripts/build.sh`（通过 `CROSS_BUILD=1`） | 五目标通过【主分支验证】 | Windows amd64/arm64、Darwin amd64/arm64、Linux amd64；编译不是系统运行或签名 |
| 原生 UI 与命令测试 | 主分支 `go test ./...` / `go test -race ./...` 中的 UI/cmd 包 | 通过【主分支验证】 | 33 个 UI 测试函数与 1 个 cmd 测试，覆盖七页、双语、规则/显示器/异步反馈/错误路径；仍使用假控制器 |
| 独立代码复核 | 原生实现、导入/编码队列、删除/替换、持久化与范围检查 | 未发现尚待处理的 P1/P2 问题 | 复核结果不是无缺陷证明，也不是安全审计或平台认证 |
| 原生 UI 软件截图 | 主分支工作区 `go run ./cmd/uishots`，`-scale 1`；英文加 `-lang en -width 900 -height 600` | 通过所检查视图【主分支验证】 | 该检查点截图生成记录 16:06 UTC：中文 1120×740、英文 900×600 各 19 PNG；[代表图与来源](screenshots/native-current/README.md)。不代表真机窗口/DPI/所有尺寸 |
| mpv 实际运行测试 | `go test -v ./internal/render/mpv -run TestReal` | 跳过【主分支验证】 | `TestRealMpvLifecycle` / `TestRealMpvFPSCap` 明确 SKIP：`mpv not installed`。包级 PASS 不表示这些实际播放测试通过 |
| Linux 运行 e2e | `scripts/e2e-linux.sh` | 未执行，运行依赖不足 | 当前无 mpv / xvfb-run；需 Xvfb、mpv、ffmpeg，即使通过也不能替代真实桌面会话 |
| 更新/回滚 e2e | `scripts/e2e-update.sh` | 7/7 通过【主分支验证】 | 该检查点源码于 16:08:34 UTC 开始复跑；测试版本 0.1.0→0.2.0，只下载变化文件、不重复下载未变化的 2 MB 文件；已最新、回滚、拒绝篡改清单且旧版完整。仅本地测试源，不证明线上更新源/发行签名/公证 |
| Windows/macOS 真机验收 | W-T*、M* | 本轮未执行 | 明确保留发布门槛 |
| 性能 AC / 能耗 | PB-idle/video/paused/battery | 本轮未测量 | 不引用旧 box 数据宣称达标 |
| F-13 搜索正确性/微基准 | `TestLibrarySearchMetadataAndColors`、`BenchmarkLibrarySearch1000`（`internal/ui`） | 通过【主分支验证】 | AMD EPYC 9V74 云端，1000 个匹配项：1,183,088 ns/op（约 1.18 ms）、1,172,315 B/op、4015 allocs/op；不含真实 UI 绘制与 I/O |
| 历史研究状态机模块 | [历史索引](design/visibility/README.md)；旧命令 `cd docs/design/visibility && go test ./...` 已失效 | 历史检查点曾通过；当前 checkout 不可重跑 | Go 副本及模块文件已删除；旧结果须从 Git 历史恢复环境，不能转作生产覆盖 |
| 研究 D-Bus 模块 | `cd docs/design/linux-dbus && go test ./...` | 编译通过，无测试文件 | 不代表实际 D-Bus/GNOME 会话联调 |
| 后期研究原型模块 | `cd docs/design/prototype && go test ./...` | 通过【主分支验证】 | 只覆盖该模块当前环境的测试，非 Windows/macOS 真机运行 |
| 早期 MyGo 原型模块 | `cd docs/research/wpproto_src && go test ./...` | setup 失败【主分支验证】 | 该历史模块 go.mod 未声明 main.go 导入的 `github.com/egoist/mygo`；保留历史骨架，不计为生产模块回归，也不宣称可直接复现 |
| 历史外部文献时效 | 旧市场、法律、系统版本及第三方测量来源 | 本轮未全面重新核验 | 历史价格/许可/市场结论不能称最新；本轮新增 AVFoundation/MF/purego 技术选型另由实现与代码复核对照主要 API 文档/源码，不等于新增真机证据 |

主分支首次统一脚本在 Go race/vet/无 cgo 构建之后因残留的本地 React 文档路径而停止；已移除该错误依赖，16:07 UTC 的完整脚本及 16:08–16:09 UTC 最终布局改动后的再次完整复跑均通过。此失败不计作原生运行失败，也不隐藏成首次全部通过。

更新台账时记录最后运行对应的代码状态；有后续代码修改就重跑受影响测试。单包通过不能代替整套通过，重试成功需保留先前失败的原因和修复。

## 原生 MVP 补全后的验证

此阶段新增空间跨屏、逻辑拓扑与 backing 像素分离、UTC 周暂停计数/state schema 2、目标分辨率偏好、macOS 原生 helper、严格转码输出校验及 MF 播放控制。以下按实际日志分别标注普通/race、聚焦检查、交叉构建及未执行的原生运行。统一构建之后的最后 MF 增量及随后重新通过的整树检查分别列明，不能说较早二进制已包含该修正。

| 检查 | 可追溯入口/范围 | 当前结果与边界 |
|---|---|---|
| 最后修正后的整树最终复验 | `CROSS_BUILD=1 bash scripts/check.sh` | 16:48:32 UTC 启动、16:49 完成，exit 0；native-mvp-final-verified.log。最后 MF 修正已包含：全部 27 个根包（23 个有测试、4 个无测试）的 race、vet、无 cgo 命令、文档和五目标构建通过。编译仍不等于真实平台运行 |
| 根模块最终整体检查 | `go test -count=1 ./...`；`go test -race -count=1 ./...`；vet/无 cgo/文档 | 16:44:48 UTC 启动的无缓存普通/race、vet、无 cgo、文档 self-test/链接与 diff 检查通过；native-mvp-final-tests.log。最后 MF 增量另记，不复用旧 16:09 检查点 |
| 稳定源码统一检查/五目标构建 | `CROSS_BUILD=1 bash scripts/check.sh` | 16:46:06 UTC 启动，通过原生完整脚本及 Win amd64/arm64、Darwin amd64/arm64、Linux amd64 构建；native-mvp-final-checks.log。此批二进制早于 16:47:38 的最后 Windows MF 生命周期修正，不声称包含它 |
| 聚合后 MF 增量 | 16:47:38 UTC 停止/恢复 fatal 分类、实际宿主退出等待及回归；下列 MF 聚焦命令 | 修正后 MF portable race×10/覆盖、Windows amd64/arm64 测试编译/vet/完整应用构建重新通过。构建不是原生运行；该补充结果不改写上一批二进制来源 |
| 空间跨屏 | `internal/desktop/canvas_test.go`、`internal/app/spanning_test.go`、`internal/render/panorama/crop_test.go`、`internal/ui/spanning_test.go` | 最终根模块普通/race 通过，含 Retina 输出/缓存、backing-only 拓扑变化、跨屏音频/共享续播、几何/控制器/图片/模拟 UI 回归；Win/mac 原生宿主仅交叉编译，真实屏幕接缝未验 |
| UTC 周指标 | `internal/app/metrics_test.go`、`internal/config/metrics_test.go`、`internal/ui/metrics_test.go` | 最终根模块普通/race 包含这些测试并通过；覆盖确认状态、跨周/离线/时钟/隐私/迁移，不测能源 |
| 原生转码与严格输出 | `internal/transcode`、`internal/transcode/nativeav`、`cmd/wallpaperd/encoder_test.go` | 最终根模块普通/race 通过，含 Linux 真 ffmpeg、GIF 合成及参数/失败测试；与 Darwin 原生运行分开记录，mac helper 实际运行未验 |
| Windows MF 播放控制 | `internal/render/mf`、每屏属性与续播回归 | 16:47 UTC 最后生命周期回归后聚焦检查复跑通过：portable race ×10、98.7% portable 语句覆盖；Win amd64/arm64 测试编译、目标 vet 与应用构建通过。Windows IPC/浮点 ABI 测试仅编译，未执行；最后增量按上行补测，COM/MF 实际运行未验 |
| 更新/回滚 e2e | `scripts/e2e-update.sh` | 16:42:56 UTC 开始，7/7 通过；native-mvp-update-e2e.log。覆盖更新/仅下载变化文件/已最新/回滚/篡改拒绝，仍仅本地测试源，不是线上或签名发行 |
| 新技术选型来源复核 | AVFoundation/VideoToolbox、MF/DXGI、purego API/ABI | 实现与复核已对照主要文档/源码；[转码主要来源](../internal/transcode/README.md#primary-references)。这是技术依据核对，不是原生 API 已实际运行或旧市场/法律资料已全部更新 |
| 最终独立代码复核 | 导入源编辑/替换/删除保护、跨屏音频/共享续播、Retina、MF 属性回滚/不确定 IPC/致命生命周期恢复 | 未发现尚未处理的已确认 P1/P2；保留修正与回归。不是无缺陷保证、安全审计或真机认证 |
| 本阶段原生 UI / 截图 | `go test -race ./internal/ui/... -count=1`；`go vet ./internal/ui/...`；`cmd/uishots` 和状态测试 | 最后 UI 改动后 47 个测试函数、独立 race 50.021 s 与 vet 通过；16:45 UTC 重生中文逻辑 1120×900/英文 900×680，均 scale 1.5，各 19 PNG，另有 16 状态图。已检查七页双语/浅深与滚动控件，文档收录 10+4；[来源/边界](screenshots/native-current/README.md) |
| 文档与本地链接 | `python3 scripts/check-docs.py`；`--self-test` | 16:49 UTC 文档冻结检查通过：39 份 Markdown、362 个本地链接与矩阵编号；self-test 与 diff 检查通过，不检查外链时效 |

本轮新增原生代码可交叉编译与云端模拟验证，不等于已执行 Win/mac 本地解码、桌面挂载或功耗测试。[跨屏验收](SPANNING.md) 与 [研究接续](research/NATIVE_MVP_ADDENDUM.md) 区分“已有源码但缺真机证据”和“仍缺产品实现”。

### MF 聚焦验证命令（2026-10-21 16:47 UTC）

以下命令已通过；输出为本轮工具执行记录，未生成独立 MF 日志文件。覆盖率只统计当前 Linux 可编译的 portable 逻辑，不包括 Windows FFI/驱动路径。

```sh
go test -race -count=10 ./internal/render/mf
go test -race -cover ./internal/render/mf
for arch in amd64 arm64; do
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go test -c -o /tmp/mf-controls-$arch.test.exe ./internal/render/mf
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go vet -unsafeptr=false ./internal/render/mf
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -o /tmp/wallpaper-mf-$arch.exe ./cmd/wallpaperd
done
```

原生 [客户端 IPC 测试](../internal/render/mf/client_windows_test.go) 和 [浮点 ABI 测试](../internal/render/mf/float_windows_test.go) 已加入 Windows 测试二进制，但尚未在 Windows 运行。交叉编译结果不得写成 seek/循环/resize/硬解/功耗通过。

## 本轮复核中修复的问题

在功能接线之后，补充了跨屏启动重复音频、共享续播退出时被不同屏随机覆盖、macOS Retina 逻辑坐标误作输出像素、有限编码器被文案错误承诺严格转换，以及 MF 超时/回滚失败后旧宿主继续运行等修正。相应回归保存在产品测试中；修正后按受影响范围复跑，最终 Windows-only 增量既有上表聚焦复跑，也已进入 16:48:32 开始的整树最终复验。代码复核发现并修复这些问题，不是“从未出现失败”或“没有其余缺陷”的证明。

## 本轮原生 UI 覆盖

代码复核期间修正了多选导入丢项、编码无界并发、删除/同 ID 替换与过期编码结果竞态、保存器并发退出、静态预览原子替换和删除异步反馈，并以回归测试覆盖。

七页原生导航与控件已完成聚焦交互检查，新增 zh-CN/en 控件翻译。导入素材标题/说明及后端原始状态/错误保留来源语言，不承诺把用户内容或所有系统错误自动翻译。新增界面接入 Win/mac 空间跨屏、实际 UTC 周暂停时长与编码路线/分辨率能力；不能继续沿用检查点时“跨屏和本周统计未提供”的说明。网页/Shader、在线工坊和远程版本历史仍未提供。各后端实际属性支持见矩阵，部分全局/不支持属性仍可能重载；不宣传已经测得 F-18 的 ≤200 ms 真机时延 AC。

## 设计稿范围

按当前范围，React 只作 UI 设计参考，保留在 UI 分支；主分支应用工作区没有 `web/`，不复制 React 源码。本轮的应用功能、持久化、控制器接线和验收只针对原生 MyGo；不将浏览器交互/构建/测试结果计入产品完成度，也不建立 React-Go 桥接。

## 可重复命令与前置条件

首选仓库根目录的 [`scripts/check.sh`](../scripts/check.sh)：

```sh
bash scripts/check.sh
CROSS_BUILD=1 bash scripts/check.sh
```

脚本包含 Go 格式检查、race、vet、CGO_ENABLED=0 命令构建和文档链接；需要 Go、可用 C/race 工具链、Python3 和 Go 依赖访问。可选交叉构建与嵌套研究模块仍各自记录，不能省略后宣称全部通过。React 设计稿不进入该原生应用检查流程。

### 原生产品

`go.mod` 声明 Go 1.27.3。若机器没有该工具链且无法下载，不要静默改模块版本来伪造“项目按原配置通过”；可做兼容性诊断，但结果必须注明临时配置与局限。

```sh
# 在仓库根目录。单元测试与 race 检查不是同一种保证。
go test ./...
go test -race ./...
go vet -unsafeptr=false ./...
CGO_ENABLED=0 go build ./cmd/wallpaperd ./cmd/wpupdate ./cmd/uishots
scripts/build.sh
```

界面截图依赖可用的 MyGo/系统图形库。`cmd/uishots` 使用假控制器；需单独人工检查浅色/深色、文字裁切、七页布局与错误提示，不能只确认 PNG 文件存在。若重写 `docs/screenshots/`，须先确认截图是本轮生成并检查过。

```sh
# 可选运行集成。没有依赖时记受阻，不记通过。
scripts/e2e-linux.sh
scripts/e2e-update.sh
```

### 嵌套研究模块

根目录测试不递归进入独立 `go.mod`。如改动实验模块，应分别在这些目录执行 `go test ./...` 并独立记结果：

- [`design/prototype`](design/prototype/README.md)
- [`design/linux-dbus`](design/linux-dbus/README.md)
- [`research/wpproto_src`](research/wpproto_src/)

历史状态机副本 [`design/visibility`](design/visibility/README.md) 已删除 Go 与模块文件，不再是可运行模块，旧结果不能在当前 checkout 重跑。生产状态机改用根目录 `go test ./internal/visibility`，其结果不继承历史测试数或覆盖率。

本轮已分别执行的模块结果在台账中列明。早期 `wpproto_src` 依赖声明不完整，不能直接测试；若未来要恢复可运行性，应另行修复依赖并明确区别于原始历史骨架。研究模块即使本轮通过也不计为产品平台覆盖。

### 原生 macOS 编码器集成（必须在 Mac 上显式执行）

源码提供 opt-in 测试，不在 Linux 自动执行：

```sh
WP_NATIVEAV_INTEGRATION=1 go test -v ./internal/transcode/nativeav -run TestNativeAVIntegration
```

它覆盖 GIF→H.264、原生视频重采样、音轨去除、HEVC 24fps 输入和拒绝覆盖既有文件。未设置变量时该测试明确跳过；交叉编译成功不算执行。完整旋转/像素比例、复杂 GIF、性能、资源压力、UI 取消与系统桌面仍按 [目标设备计划](design/bench/TEST_PLAN.md) 补测。

## UI 人工验收单

对 [UI-01…UI-07](IMPLEMENTATION_STATUS.md) 每页：

1. 导航到页面，检查浅/深主题、长文字、空数据、错误状态和可访问标签。
2. 修改设置，确认真实控制器/配置变化；关闭重开保持；失败时不展示成功值。
3. 对删除、重置、导入和更新测试取消、确认、重复点击与运行中离开页面。
4. 真机上验证托盘驻留、再次启动唤起、窗口关闭重建、文件/目录对话框和开机启动。
5. 对损坏配置、只读目录、导入取消和错误恢复进行原生测试；参考 React 设计时不能把浏览器功能移作产品实现。

## 必须分开关闭的发布门槛

| 门槛 | 相关需求/决策 | 需要的证据 |
|---|---|---|
| 平台行为 | F-01/02/03/04/06/09/10；Q-W1/W11/W12、Q-M1/M6 | [真机计划](design/bench/TEST_PLAN.md) 和 [设备清单](design/bench/DEVICE_HANDOFF.md) 的版本、日志、录屏及逐项结果 |
| 转码完整 AC | F-07/08 | mac 原生与 Win h264_mf 实际编码/播放、30fps/IDR/无音轨/目标边界、复杂 GIF/旋转/取消；按屏多 rendition/cover 缓存另需实现 |
| 本地指标与后端控制 | F-17/18 | 真实暂停确认、睡眠/跨周/时钟变化和位置恢复；MF/AVF 硬解仍须实证，不用暂停时长代替能源数据 |
| 性能与电池 | F-04/05/06；D-04/D-05/D-07 | 同机静态基线、CPU 核数单位、GPU 解码/3D、内存、功耗、重复次数；预算是目标不是测量 |
| 发行信任 | F-15；D-02 | 真实发布主体和证书、Windows 签名/时间戳验证、mac codesign/notarization/staple/Gatekeeper 实测；`signedBuild` 标记不够 |
| 更新可用性 | F-15 | 有效的受信公钥、可达且正确签名的源、安装/中断/失败回滚；mac 更新后签名完整性 |
| 第三方发行义务 | ADR-004；D-03/D-13、L-1…L-4 | 固定组件构建、许可证/源码交付与需要的法律评估；文档整理不是法律批准 |
| 未实现服务/引擎 | F-12/14/21/22/23/25/26 | 独立实现与安全/平台/服务验收；本地 demo 或占位 UI 不可关闭 |

当前未获得这些证据时，只能称“源码已实现/某检查通过”，不能称“全平台完成、全部功能完成、已签名发布或可用线上工坊”。
