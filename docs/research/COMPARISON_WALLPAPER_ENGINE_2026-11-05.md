# Wallpaper Engine 2.8.42 与本项目对比

**日期**：2026-11-05
**范围**：本机安装的 Wallpaper Engine 2.8.42（Steam appid 431960，build 26168761）与本仓库 `master` 的 `9f09dbd`（已合并状态菜单与离线 scene 探测）。
**方法**：只读检查源码、文档、`config.json`、locale、项目文件、安装产物与运行中进程；未运行 Wallpaper Engine 或本项目的任何 exe。下文用【源码证实】【实测观察】【文档声明】【推断】区分可信度。

## 一句话结论

两者不是同一类产品：Wallpaper Engine（下称 WE）是 Windows 单平台的「3D 场景引擎 + 可视化编辑器 + Steam 创意工坊」内容生态型商业软件；本项目是 Windows/macOS 双平台的轻量本地播放器，强在平台覆盖、体积、功耗治理与隐私，弱在 Scene 内容、编辑器、工坊、语言与可交付性。

## 速览

| 维度 | Wallpaper Engine 2.8.42 | 本项目 | 判定 |
|---|---|---|---|
| 壁纸类型 | Scene / Video / Web / Application 四类 | video / image / web / shader；GIF 走视频管线 | 我们较弱 |
| 3D/场景 | D3D11 场景引擎，PBR、粒子、脚本；本机 38/63 工坊项为 Scene | 无 Scene 运行时，只有 `-sceneprobe` 离线结构探测 | 我们较弱 |
| Web 壁纸 | 自带 CEF/Chromium（`libcef.dll` 259.4 MiB），另有 Edge WebView2 备用 | 系统 WebView2/WKWebView，独立子进程，默认禁网 | 各有取舍 |
| Shader | 多 pass 材质/效果，`assets/shaders` 137 个文件、`effects` 46 类 | Shadertoy 单 pass 子集，19 组 GLSL→HLSL/MSL golden | 我们较弱 |
| 视频播放 | MF / DirectShow(LAV) / Chromium / Native 可选 | mpv / MF / AVF；导入仅 `.mp4/.mov/.m4v/.webm`；默认不捆绑 mpv | 各有取舍 |
| 多屏/跨屏 | 每屏、stretch、clone、split、profile | 每屏独立、原生跨屏画布；无 clone/split/profile | 各有取舍 |
| 暂停策略 | 焦点/全屏/最大化/电池/睡眠等触发 + 应用规则 | 每屏 L0–L4、14 个原因位、300/150 ms 去抖 | 各有取舍 |
| 内容生态 | Steam 工坊 1,000,000+ 壁纸；本机 63 项 / 3.28 GiB | 仅本地导入；两个示例包 | 我们较弱 |
| 编辑器 | 场景、粒子、模型、Shader 编辑器与工坊发布 | 无编辑器，F-25 仍为「以后」 | 我们较弱 |
| UI 技术 | CEF/Chromium + HTML/CSS/AngularJS | Go + MyGo 原生绘制，无 WebView | 各有取舍 |
| 平台 | Windows 10/11；Android 伴侣应用 | Windows/macOS 四目标；Linux 暂停 | 我们更强 |
| 语言 | 36 种 locale（完整度不一） | 中/英 2 种 | 我们较弱 |
| 体积/内存 | 安装目录 1.7 GB（文件字节 1.65 GiB），后台约 70–87 MB WS；UI 打开时另有多个 CEF 进程 | 安装包 4.9 MB；本机安装目录 131 MB（含外部 mpv 116 MB）；后台约 32 MB WS | 各有取舍 |
| 隐私/遥测 | Steam 集成、随装诊断/转储组件；实际上报范围无公开证据 | 默认禁网、`telemetry=false`、无账号/云 | 我们更强 |
| 商业模式 | Steam US$4.99 / ¥22.90 买断 | PolyForm Strict 1.0.0，源码可见、非商业免费 | 各有取舍 |

## 1. 定位与范围

WE 是 Windows 单平台内容平台。本机 63 个工坊项中 Scene 38（60.3%）、Video 19（30.2%）、Web 4（6.3%）、Preset 2（3.2%）；2 个 Preset 是配置覆盖，实际可播放壁纸 61 项。官方 19 个项目里显式 `type=scene` 14 项、Web 2 项，`audiophile`/`techno` 按结构推断为 Scene，`sheep` 指向 `sheep.exe`，属于 Application【实测观察/推断】。

我们的播放/库类型是 video、image、web、shader。GIF 只有在导入器同时具备转码器且允许 GIF 转码时才转成视频，否则保留 `.gif` 交给播放器；这是当前 MVP 的已知偏差【源码证实】。WE 导入只接受 video 与纯本地 web，scene/application 返回 `ErrUnsupported`；`-sceneprobe` 只输出离线特征报告，不接入导入、UI 或播放【源码证实】。

按现行规则，本机 63 个工坊项中可导入 22 项（34.9%：19 个 Video + 3 个无音频处理的 Web），41 项（65.1%）被拒；官方 19 个项目仅 `corsair_collection` 一项可导入，`corsair_o_tron` 因声明音频处理被拒【源码证实 + 实测观察】。这是比「22/63」更值得注意的现实：**WE 用户迁移过来的库，大部分不能直接播放**。

因此定位不同：WE 卖的是创作—发布—订阅闭环；我们做本地播放与桌面治理。目标用户是关注功耗、多屏与隐私的 Windows/macOS 桌面用户，以及想把 Lively/WE 素材迁入本地库的人，不面向创作者。

## 2. 渲染与播放

WE 是自研 D3D11 场景引擎：二进制含 `D3D11CreateDevice`，支持 PBR 灯光、阴影、反射、体积光、Bloom、粒子；`assets/shaders` 137 个文件、`assets/effects` 46 类/1,049 个文件、`assets/presets` 20 类/880 个文件、26 个 `.mdl`、`scenescript64.dll` 28.7 MB；HDR 由 Ultra 档与 `combine_video_hdr.frag` 推断支持，未开启验证【实测观察/推断】。`config.json` 存在两个用户档（`33114` 与 `WsiAccount`），当前档为 fps=25、preset=high、msaa=x2、postprocessing/reflection/shadows/volumetrics=high；另一档为 fps=15、preset=medium、msaa=none。引用「当前值」必须带档位【实测观察】。

我们是 mpv/MF/AVF：Windows 默认 mpv（`--hwdec=d3d11va`），MF 自建 D3D11 设备，macOS 用 AVFoundation；`fpsCap` 0–240、默认 30，saver 取 `EffectiveCap(15, 全局值)`，电池默认策略是暂停而不是固定 15 fps【源码证实】。缩放仅 mpv 后端开放 50–100%，MF/AVF 不支持 Scale；Shader 是 Shadertoy `mainImage` 单 pass 子集，无 iChannel、多 pass、数组、结构体、include；没有 MSAA、纹理质量、后处理、HDR 参数【源码证实】。

导入层只收 `.mp4/.mov/.m4v/.webm`，`.mkv` 在导入时就被拒绝；WE 的二进制字符串列了更多扩展名，实际路径由 MF/LAV/Chromium/Native 决定，不能仅凭字符串下结论【实测观察/推断】。Web 侧我们用系统 WebView2/WKWebView，WE 主路径自带 CEF（`libcef.dll` 259.4 MiB），但 WE 也提供 Edge/Chromium 框架选项，不能把「WE Web = CEF」绝对化【实测观察】。

双方硬解、功耗、长期稳定性都缺同片源同负载实测，本轮不做胜负结论。

## 3. 桌面集成

桌面宿主是我们少数能同时拿源码和本机日志证明的强项：Windows 24H2 下检测 Progman 的 `WS_EX_NOREDIRECTIONBITMAP`，按 DefView/WorkerW 逐屏建子 HWND，日志有 `raised24H2=true`、两块屏各一个 layered surface；显示器 ID 走四级回退并保留旧 ID 迁移；暂停有 14 个原因位、每屏 L0–L4、300/150 ms 去抖、2 s/60 s/10 min 升级、低电阈值 20%【源码证实 + 实测观察】。安装按用户写 `%LOCALAPPDATA%`，不需要管理员；macOS 每屏 `kCGDesktopWindowLevelKey` NSWindow 有源码，但文档明确未真机验证。日志里的桌面 probe 平均约 2.7 ms、最大 47 ms，高于内部 `<1 ms` 目标，这是要直说的弱项【实测观察】。

短板同样明确：没有 Windows 服务、锁屏图覆盖、屏保宿主、应用壁纸、虚拟桌面、clone/stretch/split 分组；应用规则只按 exe basename 首条命中。最实在的问题是系统壁纸只写不读：`assignItem` 每次调用 `installSystemWallpaper` 写首帧/静态图，没有 `GetWallpaper`、备份或还原，NSIS 卸载也不恢复，与架构文档中「记录原壁纸并在卸载时恢复」的表述不一致；用户停用后会停在静态首帧【源码证实】。

WE 这套更完整：`Wallpaper Engine Service` 以 LocalSystem/AUTO_START 运行，服务二进制被复制到 `C:/Windows/system32/wallpaperservice64.exe`；`overridelockscreen=true`，`log.txt` 有 361 条 `setlockscreen`、357 条 `SetImageFileAsync` 记录；桌面壁纸覆盖图为 6400×2160、备份图 3840×2160；`wpxscreensaver64.scr` 存在但本机未安装/未激活；`apprules=null`、layout/profile=null，说明「locale 提供」与「本机启用」是两回事【实测观察】。WE 当前以 `-safe -silent -service` 运行，正常渲染路径本轮不评价。

## 4. 内容生态

WE 的内容闭环可量化：19 个默认项目 987 个文件/约 142 MiB，2 个模板 351 KB，`assets` 2,947 个文件/75.8 MiB；`ui_en-us.json` 3,332 个键中 `ui_editor_*` 有 2,163 条（64.9%）；本机 63 个工坊项 3.28 GiB，63/63 有预览【实测观察】。

我们只有 `examples` 两个包（8 个文件/5,153 B），没有编辑器、打包/导出命令或在线工坊。`.wpk` 是 zip + manifest v0，支持 video/web/shader/image 四类，preview 必需；解压限制 10,000 条目、4 GiB 总量、256 MiB 非视频单文件、200:1 压缩比、preview 2 MiB，并拒绝 exe/dll/msi/lnk、MZ/ELF/Mach-O、符号链接和路径穿越【源码证实】。Lively 13 类只接 video/gif/picture/web 4 类；WE 只接 video/web；web 无预览时写 1×1 PNG，WE 的 preview/description/tags 未解析——设计文档 10.7 的 HEIC 与 scene 静态替代仍是规划，不是现状【源码证实 + 实测观察】。

格式取舍清楚：我们的 `.wpk` 透明、可校验、可审计；WE 的 `scene.pkg` 私有，38 个包里出现 15 种 `PKGV` 版本头（当前选中的是 PKGV0022），无公开规格（推断）。这既保护了 WE 的编辑器/资源编译链，也把我们挡在实时兼容之外；把反向工程当作核心路线风险很高。

## 5. UI 与交互

我们是 Go + MyGo v0.6.0 原生 GPU 绘制，无 HTML/JS/WebView；7 页（壁纸库、显示器 + 5 个设置子页），684 条英文映射、2 种语言、4 个菜单快捷键、2 种排序、6 个类型筛选；本机 0.3.4 的 `wallpaperd.exe` 14.38 MiB，后台 32.0 MiB 工作集/63.7 MiB 私有/15 线程/446 句柄。文档截图仍基于旧基线，当前 `chrome.go` 已改顶部导航，32 MiB 也只属于 0.3.4——版本证据不齐要写明【源码证实 + 实测观察】。

WE 是 CEF/Chromium + HTML/CSS/AngularJS：`index.html` 含 `ng-app=wallpaperbrowserApp`/`ng-view`，`libcef.dll` 259.4 MiB、`ui/dist` 37.4 MiB；3,332 个 UI 键、2,163 个 editor 键、36 种 locale（`ui_zh-chs` 少 12 键，不能把 36 当成 100% 完整）、11 个皮肤标签、24 个热键键、7 种排序、4 类筛选；`uicache` 229 MiB + `wpcache` 134 MiB。UI 未开时 `wallpaper64` 实测约 70–87 MB 工作集/762–814 MB 私有/94–95 线程；一次 UI 打开时观察到 6 个 `wallpaperui` 进程、工作集合计约 458 MiB（瞬时值，不是基准）【实测观察】。

结论：技术路线和离线能力我们占优，信息架构、本地化、编辑器与热键生态 WE 领先；两边都没有可核验的读屏验收，也都没有 UI 打开状态的帧时间/内存基准，这块不能自夸。

## 6. 工程质量与可靠性

仓库侧：33 个 Go 包、34,212 行生产代码、15,996 行测试、118 个 `_test.go`、526 个 `Test` 函数；配置 tmp→fsync→rename，schema 迁移前写 `.vN`；supervisor 退避 0.5→60 s、10 分钟 5 次崩溃进入 Failed；更新有 Ed25519 清单、HTTPS-only、逐文件 SHA-256、2 次未确认启动自动回滚；本轮聚焦 5 个包 `go test` 通过【源码证实 + 实测观察】。但装机 exe 的 `Authenticode=NotSigned`、FileVersion 为空；feed 为空、无生产公钥、UI 禁用检查更新；UI 与编排同进程，panic 会带走全部屏幕；`-race` 只在 Ubuntu 跑；2000 行限制由 Python 块执行且豁免 `i18n.go`，不是 rulecheck；Windows 暂停/恢复、MF/WebView2、NSIS 许可页与 macOS 注销仍未真机验收【源码证实/文档声明】。

WE 侧抽检 7 个 PE 全部 `Authenticode Valid`，签名者 `CN=Skutta Software GmbH`，证书 NotAfter 2026-10-30 但 DigiCert 时间戳使其在 2026-11-05 仍有效；`wallpaper64` FileVersion 2.8.0.42。安装 6,685 个文件，按文件字节合计 1.65 GiB（`du` 1.7 GB），而 Steam appmanifest `SizeOnDisk=826,555,680 B`，`distribution` 里还有 523 MB 的 `bin` 副本，口径不能混用。WE 无源码/测试产物，不能据此断言其质量差；它的服务/渲染器/CEF UI 分层确实让故障域更小【实测观察】。

空闲时双方 CPU 5 秒增量都为 0；内存我们 32.02 MiB WS/63.66 MiB private，WE 约 70–87 MB WS/762–814 MB private，另有服务 7.4 MB，且 WE 处于 `-safe` 安全模式，功能范围不同，不是纯等价比【实测观察】。总的来说：我们的可审计性和测试可追溯性更好，WE 的生产签名、分发和进程隔离更成熟。

## 7. 平台、分发与商业

我们承诺 Windows/macOS 四目标（windows/darwin × amd64/arm64），Linux 自 2026-11-04 起暂停；公开渠道只有 GitHub `Alpenl/wallpaper`，7 个 release 全是 prerelease。v0.3.4（2026-10-23）Windows amd64 setup 4,916,042 B、arm64 4,142,744 B，macOS amd64 6,227,054 B、arm64 5,680,941 B；Windows amd64 下载 1 次，其余 0。Windows 包 NotSigned，macOS 仅 ad-hoc、无公证；许可证是 PolyForm Strict 1.0.0（源码可见、非商业、禁止再分发，商业需另谈），不是 OSI 开源【实测观察/文档声明】。

WE 是 Windows 10/11 单平台 + 免费 Android 伴侣应用（Google Play 10M+ 安装），Steam US$4.99/¥22.90 一次买断，商店页称工坊 1,000,000+ 免费壁纸；英文评测 235,057、简中 434,596，SteamSpy 估计 20–50M 拥有者（估计值）；创作者不能通过 Steam 卖工坊内容。官方明确不做 macOS/Linux【文档声明/推断】。

macOS 窗口期是真实的，但分发、签名信任、内容生态、商业许可这四件事没补齐之前，平台覆盖优势无法转化为用户；WE 的 Steam 锁定、闭源和体积也是它自己的代价。

## 差距清单

**P0**

1. **Windows 视频开箱依赖自装 mpv。** `BUILD-INFO` 写 `mpv_bundled=false`，默认后端仍是 mpv；本机安装目录 131 MB 中 `mpv.exe` 约 116 MB，`SOURCE.txt` 记录 shinchiro 20261021，不能确证「用户放置」。全新安装时核心视频壁纸可能直接不可用。建议：随包分发经许可审查、带版本/哈希校验的固定 mpv，或完成 MF 真机验收并设为默认；首启做依赖检测与可操作报错。
2. **发布物未签名/未公证。** 已安装 `wallpaperd.exe` 为 NotSigned、无版本资源；macOS 仅 ad-hoc。SmartScreen/Gatekeeper 会拦截，发布只能标 prerelease。建议：Authenticode 覆盖 exe/安装器/helper 并写版本资源；Developer ID + Hardened Runtime + `notarytool` + `stapler`；在干净虚拟机做下载—安装—启动验证，设为发版门槛。
3. **Scene 内容缺口。** 38/63（60.3%）工坊项、官方显式 14/19（含结构推断 16/19）是 Scene，我们只能离线探测。迁移用户的大部分库直接失效。建议：先做设计 10.7 的静态替代（预览图/首帧导入为 image 并标注不可播放），把 `-sceneprobe` 报告接入导入前置检查；实时 Scene 单独立项，不混入日常迭代。

**P1**

4. **分发与更新只有 prerelease。** 7 个 release、Windows 下载 1 次；feed 空、无生产公钥。建议：先发签名稳定 1.0，上 winget 与 Homebrew cask，静态 HTTPS manifest + Ed25519，补灰度、撤销与回滚。
5. **系统壁纸无备份/还原。** 只写不读、卸载不恢复，与架构文档冲突。建议：首写前持久化每屏原壁纸，退出/卸载/关闭覆盖时恢复，补「写入→重启→卸载→断言恢复」集成测试。
6. **没有编辑器/工坊，F-25 仍「以后」。** 建议：若坚持播放器定位，就在 README/关于页明确非目标；若要补，先做 `wpk pack/validate` CLI + 四类型向导 + starter pack，不做全场景编辑器。
7. **商业许可与定价未落地。** PolyForm 禁商业使用/再分发，商业入口只有 GitHub Issues。建议：发布商业条款、价格、支持范围与再分发授权，并明确 source-available 不是 open source。
8. **语言 2 对 36。** 建议：抽资源文件、建漏译/伪本地化 CI，先补日、繁中、德、法、西、俄，再加「跟随系统」。

**P2**

9. **库组织弱：** 6 个筛选/2 种排序，无标签/分辨率/来源筛选、收藏/播放列表；4 个快捷键。建议先做标签+分辨率+来源筛选、收藏、本地播放列表/定时，再做全局热键。
10. **UI 与编排同进程、`-race` 仅 Ubuntu。** 建议加 panic 边界，评估设置 UI 拆进程；Windows/macOS 增加 race 任务。
11. **画质/格式：** 无 HDR、Shader 单 pass、导入不收 `.mkv`。建议 mpv 暴露 target-colorspace/tone-mapping，补 iChannel/多 pass，扩 `.mkv` 探测，按后端能力矩阵展示。
12. **macOS 未真机验收，逐文件更新可能破坏 `.app` 签名封印。** 建议做真机矩阵，改为整包替换或升级后 `codesign --verify --strict`；公证后再开自动更新。

## 我们的差异化优势

- **平台：** Windows/macOS 四目标，WE 仅 Windows（Android 是伴侣应用）。
- **原生轻量：** MyGo 无 CEF，`wallpaperd.exe` 14.38 MiB，后台 32.02 MiB WS；安装包 4.9 MB（运行时另需 mpv）。
- **隐私默认：** Web 默认禁网、telemetry 恒 false、无账号/云；WE 与 Steam 集成，诊断/转储范围无公开证据。
- **每屏治理：** 14 个暂停原因、L0–L4、稳定四级显示器 ID、逐屏渲染子进程与 Job Object。
- **源码可见可审计（非开源）：** 33 包、34,212 行生产代码、526 个 `Test`、15,996 行测试。
- **开放包格式与严格校验：** `.wpk` schema 公开，Lively 4 类导入；WE 官方仅 1/19 项目可导入。
- **跨屏画布：** 按物理屏裁切、保留负坐标/空隙/混合 DPI；静态图交系统壁纸，CPU 近 0。

## 不建议照搬

- **1.65 GiB 安装（`du` 1.7 GB）/826.56 MB Steam 清单 + 3.28 GiB 工坊 + 229/134 MiB 缓存：** 与轻量定位冲突。
- **CEF/AngularJS UI：** `libcef` 259.4 MiB、UI 打开时多进程约 458 MiB（单次观察）；换来编辑器/工坊，但代价过高。
- **Steam 工坊绑定与无创作者分成：** 分发、更新、订阅都依赖 Steam，本地优先产品不能照搬。
- **私有 `scene.pkg`（15 种 PKGV 版本头）：** 无公开规格，不能作为核心兼容路线。
- **Application exe 注入、无备份锁屏/系统壁纸覆盖、虚拟桌面实验特性：** 安全与信任成本高；WE 自己也默认关闭虚拟桌面。

## 如果要接近 WE：最小可行路线

1. **先修可交付性：** 固定版本 mpv 或完成 MF；Authenticode + 版本资源、Developer ID + 公证；发签名 1.0；配置 HTTPS feed + 公钥 + 灰度回滚。投入产出最高，不新增内容功能。
2. **做迁移承接：** scene 静态替代 + 探测报告、WE preview/description/tags 解析、Workshop 目录检测/导入指引、文件夹监听。官方 19 项只有 1 项可导入，这一步能立刻改善。
3. **补本地库可用性：** 分辨率/来源/标签筛选、收藏、播放列表/定时、全局热键；语言先补 6–8 种。
4. **最小作者路径（对齐 F-25/编辑器规划）：** `wpk pack/validate` CLI + video/image/web/shader 向导 + 2–3 个模板/starter pack；不做全场景编辑器。
5. **再评估内容服务：** 静态索引/签名清单/深链导入，不做托管 UGC 商店；等 2–4 验证后再谈。

**不应做：** 完整 3D Scene 运行时、捆绑 CEF、Application exe 注入、Steam 式工坊商店、一次性上 36 种语言、屏保/锁屏/虚拟桌面（除非先有备份与安全框架）。

## 证据边界

- 本轮没有运行任何一方的 exe，也没有做同片源、同分辨率、同电源策略下的帧率/功耗/硬解对比；所有内存与 CPU 都是瞬时观察值，不能当作基准。
- WE 当前以 `-safe -silent -service` 运行，且 UI 未常驻；其渲染与 UI 性能没有在本轮被完整压测。
- 我们的「硬解、循环、跨屏、MF/AVF 恢复」等能力在矩阵中仍有真机验收缺口；源码存在不等于已验收。
- 外部价格、工坊规模、Android 安装量来自 2026-11-05 的公开页面，之后可能变化；SteamSpy 拥有者数是估计值。
- 本文是本地技术对比，不是法律意见；许可与商业条款以仓库 LICENSE 和正式发布材料为准。
