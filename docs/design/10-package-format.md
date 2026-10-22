# 10 壁纸包格式 v0（完整规格）

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

> 机器可读的 schema：[schema/manifest.v0.schema.json](schema/manifest.v0.schema.json)；示例：[schema/example.video.json](schema/example.video.json)、[schema/example.web.json](schema/example.web.json)。

## 10.1 参考格式（导入兼容的依据）
- **Lively**【源码】`Lively.Models/LivelyInfoModel.cs`、`Enums/WallpaperType.cs`：
  - `LivelyInfo.json` 的字段：`AppVersion, Title, Thumbnail, Preview, Desc, Author, License, Contact, Type, FileName, Arguments, IsAbsolutePath, Id, Tags, Version`；
  - `Type` 共 13 种：app、web、webaudio、url、bizhawk、unity、godot、video、gif、unityaudio、videostream、picture、heic；
  - 可调参数放在 `LivelyProperties.json`，控件类型包括 slider、checkbox、dropdown、textbox、color 等（内置 mpv 插件的示例里就有 slider/checkbox/scalerDropdown，见 `Assets/Plugins/Mpv/LivelyProperties.json`）；
  - 属性注入：调用页面全局函数 `livelyPropertyListener(name, value)`（`Lively.Player.WebView2/Form1.cs:539-547`），暂停时调用 `livelyWallpaperPlaybackChanged`。
- **Wallpaper Engine**：
  - `project.json` 必须有 `type`（scene/video/web）、`title`、`file`；可选 `preview`、`workshopid`、`general.properties`【源码】linux-wallpaperengine `ProjectParser.cpp`；【文档】help.wallpaperengine.io；
  - Web 属性注入：`window.wallpaperPropertyListener.applyUserProperties(props)`，每个属性的形式是 `props.key.value`【文档】docs.wallpaperengine.io/en/web/api/propertylistener.html；
  - 音频：`wallpaperRegisterAudioListener`，128 个采样值，每秒约 30 次【文档】；
  - scene 包使用私有格式（`PKGV` 头）。

## 10.2 容器
```
xxx.wpk  = zip（视频文件用 STORE 不压缩，可以直接 mmap 或按偏移读取；其他文件用 DEFLATE）
 ├─ manifest.json           必需，UTF-8，≤ 256 KiB
 ├─ preview.webp|png        必需，≤ 2 MiB，16:9
 ├─ thumb.webp|png          可选，≤ 256 KiB
 ├─ content/…               入口和资源（video: mp4/mov/webm；web: html/js/css/图片/字体；shader: .glsl）
 └─ source/…                可选，原始素材，用于重新转码；分发时可以省略
```
- **导入后一律解压**到 `library/<id>/`，播放时只读这个目录。转码产物放在 `cache/<id>/renditions/`，**不写回包里**，因为它们和用户的显示器、GPU 有关【推断】。

## 10.3 manifest 字段（v0）
| 字段 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `format` | `"wpk"` | ✓ | 固定值 |
| `schema` | 整数 | ✓ | 格式主版本，目前是 0（v0 冻结后改为 1） |
| `minApp` | semver | | 最低应用版本 |
| `id` | UUID | ✓ | 包作者生成；导入时如果重复，就当作同一个壁纸的更新 |
| `version` | 整数 | ✓ | 包内容版本，单调递增 |
| `type` | `video\|web\|shader\|image` | ✓ | v0 **没有** `app`（可执行文件）类型，见 10.6 |
| `entry` | 相对路径 | ✓ | 必须位于 `content/` 之下 |
| `title` / `description` | 多语言对象 `{"en":"…","zh-CN":"…"}` | `title` ✓ | |
| `author` | `{name, url?}` | | |
| `license` | SPDX 标识或 `"proprietary"` | | |
| `tags` | 字符串数组，≤ 16 个 | | |
| `video` | `{loop:bool=true, durationSec, w, h, fps, codec, hasAudio}` | 当 type=video | 导入器根据探测结果填写或校验 |
| `web` | `{permissions:[], fpsCap?, transparent?:false}` | 当 type=web | 权限见 10.6 |
| `shader` | `{dialect:"shadertoy-glsl", passes:[…]}` | 当 type=shader | v0 只支持单 pass |
| `properties` | 数组，见 10.5 | | |
| `perf` | `{maxFps?, minRenderScale?, allowBattery?:true}` | | 用户设置的上限优先于这里 |
| `origin` | `{kind:"native\|lively\|we", ref?}` | | 导入器写入，用于追溯来源 |

**版本策略**
1. `schema` 是主版本，**只在不兼容时 +1**。应用支持读 `[当前-1, 当前]` 两个主版本；读到更新的主版本时拒绝导入，并提示升级应用。
2. 同一主版本内**只增加可选字段**。读取方忽略不认识的字段，但会原样保留（再次导出时不丢失）【推断】。
3. `minApp` 用于 “字段语义已存在、但旧版应用实现有 bug” 的情况，属于软限制，可以由用户强制导入。

## 10.4 校验与安全解压（`internal/wpk`）
Go 的 `archive/zip` 默认**不会**拒绝不安全路径；只有设置 `GODEBUG=zipinsecurepath=0` 时才返回 `ErrInsecurePath`【源码】Go 1.27.3 `archive/zip/reader.go:26-76`。所以必须自己做校验：
| 检查 | 规则 |
|---|---|
| 路径 | 拒绝：绝对路径、`..` 段、反斜杠、盘符、NUL 字符、Windows 保留名（CON、NUL…）、末尾带点或空格；拒绝大小写不敏感下重复的文件名；统一转成 NFC |
| 写入约束 | 解压时用 `os.OpenRoot(dst)` + `Root.Create`，由运行时保证写入不会逃出目标目录【源码】Go `os/root.go:82-117` |
| 类型 | 拒绝符号链接和硬链接（检查 zip 外部属性里的 `S_IFLNK`）；拒绝可执行文件：扩展名在黑名单里（exe/dll/scr/msi/bat/cmd/ps1/vbs/lnk/app/dylib/so/command/sh/pkg），或者文件头是 MZ/ELF/Mach-O 魔数 |
| 规模 | 条目数 ≤ 10,000；解压后总大小 ≤ 4 GiB；单个非视频文件 ≤ 256 MiB；DEFLATE 压缩比 ≤ 200:1（防 zip bomb）；按流式累计计数，**不信任头里声明的大小** |
| manifest | 先用 JSON Schema 校验（注意：schema 里 `entry` 的正则用了前瞻断言，Go 的 RE2 引擎不支持。因此 Go 实现里路径校验一律在代码中完成，schema 里的正则只是给其他工具参考的；schema 文件本身已在 box 上用 python-jsonschema 4.x 校验通过，两个示例合法，越界路径和非法权限会被拒绝【实测】），再做语义校验：entry 文件存在；type 和入口扩展名匹配 |
| 媒体 | 视频先在**单独的转码进程**里探测和转码（02 章），主进程永远不解析不可信的媒体文件。解析器崩溃只会让这次导入失败【推断】 |
| 来源标记 | Windows 上，如果下载来的包带有 Mark-of-the-Web，导入时提示 “来自互联网” |

## 10.5 用户属性
```json
"properties": [
  {"key":"speed","type":"slider","label":{"en":"Speed"},"min":0.25,"max":2,"step":0.05,"default":1},
  {"key":"tint","type":"color","default":"#ffffff"},
  {"key":"mode","type":"select","options":[{"value":"a","label":{"en":"A"}}],"default":"a"},
  {"key":"showClock","type":"bool","default":true},
  {"key":"caption","type":"text","maxLength":200,"default":""}
]
```
- 属性值的注入方式：页面加载后调用 `window.wpk.onProps(obj)`。为了兼容，我们还会同时调用 `livelyPropertyListener(k,v)`（逐个属性）和 `wallpaperPropertyListener.applyUserProperties({k:{value:v}})`，前提是页面里定义了这些函数（导入的 Lively/WE 包靠这个就能直接用）【推断，基于 10.1 的【源码/文档】】。
- 视频和 shader 的属性只允许系统定义的键：`speed`、`fit`（cover/contain/fill），以及 shader 的 uniform。

## 10.6 Web 壁纸的沙箱与权限
**威胁模型**：Web 壁纸包是不可信代码，它会在用户登录期间一直运行。要防的是：外传数据（访问内网、追踪）、挖矿等滥用 CPU/GPU 的行为、诱导点击（钓鱼）、利用浏览器漏洞。

| 控制项 | WebView2（Windows） | WKWebView（macOS） | 依据 |
|---|---|---|---|
| 来源隔离 | `SetVirtualHostNameToFolderMapping("<id>.wpk.invalid", dir, DENY_CORS)`（主机名用保留 TLD `.invalid`，保证不会和真实域名冲突；这是【推断】的命名选择），每个包一个独立的 origin | `WKURLSchemeHandler` 注册自定义 scheme `wpk://<id>/` | 【文档】两边 API |
| 网络 | 默认**禁止**：`AddWebResourceRequestedFilter("*")`，非本 origin 的请求一律返回 403；包声明 `"network"` 权限、并且用户在导入时同意后才放行 | `WKContentRuleList` 拦截 http(s)，规则同左 | 【文档】 |
| 导航 | `NavigationStarting`：不是本 origin 就 Cancel；`NewWindowRequested`：Handled=true（不弹窗） | `decidePolicyForNavigationAction` 返回 cancel；`createWebViewWith` 返回 nil | 【文档】 |
| 设备权限 | `PermissionRequested` 一律 Deny（摄像头、麦克风、定位、通知、剪贴板） | `WKUIDelegate` 的媒体权限请求返回 deny | 【文档】 |
| 下载 | `DownloadStarting`：Cancel | `WKDownload` 一律取消 | 【文档】 |
| 宿主桥接 | `AreHostObjectsAllowed=false`；只用 `ExecuteScript` 单向注入属性；`AreDevToolsEnabled=false`（诊断模式除外）；`AreDefaultContextMenusEnabled=false` | 不注册 `WKScriptMessageHandler`（v0 页面不能调用宿主） | 【文档】 |
| 输入 | 壁纸窗口本身不接收鼠标和键盘（WorkerW 子窗口 / `ignoresMouseEvents`）。v0 **不支持交互式壁纸**，这也顺带消除了钓鱼点击的风险 | 同左 | 【推断】 |
| 音频 | `IsMuted=true` | `mediaTypesRequiringUserActionForPlayback` + 静音 | 【文档】 |
| 资源 | 帧率封顶，由宿主节流 rAF：页面不可见时浏览器本身会节流，可见时我们按 `fpsCap` 注入 `requestAnimationFrame` 包装【推断】；CPU 看门狗：渲染进程持续 60 s 超过 0.5 核就提示 “该壁纸占用过高”，并允许一键降级 | 同左 | 【推断】 |
| 存储 | 每个包独立的 user data folder / `WKWebsiteDataStore.nonPersistent()`（v0 不持久化 localStorage） | 同左 | 【文档】 |

**权限清单**（v0）：`network`（访问外网，需要用户同意，同时显示包里声明的域名列表 `web.networkAllow: ["api.example.com"]`），`audio-reactive`（保留字段，v0 未实现，导入时提示不支持）。其他取值拒绝导入。

**不支持 `app` 类型**：Lively 支持运行任意 exe（Unity/Godot 等）作为壁纸【源码】WallpaperType。这等于让任意代码以用户权限运行，v0 不做；导入 Lively 的 app 类型包时直接提示不支持【推断，产品决策】。

**签名**（v1 之后再考虑）：给包加上作者的 Ed25519 签名（`signature.json`，签名对象是 manifest 加上所有文件哈希的 Merkle 根），用于在线画廊；本地导入不强制要求【推断】。

## 10.7 导入 Lively / Wallpaper Engine
| 来源 | 识别方式 | 映射 | 不支持的情况 |
|---|---|---|---|
| Lively zip 或目录 | 存在 `LivelyInfo.json` | `Title→title.en`，`Desc→description`，`Author→author.name`，`License→license`，`Tags→tags`，`FileName→entry`（复制到 content/），`Type`：video/gif/picture/heic → video 或 image，web → web，url → 拒绝（远程页面不是包）。`LivelyProperties.json` → properties（slider→slider，checkbox→bool，dropdown/scalerDropdown→select，color→color，textbox→text），其余控件类型丢弃并给出警告 | app、unity、godot、bizhawk、unityaudio、videostream、webaudio（音频可视化 v0 不支持）；`IsAbsolutePath=true`，说明是用户自己打开的外部文件，此时直接把那个文件当作素材导入 |
| WE 项目目录 | `project.json` | `type: video` → video（`file`），`type: web` → web（`file` 是 html）；`title`、`preview`；`general.properties`：slider→slider，bool→bool，combo→select，color→color（WE 用 `"r g b"` 0–1 浮点，换算成 hex），textinput→text | `type: scene`（私有引擎，linux-wallpaperengine 的规模能说明工作量）；`application`；需要 audio 的 web |
| 普通视频或图片文件 | 扩展名加文件头探测 | 自动生成 manifest | |

- **网络权限**：导入的 Web 包，我们扫描 html/js 里的 `http(s)://` 字面量，作为建议的 `networkAllow`，让用户确认。默认不授予网络权限，页面可能会因此显示不全，UI 上给出提示【推断】。
- **转码**：导入的视频统一按 06 章 6.4 的规范生成 renditions；macOS 上的 VP9/WebM 走 06 章 6.6 的路径。
- **版权**：只导入用户本地已有的文件。不提供从 Steam Workshop 下载或解密的功能；WE 的 scene 包更不在范围内【推断，非法律意见】。
