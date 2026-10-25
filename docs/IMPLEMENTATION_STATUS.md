# 当前实现与验收矩阵

本文件连接“原来的 F-* 需求 → 七页 UI 计划 → 产品源码 → 自动化/真机验收”。它不是发布合格证明。正式实现基于主分支 `master/2710033ef169852bd7c4a1b5c063d29f3f3e89fd`；UI 分支 `5f00d334` 仅为设计参考，不复制 React 代码。本矩阵描述主分支工作区中的原生实现；历史测试不自动继承，本轮实际结果以 [VALIDATION.md](VALIDATION.md) 为准。标签规则见 [EVIDENCE.md](EVIDENCE.md)。

> 2026-10-25 macOS 真机增量：视频循环、AVF L3 释放与七次恢复、精确续播修复、原生编码和无证书打包结果，见 [验证记录](VALIDATION.md#macos-真机补验2026-10-25singapore-utc8)。下文历史检查点不替代该记录。

> test.4 完整界面复测：用户确认遮挡暂停/恢复“快了很多”，短遮挡、长遮挡及短锁屏恢复均无黑屏、动画继续；日志与计时边界见 [本轮记录](VALIDATION.md#test4-完整界面响应复测2026-10-25-15261531utc8)。

## 产品界面只使用原生 MyGo

- 唯一正式应用路径：[`cmd/wallpaperd`](../cmd/wallpaperd/) → [`internal/ui`](../internal/ui/) → [`internal/app`](../internal/app/) → 平台宿主/渲染器。导入、设置持久化和桌面控制都在原生产品中实现。
- [UI 分支的 React 设计稿](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web) 仅参考七页布局、视觉和信息架构；主分支工作区没有 `web/`，不复制设计稿代码、不嵌入 MyGo、不接 Go 后端。
- 原生 UI 必须自行完成所有行为验收，不能以 React 原型能点击、能展示页面作为原生功能完成的证据。网页/Shader 壁纸引擎是另一个功能范围，与 React 设置设计稿无关。

## 七页实施计划与验收（UI-*）

原生导航已按七页组织，旧的性能/暂停/电源/外观/启动/诊断入口映射到新页面。以下是对原 Web 规划七页的落地映射。每行包含行为验收，不能只靠截图关闭。具体本轮通过/阻塞见 [验证记录](VALIDATION.md)。

| ID | 页面与原规划映射 | 必须可验收的行为 | F-* / 设计依据 | 主要源码 |
|---|---|---|---|---|
| UI-01 | 壁纸库（紧凑原生布局） | 12px 横向边距、宽窗合并标题/工具栏、按左列可用宽 960px 切换「筛选与显示」（筛选/排序/视图），不足 680px 时导航独立一行，搜索至少 140px；默认深色（缺失/非法值同样回退深色）；320px 全高属性栏（预览固定、属性独立滚动、38px 固定应用区），左侧 38px 底栏只占内容列；Windows 按 TitleBar inset 留拖动条、macOS/默认标题栏预览贴顶；空库保留属性占位；导入反馈进入底栏；1920×1080 网格至少六列四行的 Tester 回归； 搜索/筛选、网格/列表、详情、导入、应用到选定屏/所有屏、优化、删除确认；空库/取消/导入失败有明确状态 | F-06/07/08/11/13/18；[包格式](design/10-package-format.md) | [`library.go`](../internal/ui/library.go)、[`app.go`](../internal/app/app.go)、[`importer`](../internal/importer/) |
| UI-02 | 显示器 | 布局选择、稳定 ID 别名、独立壁纸/跟随默认、每屏状态与暂停；Win/mac 原生跨屏空间裁切、切回保留独立分配；不支持宿主明确禁用 | F-03/04/17；[多屏](design/09-multimonitor-dpi-hdr.md) | [`displays.go`](../internal/ui/displays.go)、[`display_test.go`](../internal/app/display_test.go) |
| UI-03 | 播放与性能 | 档位/FPS、可用后端与路径、支持的缩放/声音/速度；配置写入后实际应用或报错，不支持后端不显示成功 | F-05/06/07/18；[视频管线](design/06-video-pipeline.md) | [`pages.go`](../internal/ui/pages.go)、[`render`](../internal/render/)、[`config`](../internal/config/) |
| UI-04 | 暂停与电源 | 全屏/遮挡策略、电池 pause/fps15/ignore、低电量阈值、当前原因与恢复；新增应用规则只匹配进程，不采集窗口标题 | F-04/05/19；[状态机](design/07-pause-power-state-machine.md) | [`pages.go`](../internal/ui/pages.go)、[`visibility`](../internal/visibility/)、[`desktop`](../internal/desktop/) |
| UI-05 | 通用 | 主题/强调色/库视图/导入后动作、启动行为、zh-CN/en 控件翻译；重开后保存；导入内容与原始后端信息不强制翻译；取消/无权限显示错误 | F-09/10/13；[配置](design/16-config-logging-telemetry.md) | [`settings.go`](../internal/ui/settings.go)、[`theme.go`](../internal/ui/theme.go)、[`shell.go`](../internal/ui/shell.go) |
| UI-06 | 版本管理 | 版本、更新源/自动检查、检查/应用/回滚；未配置公钥或源时解释原因；构建标志不能冒充证书验证；失败/重复点击要有状态 | F-15；[分发](design/11-distribution-licensing.md) | [`pages.go`](../internal/ui/pages.go)、[`update`](../internal/update/)、[`cmd/wpupdate`](../cmd/wpupdate/) |
| UI-07 | 关于与诊断 | 版本/许可/隐私、导出诊断、打开日志、重置确认；不含路径/窗口标题的脱敏测试；展示未提供能力的边界 | F-16/20；[日志](design/16-config-logging-telemetry.md) | [`pages.go`](../internal/ui/pages.go)、[`telemetry`](../internal/telemetry/) |

共同验收：浅色/深色文字和层级可读；七页切换与关闭重开无错误；确认框取消不执行；快速重复点击不重复破坏数据；失败有可见提示。React 设计稿不承担产品功能验收；文件选择取消、配置损坏恢复等由原生应用测试覆盖。

## F-01…F-26 实现与差距

下表“源码存在”仅是【当前源码】层面的实现状态。完整 AC 与性能门槛仍在 [第 17 章](design/17-features-mvp.md)；列出的测试文件是可追溯的检查点，是否本轮执行必须看验证记录。源码变化后须复核本表。

| ID | 当前实现/边界 | 证据入口 | 尚需验收或决定 |
|---|---|---|---|
| F-01 | Windows/macOS/Linux 宿主源码存在；Linux 是有限兼容实现，不是全桌面认证 | [`desktop`](../internal/desktop/)、[`app`](../internal/app/) | W-T1/W-T6、M1；图标层级、Explorer 恢复、睡眠/Space 真机 |
| F-02 | 监管、退避、失败截图兜底和 Windows 子进程约束源码存在 | [`supervisor`](../internal/supervisor/)、[`app_test.go`](../internal/app/app_test.go) | W-T9、真实 TDR、主进程终止后无残留；模拟器不证明驱动恢复 |
| F-03 | 独立分配、默认壁纸、稳定 ID/别名/迁移、每屏控制已有；新增 Win/mac 每屏裁切原生宿主的空间跨屏，不是复制模式；保留独立分配和按屏暂停 | [跨屏实现与限制](SPANNING.md)、[`spanning.go`](../internal/app/spanning.go)、[`canvas.go`](../internal/desktop/canvas.go) | 真双/三屏、负坐标/混合 DPI、拔插与同数量拓扑变化、静态兜底接缝；不是单解码器或逐帧同步架构；UI-02 |
| F-04 | 每屏 ops worker 的等级状态已移入独立指针，主循环复制 screen 不再读取 worker 内存；新增视图/优化请求与 worker 并发回归；Windows mpv/MF 命名管道改为重叠 I/O，Accept 可取消超时，mpv 优先写 deadline；新增 Windows 双向并发读写、关闭和静默管道恢复回归（待 windows-latest 执行）；恢复失败最多重建两次并提示；UI 使用已确认等级，恢复中及其他暂停原因可见；每屏转换日志含后端、耗时、错误；分级状态机及平台信号已有；本轮补每屏手动暂停、全局/每屏原因保留、开关重放原始信号；Windows 信号较完整，macOS/Linux 有能力差异 | [`visibility`](../internal/visibility/)、[`app/options_test.go`](../internal/app/options_test.go)、[`desktop`](../internal/desktop/) | AVF/MF L3 释放与恢复已有 #14 实测（见下）；W-T10/M2/M4、≤500 ms、暂停 CPU/GPU 与真实按屏/系统事件仍待完整验收 |
| F-05 | 生产 mpv/MF/AVF 已有 FPS cap，电池/低电量/省电策略已有；不是“限帧未实现” | [`mpv.go`](../internal/render/mpv/mpv.go)、[`mf`](../internal/render/mf/)、[`avf`](../internal/render/avf/)、`TestFPS15BatteryAndSaverProfile` | 真机验证 MF/AVF 限帧、电源事件与功耗；MF 节流呈现不等于少解码；D-07 |
| F-06 | mpv、MF、AVFoundation 视频路径已有；硬解/循环质量不能仅由参数证明 | [`render`](../internal/render/) | W-T3/M3、`hwdec-current`、丢帧与逐帧循环；D-04/Q-W10 |
| F-07 | 手动/自动转码、目标分辨率偏好、隔离任务与严格产物校验已接入；mac 默认原生 AVFoundation helper，无需 ffmpeg；可选 ffmpeg 在 Win/mac 分别要求 h264_mf/h264_videotoolbox，不静默换软件路线 | [转换契约](../internal/transcode/README.md)、[`nativeav`](../internal/transcode/nativeav/)、[`encoder.go`](../cmd/wallpaperd/encoder.go)、[`verify.go`](../internal/transcode/verify.go) | 原生编解码/帧率/IDR/音轨/取消真机验证；已接入按屏 backing 像素 center-cover 缓存、manifest、懒编码与单活跃队列、取消/选择/清理；mpv 保进程换源；MF/AVF 换源重建，跨屏/有声音/非 fill 回退共享版本；avconvert 有限回退不满足严格 AC |
| F-08 | 静态系统壁纸/跨屏静态裁切已有；mac 原生 GIF 预检、透明/子矩形/帧延迟/disposal 合成后编码视频，Win/Linux 可用外部转码；mac GIF 导入接入原生路线 | [`nativeav/gif.go`](../internal/transcode/nativeav/gif.go)、[`importer`](../internal/importer/)、[`panorama`](../internal/render/panorama/) | GIF 合成纯 Go 测试与 Linux 真 ffmpeg 不能替代 mac 原生编码/播放；透明复杂 GIF、末帧、资源限额及静态 CPU 需真机；有限 avconvert 不支持 GIF |
| F-09 | 原生托盘、菜单、窗口按需打开/关闭；Windows 左键打开库；安全模式提示/恢复、暂停状态 ToolTip、全局与按屏暂停（含 span）；同快照刷新；设置不使用 WebView | [`shell.go`](../internal/ui/shell.go) | 各系统托盘/退出/窗口重建；后台内存与菜单时延 |
| F-10 | MyGo 自启桥接、默认关闭、首次询问已有 | [`cmd/wallpaperd`](../cmd/wallpaperd/)、[`config`](../internal/config/) | 真机登录后启动、禁用后撤销、失败回滚；不是网页开关 |
| F-11 | Lively/WE video 与纯本地 web 导入；Web 属性转换；exe/scene/audio-reactive/远程入口拒绝 | [`importer`](../internal/importer/)、[`wpk`](../internal/wpk/) | 样例包、非法路径/不支持类型、安全解压；不扩展到 WE scene/app |
| F-12 | 独立 WebView2/WKWebView 子进程、来源隔离与请求/导航/权限策略、默认拒绝的导入同意对话框与撤销、五类属性持久化/实时注入、rAF 限帧及暂停/电池策略已接线；Linux 明确不支持播放 | [`render/web`](../internal/render/web/)、[`ui/web.go`](../internal/ui/web.go)、[`web-orbit`](../examples/web-orbit/) | 原生沙箱行为、Windows 截图覆盖/恢复、macOS 桌面层级/多屏与隐藏节流、B-T2/Q-B2 仍须真机验证；不能据单元测试标记安全/性能验收完成 |
| F-13 | 本轮补作者/说明/标签/尺寸/时长/大小与预览主色索引；搜索覆盖名称/来源/作者/说明/标签/中英文颜色；网格/列表/缩略图已有 | [`library.go`](../internal/ui/library.go)、[`palette.go`](../internal/importer/palette.go)、[`search_test.go`](../internal/ui/search_test.go) | 16:09 以前的布局检查点，1000 条查询云端微基准约 1.18 ms，通过该检查点基准目标；不是端到端 UI/旧库完整索引或真机时延认证；UI-01 |
| F-14 | 独立原生 Shader 子进程、GLSL → HLSL/MSL 转译、D3D11/Metal、离屏缩放、GPU 计时与自适应控制、导入和原生属性 UI 已接线；不使用 WebView；Linux 明确 unsupported | [`render/shader`](../internal/render/shader/)、[`shader-waves`](../examples/shader-waves/) | 真机画面、原生 GPU 帧预算与长期暂停恢复尚未验证；当前沙箱无 Metal device，GPU 测试跳过，不能以占位预览验收 |
| F-15 | 严格 SemVer（含数字 prerelease）排序、dev 不参与更新、下载/替换前拒绝非 schema 1；pending 状态机先持久化完整计划；逐文件原子 rename、主程序最后替换；启动 Recover→Boot→Cleanup，未完成事务幂等回滚，损坏记录保留并报错；故障注入覆盖替换/恢复每步；打包脚本随包保留自有代码 PolyForm Strict 1.0.0 LICENSE 与第三方声明；[标签 CI/CD](releases/README.md) 已接入三系统验证、五目标打包、可选签名与幂等发布；真实签名/更新服务仍待验证 | [`update`](../internal/update/)、[`packaging`](../packaging/)、[`scripts`](../scripts/) | D-02、更新源/公钥、实际签名及公证、安装卸载、mac 更新后签名完整性、三系统真实断电/运行中 exe 验证；主程序两次 rename 之间断电可能无法启动，须重新运行安装器 |
| F-16 | 存储扫描逐项检查 App 取消且关闭后不回写；IPC 回复写入有 5 秒期限，序列化失败返回 E_INTERNAL 并记录日志；已接入 dispatch 前 Go crash 文件（启动清理旧空文件、保留最新 5 份非空报告并保护当前文件；-version 不安装）、child stderr/MF 文本 2 MiB 写入限额与启动 `.1` 轮换、helper PID/build、worker 转场 begin/end 和生命周期 IPC 分阶段日志、mpv warn 转发；mpv 原生 log-file 保留且仅启动轮换；本地日志、诊断包脱敏实现和测试已有；I/O 错误保留首错但允许后续写入，重开失败仅随刷写 tick 重试；正常退出无日志状态噪声；不上传遥测 | [`telemetry`](../internal/telemetry/) | 本轮脱敏测试、失败提示、各平台导出路径；日志性能 AC |
| F-17 | 每屏 L0–L4、暂停原因、当前 UTC 周已确认动态运行时长/暂停时长已持久化；显示器指标键为 SHA-256 截断摘要；不估算能源 | [`app/metrics.go`](../internal/app/metrics.go)、[`config/metrics.go`](../internal/config/metrics.go)、[`ui/metrics_test.go`](../internal/ui/metrics_test.go) | mpv 展示报告的硬解状态；MF/AVF 仍标 unknown/未验证，不能从框架名推断硬解。真实睡眠/时钟/重启与 UI 一致性需平台验收，完整 F-17 未关闭 |
| F-18 | 每屏 fit/speed、静音/降采样按能力接线；mpv/MF/AVF 支持 fit/speed 原位修改并保持暂停；MF 增加原生播放速率、位置采样/异步 seek 及 L3 恢复 | [`render.go`](../internal/render/render.go)、[`options.go`](../internal/app/options.go)、[`mf/playback.go`](../internal/render/mf/playback.go)、[`mf/engine_windows.go`](../internal/render/mf/engine_windows.go) | MF/AVF L3 恢复已有 #14 实测；其他原生路径与 ≤200 ms 时延仍未验；MF 不支持帧降采样，全局/不支持属性仍可重建；Web 属性已接入独立宿主，完整 AC 的原生时延仍未关闭 |
| F-19 | 本轮接入可执行文件 basename 规则 pause/saver/ignore，Windows/macOS/X11 前台进程探针；ignore 只豁免全屏/遮挡，不能覆盖安全或手动暂停 | [`foreground.go`](../internal/desktop/foreground.go)、[`options.go`](../internal/app/options.go)、[`options_test.go`](../internal/app/options_test.go) | 前台而非“任意后台运行”语义；实际桌面会话探针/权限/误判验证；不可用时能力提示 |
| F-20 | 未启用远程遥测；`Normalize` 强制 false，与早期 opt-in 设想不同 | [`config/files.go`](../internal/config/files.go)、D-15 | 若将来启用需独立产品/隐私/服务决策；不在本轮暗中增加 |
| F-21 | 未提供已认证 MSIX/Store/MAS 发布物 | [分发设计](design/11-distribution-licensing.md) | D-06/D-12、账号/审核/签名、W-T8；不是本地构建能认证 |
| F-22 | 隔离宿主 WASAPI / ScreenCaptureKit、Go FFT 128 log bins/RMS、平滑/限频、Shader uniform 与 Web 单向回调；导入同意/撤销/默认关闭总开关 | [`reactive`](../internal/reactive/)、[`shader`](../internal/render/shader/)、[`ui/reactive.go`](../internal/ui/reactive.go) | 纯 Go 与 UI 已测；WASAPI 原生设备、macOS TCC/采集、实际功耗及长期运行仍需真机；Linux unsupported |
| F-23 | 未提供 macOS 锁屏 Aerial 替换；不调用非公开替换流程 | [macOS 设计](design/04-macos-desktop.md)、D-11 | 产品/安全/分发决定与真机实验；不能作为标准支持 |
| F-24 | Linux X11 产品参考路径及 GNOME/D-Bus 实验代码保留；非全 Wayland/GNOME/KDE 发行支持 | [`desktop/linux`](../internal/desktop/linux/)、[Linux 设计](design/05-ubuntu-compat.md) | D-14、Q-L4/5/7、真实 Shell 会话联调；嵌套实验测试单列 |
| F-25 | 原规划“以后”；未提供在线画廊、工坊、云同步、创作服务或包签名基础设施 | [包与分发设计](design/10-package-format.md) | 服务端、身份/内容授权、协议、签名/更新信任链及运维；本地示例不是服务 |
| F-26 | 原生 Win 每帧轮询 / mac NSEvent 鼠标监视；桌面命中门控、iMouse、负坐标/混合 DPI/跨屏映射、L1+/原始全屏信号阻断、Web 单向回调 | [`reactive`](../internal/reactive/)、[`app/reactive.go`](../internal/app/reactive.go) | 纯 Go/应用/UI 测试；原生桌面命中、多屏、锁屏和睡眠仍需真机；Linux unsupported |

## 本轮新增控制的实际支持范围

以下是【当前源码】能力，不是各平台运行测试结果。相应渲染器的 `Capabilities` 与 `ValidateOptions` 防止静默忽略不支持的非默认值；编排器回退默认并显示原因。

| 控制 | mpv（Windows/Linux） | MF（Windows） | AVF（macOS） | 静态图 |
|---|---|---|---|---|
| crash / child / lifecycle 日志 | Go crash、2 MiB stderr、mpv warn 与转场/IPC 阶段；MF hello/build 与有界文本 | Go crash、web/shader PID/build、转场与有界 stderr | Go crash、mpv warn、转场/IPC 与有界 stderr | 纯 Go 回归含空文件清理、最新 5 份非空 crash 保留与当前文件保护；Windows MF、各平台原生 child/crash 行为仍需实机验证；Go crash 不等于 native dump，mpv log-file 无运行时硬限额 |
| 日志与配置故障恢复 | 共用纯 Go：日志瞬时错误后继续写；普通 .bak 失败仍保存主文件并报告；迁移备份失败仍中止 | 同左 | 同左 | 同左；故障注入回归覆盖，真实磁盘满/杀毒软件锁文件仍需各平台验证 |
| 每屏 worker 状态隔离 | 独立指针；纯 Go 并发回归 | 同左 | 同左；L3 释放/恢复已有 #14 实测，真实每屏编排仍待验证 | 共用编排结构 |
| fill / fit / stretch | 已接入，可原位调整 | 已接入：源裁切 / MF letterbox / 交换链 stretch，可原位调整 | 已接入，可原位调整 | 单屏自定义不支持；跨屏由图片裁切实现 |
| 每屏 cover rendition | 相同 key 复用；换源保留进程、位置、暂停与 cap | 选择缓存；换源按现有恢复路径重建 | 选择缓存；原生 helper 支持 cover；换源重建 | 不适用 |
| 播放速度 | 0.25–4 倍，可原位调整 | 0.25–4 倍，原生 SetPlaybackRate，可原位调整 | 0.25–4 倍，可原位调整 | 不适用 |
| 静音 / 声音 | 已接入，默认静音 | 已接入，默认静音 | 已接入，默认静音 | 不适用 |
| 视频帧降采样 | 0.25–1 倍软件滤镜 | 不支持 | 不支持 | 不适用 |
| FPS cap / 电池 15fps | 已有 fps 滤镜 | 已有 Transfer/Present 节流 | 已有 videoComposition 路径，未真机 | 不需要动态渲染 |
| 位置记录/恢复 | 按屏+素材持久化检查点；L3/换源等待 file-loaded 后应用 pause，IPC 使用重叠 I/O 与写 deadline；静默管道 pause=false 回归待 Windows CI 执行，真机复测仍待完成 | 父/子双端命名管道均使用重叠 I/O；Accept 超时取消；待 Windows CI/真机验证；已接入 Get/SetCurrentTime；seek 完成重新应用播放意图；L3 退出等待与资源释放已有测试。真机确认 frame-server 启动 seek 仅在应用 Transfer 一帧后才收到 SEEKED；启动阶段现持续服务帧直到 SEEKED，修复 L3 恢复及初始 Load（Start > 0）等待 10s 超时，L3 恢复实测 6/6 成功 | 已接入延迟 seek/位置采样；L3 释放 AVQueuePlayer/looper/layer，保留有界静态帧，恢复首帧就绪后无动画切回（2026-10-25 修复，见 VALIDATION）；Apple Silicon 实测释放解码资源，恢复前后位置差 ≤20 ms（#14，见下） | 不适用 |

### 暂停释放实测（#14，2026-10-23）

以下为临时 harness（未提交）测量，非完整平台验收；素材为 1024² H.264、180 s 视频。

- **AVF**：本开发机 macOS / Apple Silicon，L0 → L1 → L3 后做 5 次 L0/L3 循环。L3 释放解码资源：IOSurface regions 从 L0/L1 的 19 降至 5；线程从 L0 的 24–26 降至 15–19。`phys_footprint` 约 18–23 MB、变化很小，解码帧不计入该指标；5 次循环中每次约增长 0.4 MB，是否泄漏未定，需更长运行。恢复前后位置差 ≤20 ms；`SetLevel` 返回耗时 1–12 ms，约 1.3 s 内观察到播放推进（受 1 s / 250 ms 采样限制），离群值为 2.3 s、3.3 s。L3 → `UpdatePlayback`（音量）→ L1 保持暂停，再到 L0 恢复。
- **MF**：Windows 11 / RTX 3060 Laptop / 2560×1600，同一素材、flip swapchain、同一 harness。L3 保留 mfhost：GPU Dedicated 约 65 MB → 0、Shared 约 15 MB → 0；子进程 private bytes 约 120 MB → 57 MB；线程约 39 → 16。修复前 L3 恢复每次均在 10 s 超时，frame-server 的 SEEKED 仅在应用 Transfer 一帧后到达；修复后 6/6 次恢复成功，`SetLevel`（含 seek 和首帧）耗时 0.47–0.61 s。L3 → L1 保持暂停，再到 L0 恢复。子进程 private bytes 每循环约增长 0.6 MB，是否泄漏未定。L3 中 `UpdatePlayback` 返回 unsupported，应用回退重建（既有行为）。
- **mpv**：两台机器均未安装，未测。本次结果仅适用于上述设备、素材和 L3 路径，不代表所有暂停等级或后端的显存释放表现；AVF FPS cap 等其他路径仍未真机验证。

重要边界：

- “视频帧降采样”不是缩小桌面窗口，也不是已经证明省电；软件滤镜可能触发硬解帧回读，必须测开销。不宣传为无损零拷贝优化。
- mpv/MF/AVF 的每屏 fit/speed 优先调用 `UpdatePlayback`，不应自行恢复已暂停播放器；不支持时才重建，其他错误报告给 UI。全局音频/降采样/后端等设置仍可能重建。原 F-18 的整体无重载与 ≤200 ms 真机 AC 仍开放。
- MF 在渲染线程串行执行原生属性控制，启动等待媒体/seek 就绪，L3 保存位置；实时更改不调用 Play/seek，失败尝试回滚，回滚失败则标记状态不可信并交监管重启；实时控制 IPC 超时/传输结果不确定、停止/恢复收到原生 fatal 错误时也终止旧宿主，由监管恢复，不能假定命令没有执行。素材/系统不支持的速率会报错，0.25–4 倍是请求范围而非对所有编码格式的成功承诺；MF 画布上限为 16384 像素/轴，设备实际限制可能更低。AVF 同样新增位置采样与延迟 seek。两者 L3 实测范围见上；其他原生路径和无缝循环仍待真机验证。
- 应用规则匹配前台进程 basename，不读取窗口标题；Windows 使用进程映像，macOS 使用 NSWorkspace，X11 使用活动窗口 PID 与本地 `/proc`。Wayland 或无法获得进程的会话不宣称有效。
- 导入和编码采用有界 FIFO（各 256），编码限制单活跃任务；删除会取消关联的排队/运行工作。相关测试覆盖多选导入不丢项、替换/删除与旧编码结果竞态，最终全套结果仍以验证记录为准。
- macOS 默认原生 AVFoundation helper 与可选 ffmpeg 是严格路线，要求 H.264、恒定 30fps、无音轨、首同步样本/GOP 和偶数尺寸约束；MP4 样本表验证不是完整码流解码。avconvert 只作明确标注的有限回退，不支持 GIF、固定 30fps、目标尺寸或去音轨。Windows ffmpeg 必须具备 h264_mf；框架/编码器名称不证明实际硬件加速。
- 目标分辨率 `display` 取主屏当前模式 backing 像素，无主屏时取首个有效屏，最后回退 3840×2160；也可显式选择 1080p/1440p/2160p。任务开始时重新读取拓扑。macOS 逻辑坐标与 backing 像素分离，Windows 不重复乘 DPI；不是固定取面板最大分辨率。共享回退输出仍按边界保比例、不放大、偶数对齐。独立显示器在有效 fill 且静音时，按自身 backing 像素（偶数对齐、每轴最大 8192）懒生成 center-cover H.264/30fps/无音轨版本，允许放大；相同素材修订/尺寸/裁切 key 共用 `cache/<id>/renditions/<key>/video.mp4` 与 manifest。严格探测通过后发布；无缓存回退当前共享入口或原文件。缓存纳入空间统计，清理保留当前目标、已加载和队列中的 key，删除素材移除全部缓存。原生 helper 仍受 canvas 像素预算约束，失败保留回退。多屏裁切质量、混合 DPI/热插拔、暂停换源和原生 UI 仍待真机验收；进度为排队/执行中/完成状态，不提供编码百分比。
- 跨屏是每屏独立宿主/渲染器对共同画布的空间裁切，保留按屏暂停；未实现单解码器和跨屏帧时钟，不承诺视频帧同步。静态图与视频首帧使用相同画布映射，详见 [SPANNING.md](SPANNING.md)。
- 本周暂停时长只累计已确认的动态壁纸 L1–L4，L0 单列；不计静态图、未启动/失败、离线和系统睡眠，正常 30 秒检查点，超过 45 秒的异常间隔整段丢弃。周界为 UTC 周一，时钟回退不重复计时。上限 64 个显示器记录，仅保存摘要键及有限计数，不是实际/估算节电量。
- 文件夹导入不遍历符号链接/隐藏子目录；包目录作为整体导入，失败项汇总，不把有问题的包拆成媒体“假成功”。
- 主色索引只读取本地静态预览：最多 16 MiB、16×1024² 像素，最多三组中英文色名；无效/超限/缺少预览时保留导入但不生成颜色索引，不能保证旧条目均有颜色。
- 存储统计扫描实际库/缓存路径；清理只移除无引用缓存，不清空仍用于首帧/降级的有效预览。见 [`storage.go`](../internal/app/storage.go)。

## Web 壁纸支持（F-12）

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 包导入/属性/同意 | `.wpk` 与 manifest 目录；Lively/WE 纯本地 HTML；共享 wpk 安全校验 | 同左 | 可管理/导入包，播放能力为 false |
| 页面隔离 | `-webhost` + IPC v1；每包临时 user data folder；virtual host `<id>.wpk.invalid` | `-webhost` + IPC v1；独立桌面 NSWindow；`wpk://<id>/` + os.Root；nonPersistent data store | 不启动网页引擎 |
| 网络与宿主权限 | 默认非来源请求 403；使用 `_22` filter 覆盖 document/worker；取消外部导航、新窗口和下载；设备权限拒绝；禁宿主对象/消息桥/devtools/菜单 | 未授权时编译 WKContentRuleList 阻断 http(s)/ws(s)；取消外部导航/新窗口/不可显示响应；拒绝媒体权限/文件选择；不注册消息桥 | 不适用 |
| 暂停/限帧 | L1–L3 截取最后帧到 STATIC 子窗口，再隐藏 controller、TrySuspend；恢复移除截图；L4 退出进程 | L1–L3 隐藏 WKWebView；L4 退出进程；隐藏后的 CPU/rAF 节流强度待实测 | 不适用 |
| 自定义属性 | slider/color/select/bool/text；JSON 编码单向 ExecuteScript；兼容 wpk/Lively/WE 回调；暂停期间保存更新，恢复重推 | 同左，evaluateJavaScript | 保存属性但不执行 |
| 运行时边界 | 需要支持 `ICoreWebView2_22` 的 WebView2 Runtime；官方 loader 或 Evergreen client；缺少/过旧明确报能力错误 | 需要正常图形登录会话与 WebKit；透明背景要求 macOS 12+ | 明确 unsupported |

联网仅在包声明 network 且本次导入用户明确同意后启用；声明域名用于同意对话框展示，不是授予权限本身，也不是允许后的域名白名单。拒绝仍导入离线包；撤销会关闭旧进程并以拒绝状态重建。替换包的所有文件先通过安全校验，再请求同意并排空旧渲染器，防止新内容沿用旧进程的授权。`audio-reactive`、WE 音频处理、可执行文件仍拒绝。

验证：新增策略/导航/转义、host fake、导入示例/恶意目录、同意持久化/撤销、schema 迁移、UI Tester 和电池/实时属性测试。三平台 vet 与 CGO_ENABLED=0 构建通过。完整 `go test ./...` 在既有 `internal/update.TestIncrementalUpdateBootRollback` 的 httptest 本地监听处被当前沙箱拒绝（`listen tcp6 [::1]:0: bind: operation not permitted`）；其余包通过。macOS 子进程 smoke 已实际调用规则编译；图形会话不可访问（NSScreen 列表为空，系统 XPC connection invalid），因此页面显示、网络阻断、暂停效果未获真机证据。Windows 原生调用尚无本机执行证据。未实现 CPU/GPU 使用量看门狗；rAF cap 不等于限制任意 JavaScript、CSS 动画或视频帧率。

## Shader 支持（F-14）

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 引擎隔离 | `-shaderhost` + IPC v1；纯 Go COM；桌面宿主 HWND 下创建子窗口 | `-shaderhost` + IPC v1；purego/objc；desktop/mac 创建进程自有桌面 NSWindow/NSView，挂 CAMetalLayer；不跨进程解引用 NSView 指针 | Capabilities 不提供 Shader；加载明确报 unsupported |
| 原生 GPU | D3D11 SM 5.0；d3dcompiler_47.dll 运行时编译；flip-discard 双缓冲、waitable swapchain、全屏三角形 | Metal 运行时 newLibraryWithSource；全屏三角形；presentDrawable 与定时器限帧 | 未实现 GLX，不经 WebView 或视频后端回退 |
| 分辨率/计时 | 0.5–1.0 离屏 render target + 线性采样放大；三组 timestamp/disjoint 查询 | 离屏 Metal texture + 线性采样放大；读取 command buffer GPUStartTime/GPUEndTime；按 backing 像素计算比例 | 可保存属性 |
| 调度 | 默认 30 FPS、0.75 比例；每项 15/30/60 FPS 覆盖；电池/saver 仍取更严格上限；L1–L3 停呈现并冻结时间；L4 退出，支持位置检查点；宿主无心跳 5 秒由父进程终止并交给现有监管恢复 | 同左 | 不呈现 |
| 属性/输入 | slider → float，color → RGB float3，bool → bool，select → 从 0 起的选项序号；显式 16 字节槽；iFrame 按 int32 位存储；iMouse、128 个音频 bin 与 RMS level 在授权/总开关/可见性全部通过后由原生采集更新，否则为零 | 相同布局；MSL 显式解包 | 保存属性，不执行 |
| 导入/预览 | `.wpk`/manifest 目录及自动包装的 `.glsl`/`.frag`；发布包前验证两种转译；独立 helper 尝试离屏 GPU 缩略图，失败使用基于内容哈希的确定性占位图 | 同左 | 转译/管理包与生成占位图 |
| UI | 有对应条目时显示网页/Shader 筛选；propRow 比例/FPS/用户属性；demo 与 uishots 包含两种侧栏 | 同左 | 同左，播放仍 unsupported |

转译范围：mainImage 单 pass、float/int/bool 与 vec/ivec/bvec、方阵、列主序构造/矩阵尺寸转换、矩阵乘法及读取列、swizzle、函数 in/out/inout、const/全局变量、简单对象/函数宏与精度限定符。`mod` 使用独立 helper 保留 floor 语义及参数单次求值。`texture(iAudio, uv)` 返回预留零输入；尚无 iChannel 外部纹理资源、数组、结构体、include/条件预处理、多 pass 或矩阵列写入支持；这些输入报错，不静默转入其他引擎。select 的 uniform 是选项序号，不是任意字符串值。缩放到 0.5 后仍可能超过 4 ms，控制器不承诺所有素材都能满足预算。

验证（2026-10-22）：19 组 GLSL → HLSL/MSL golden、错误/复杂度边界、uniform 对齐、假 GPU 自适应/暂停、帧策略零 Go 分配、mac 整数 objc 桥零 Go 分配、导入/非法替换、原生 UI、Shader 电池与实时控制、WebView 延迟启动/退出与 rendition 删除等待回归。`shadergpu` 测试已接入 CI；本次 macOS 返回 `no Metal device available` 而跳过，不能声称已编译/显示成功；本机也未安装独立 Metal 编译工具。Windows 原生调用仍待目标设备执行。完整驱动/API 路径的分配和 GPU 性能仍需真机测量。uishots 已生成并检查 Shader 侧栏，不能替代桌面渲染证据。

本次三平台 vet 与要求的无 cgo 构建通过；`go test ./...` 的其余包通过，但既有 `internal/update.TestIncrementalUpdateBootRollback` 被沙箱阻止本地监听：`listen tcp6 [::1]:0: bind: operation not permitted`。没有修改该测试掩盖环境限制。Go 缓存放在 `/private/tmp/wpapp-f14-go-cache`，以避开默认缓存的沙箱访问拒绝。

## 发布支持

| 范围 | 已实现 | 仍需验证 |
|---|---|---|
| 原生紧凑 UI | 隐藏系统标题文字并保留原生窗口按钮，使用 TitleBar insets 和拖动区域；壁纸库全高右栏、独立属性滚动与对齐的 38px 底栏，宽窗标题/工具栏合并；显示器与设置页保持全宽；响应式工具栏（820/900/1033/1280/1440px、断点两侧及缩放往返的空/有内容 Tester 回归）；默认深色、三层蓝紫底色与选中/主操作光晕；下拉筛选、32 项演示库、紧凑可关闭错误栏；缺编码器时自动按屏优化跳过，手动请求保留本地化提示 | Windows 真机标题栏拖动、最小化/最大化/关闭、混合 DPI；macOS/Linux 标题栏与桌面环境回退仍需真机验证，离屏截图不证明系统窗口行为 |
| 构建身份与兼容 | CLI、启动头（含 PID）、About、诊断共用 buildinfo；commit time 与未知值明确；IPC major 常量；四类未来 config 写后字节保持；release 验证核对 VERSION | 原生 About 的目标设备展示；真实签名更新/安装、macOS 更新后签名完整性；生产 feed 启用前仍须修复更新事务与统一稳定启动确认 |
| goroutine 生命周期 | 存储扫描/缓存清理取消与结果回写守卫；IPC 断连/回复写入期限与 marshal 错误回归；排除 MF engine 与 Windows web host 的生产 go 语句已审查归属 | 原生文件系统调用不可中断；Handler 须自行返回；真实设备关闭/管道释放仍需验证，两个排除文件由独立任务负责 |
| 标签发布 | 分支/PR CI；master 可达的 SemVer 标签触发；五目标包与 SHA256SUMS；自有代码采用 [PolyForm Strict 1.0.0](../LICENSE)，安装包及可选更新包携带 LICENSE 与第三方声明，NSIS 提供许可页；凭据缺失时明确标记未签名预发布；保留原始构建并续传 draft | GitHub hosted 三系统实际运行；真实 Windows/Apple 证书、公证与安装卸载；更新源部署及 macOS 更新后签名完整性。见 [发布说明](releases/README.md) |

## 配置与数据迁移

当前 `config.json` / `state.json` 为 schema 2，`assignments.json` 为 schema 3，`library.json` 为 schema 4。库新增 Web 定义、每项 networkGranted 与 ItemProps.webValues；3→4 迁移保留原播放属性并强制网络默认拒绝。新增开关默认静音、启用位置记录、自动转码关闭、普通启动打开设置；转码分辨率默认 `display`。独立/跨屏布局、跨屏素材与选项保存在 assignments；本周指标保存在 state，而不是历史设计中的独立 metrics.json。旧配置迁移保留已有值并生成版本备份；高版本文件保持只读。每屏选项跟随稳定显示器 ID 迁移，指标使用独立的 SHA-256 前 16 字节十六进制键，不把硬件原始标识写入指标集合。

来源：[`config/files.go`](../internal/config/files.go)、[`config/options_test.go`](../internal/config/options_test.go)、[`config/metrics_test.go`](../internal/config/metrics_test.go)。这不是回滚兼容性认证：旧程序读取更高 schema 的行为、迁移备份恢复和真实用户库升级需按 [验证记录](VALIDATION.md) 复核。

## 尚需区分的完成条件

- **已有源码但缺目标平台证据**：Win/mac 宿主、空间跨屏、原生编解码与播放控制、电源/睡眠、性能预算、签名/公证及安装。不能只以云端测试关闭，也不应重新描述为“完全没有实现”。
- **仍缺架构或产品实现**：F-14 的 Linux GLX 后端及超出已声明子集的 Shader 功能，F-25 在线服务/内容授权/信任链，以及其他矩阵明列未提供项。真机测试本身不能补齐这些代码。
- **后续范围仍需确认**：F-12、F-14 已按本次任务扩展；原规格把 F-12/F-14 定为 v1、F-25 定为以后、F-22/F-26 已由本次任务明确授权扩展（见下表）。本轮补全不能默默改变原分期，也不代表用户已同意从“全部功能”中排除它们；扩展到完整 v1/roadmap 的范围仍待确认。

## 状态更新规则

- 新控件只有在持久化、控制器调用、实际后端行为/错误报告全部接好后，才记“已接线”。
- 测试通过只能关闭对应覆盖范围，不能从 Linux 单元测试推出 Windows/macOS 合格。
- 表中未提供项不是“静默完成”；保留缺口及最小闭环条件，交付时明确说明。
- 不以新的 UI 工作自动批准 D-* 的发布主体、费用、签名证书、遥测、商店账号或线上服务。

## F-22 / F-26 支持与验证边界

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 系统音频 | WASAPI 默认播放端点 loopback，purego COM，shader/web 子进程 | ScreenCaptureKit audio-only 输出，macOS 13+，purego objc；未授予 TCC 时明确报 unavailable，不自动请求 OS 授权 | unsupported |
| 鼠标 | 每渲染帧轮询位置和鼠标按钮；桌面根窗口命中 | NSEvent mouse-only 全局 monitor；CGWindowList 点遮挡 | unsupported |
| Shader | 128 log bins + RMS + iMouse，D3D11 cbuffer | 相同槽位、Metal constant buffer | 不渲染 |
| Web 次级目标 | onAudio(Float32Array)、onInput 数值对象单向注入 | 同左 | 不渲染 |
| 授权/停止 | 导入逐项默认拒绝、通用总开关默认关闭、侧栏撤销；L1+、原始全屏/遮挡、锁屏/睡眠/关屏关闭源 | 同左；TCC 是额外系统权限 | 可管理包与授权，不采集 |

测试覆盖合成正弦频谱/RMS/平滑、限频、权限门控、fake source 生命周期、iMouse、负坐标/混合 DPI/跨屏映射、应用层总开关/撤销/全屏阻断和原生 UI 默认拒绝/撤销流程。平台原生接口仍需 Windows/macOS 真机采集、TCC、锁屏/睡眠、多显示器及功耗验收；交叉构建不能替代这些测试。安全限制见 [8.5](design/08-web-shader.md#85-f-22--f-26-原生输入新产品决策)。

本次验证（2026-10-22）：使用 Go toolchain 自带 gofmt；`gofmt -l ./internal ./cmd` 无输出。`GOOS=windows/darwin/linux go vet -unsafeptr=false ./...` 分别通过；本机、Windows、Darwin 的 `CGO_ENABLED=0 go build ./cmd/...` 分别通过（`GOCACHE=/tmp/wallpaper-go-cache`）。`go test ./...` 已执行，只有 `internal/update.TestIncrementalUpdateBootRollback` 因沙箱拒绝 `[::1]:0` 本地监听而失败，其他包通过；没有为通过测试弱化或跳过这个用例。`go test -tags shadergpu ./internal/render/shader -run TestMetalGoldenCompilation -v` 因无 Metal device 跳过。

Windows hosted UI 用例已改为点击布局框的稳定无障碍标签“选择显示器 2”，并额外断言选中 `d2`，保留复制后默认壁纸/两屏分配断言。本机 UI 测试通过；GitHub API 网络连接失败，不能读取 hosted 日志或确认 Windows runner 复跑结果。DPI 宿主线程在建窗前显式设为 Per-Monitor V2，防止指针坐标虚拟化。

## 紧凑原生 UI 验证（2026-10-22）

- 32 项演示库；1920×1080、320px 全高属性栏打开、导入状态在底栏时，`TestLibraryDensity` 测得 7 列、5 个可见行、首项顶边 56px（Windows 38px caption inset）；断言至少六列四行、面板顶边不超过 caption inset、底边到达窗口底部、左底栏不进入面板。新增标题栏左右 insets/显示器菜单、错误关闭和缺编码器自动跳过/手动提示回归。
- 指定的 1920×1080 中文、1100×700 中文、1280×800 英文三组 `cmd/uishots` 均成功输出；另加 `-caption-inset 38` 的 Windows 留白模拟。人工查看库页浅深色、英文工具栏、设置和显示器页面。新增空库固定栏宽/非库页全宽、三种 caption 布局、独立滚动时预览/应用区/网格不移动的 Tester 回归。截图为演示数据离屏渲染，不能替代 Windows 原生窗口与 DPI 验证。
- 使用 `GOCACHE=/private/tmp/wp-panel-go-cache`：GOROOT 内 `gofmt -l ./internal ./cmd` 无输出；Windows/darwin/Linux 的 `go vet -unsafeptr=false ./...` 全通过；本机、Windows、darwin 的 `CGO_ENABLED=0 go build ./cmd/...` 全通过。
- `go test ./...` 已执行：除 `internal/update.TestIncrementalUpdateBootRollback` 因沙箱禁止 `listen tcp6 [::1]:0: bind: operation not permitted` 失败外，其余包通过。未跳过或修改该测试来掩盖限制。

本轮恢复回归覆盖：应用层一次恢复失败重建及提示、持续失败上限、60 秒 L3 释放后恢复、300 ms 去抖及排队期间真实视图、MF seek 完成、mpv 换源与 file-loaded/暂停命令顺序。单元测试与跨平台编译不代表 Windows GPU/驱动真机通过；设备仍冻结时请提供 `%LOCALAPPDATA%\WallpaperApp\logs`。
