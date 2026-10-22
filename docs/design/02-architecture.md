# 02 总体架构

## 当前架构（以生产源码为准）

- **产品界面**：[`internal/ui`](../../internal/ui/) 使用原生 MyGo，包含设置窗口与托盘；产品 UI 不使用 React 或 WebView。WebView2 / WKWebView 仅用于隔离的网页壁纸宿主。
- **进程入口**：[`cmd/wallpaperd`](../../cmd/wallpaperd/) 是组合根。MF、Web、Shader 分别以同一 executable 的 `-mfhost`、`-webhost`、`-shaderhost` 模式启动子进程，通过 [`internal/ipc`](../../internal/ipc/) 通信；媒体探测、原生转码和 Shader 预览使用 `-helper probe/transcode/shader-preview`。不是另发 `wp-webhost` 等二进制。mpv 是外部子进程；macOS AVFoundation 视频播放当前仍在主进程，不能概括为全部 renderer 都已隔离。不可信网页/Shader 的执行在 helper 内，不能移入主进程。
- **编排与依赖**：UI 使用 app 的控制器/视图和 config 类型；[`internal/app`](../../internal/app/) 经 `desktop.Host`、`render.Renderer` 和注入工厂控制桌面与渲染，并直接使用 config、importer、media、transcode、panorama、supervisor、visibility、telemetry 等实现包，不能称为“只依赖接口”。[`internal/visibility`](../../internal/visibility/) 管理暂停/电源状态，supervisor 管理恢复。
- **现存具体边界**：UI 的 mpv 路径校验仍直接依赖 `render/mpv`；Shader 的 macOS 宿主复用 `desktop/mac` 窗口 helper，importer 复用 Shader 校验，config 依赖 wpk 类型但不反向依赖 app/UI。FFI/`unsafe` 分布在 desktop、render、reactive、transcode/nativeav 与 platform 等包，并非只在 platform。上述事实不是新增反向依赖的许可；平台运行与支持限制见[实现矩阵](../IMPLEMENTATION_STATUS.md)。

## 历史设计正文（冻结参考）

以下保留原轮次方案与证据，包括旧 WebView UI、独立 helper 二进制及依赖约束；与上方当前实现不一致时，以生产源码和实现矩阵为准，不作为当前运行说明。

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

> 本章是实现的规格：进程模型、IPC 协议、崩溃恢复、Go 包结构、接口签名。暂停状态机见 [07](07-pause-power-state-machine.md)；配置、日志、遥测见 [16](16-config-logging-telemetry.md)。

## 2.1 关键决策：MyGo 只负责控制面（ADR-001）
- 【源码】MyGo 的 Windows 窗口用 DirectComposition 绘制：`internal/windows/compositor.go:32,122,321`（`DCompositionCreateDevice`、`CreateTargetForHwnd(hwnd, topmost=1)`），窗口带 `WS_EX_NOREDIRECTIONBITMAP`（`win32.go:316`）。`WindowOptions.Parent` 是 owned 窗口，不是 WS_CHILD（`window.go`）。
- 【推断】把 DComp 顶层窗口 SetParent 成子窗口，行为没有文档保证。所以**壁纸表面不使用 MyGo 窗口**，由平台层用原生 API 自建。
- MyGo 负责：托盘和菜单；设置窗口（按需创建，关闭即销毁 WebView）；`Screen.Displays`/`OnDisplaysChanged`；`Power.OnSuspend/OnResume/OnLockScreen/OnUnlockScreen/IsOnBattery`（【源码】`modules.go:210-306`、`power.go`）；单实例；自动更新（11 章）。
- MyGo 没有覆盖、由平台层补的：刷新率和 HDR、电池变化事件、远程会话、全屏和遮挡检测、显示器稳定 ID。

## 2.2 进程模型
| 进程 | Windows | macOS | 崩溃影响 |
|---|---|---|---|
| `wallpaperd`（控制进程，Go + MyGo，常驻，无可见窗口） | 平台宿主（建子 HWND 挂到 Progman/WorkerW 下，见 03 章；可见性探针；状态机）在同一进程 | 同一进程：NSWindow + AVPlayerLayer + WKWebView 都在进程内，主线程调用 | 所有壁纸消失，系统壁纸露出来（2.5） |
| 视频播放进程 ×N | `mpv.exe --wid=<子HWND>`，每块显示器一个 | 无（AVFoundation 进程内） | 只影响该显示器 |
| Web 宿主进程 ×N | `wp-webhost.exe`：一个 WebView2 controller 挂在我们的子 HWND 上；WebView2 自己再派生 browser/renderer/GPU 进程【文档】WebView2 process model | 无单独宿主；WKWebView 的 WebContent 进程由系统管理【文档】 | 只影响该显示器 |
| 转码进程（按需） | `wp-transcode.exe`（MF 编码）| `wp-transcode` helper（VideoToolbox），MAS 版需要继承沙盒 | 导入失败，可重试 |

选这个模型的理由：
- mpv 作为子进程：崩溃不会带垮主进程，不需要 cgo，GPL 边界清晰（11 章）。Lively 也是这样用的，通过 mpv JSON IPC 控制【源码】`VideoMpvPlayer.cs`。
- Windows 的 Web 宿主单独成进程：WebView2 的 COM 回调需要 STA 消息循环；隔离后某个页面卡死只影响自己。Lively 也用独立的 `Lively.Player.WebView2.exe`【源码】。
- macOS 全部放在进程内：AVFoundation 和 WebKit 本来就是系统组件，WebKit 的渲染已经在系统进程里隔离；用 purego 调用即可，交叉编译已通过（04 章）。
- **不再单独设 watchdog 进程**（这一点修正了第 1–6 轮的设计）。Windows 用 Job Object 加 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`：控制进程一死，所有子进程随即被内核结束，不会留下孤儿进程【文档】Old New Thing 2013-12-09 / MS Learn Job Objects。要注意 Job 句柄不能被子进程继承。

## 2.3 IPC 协议 v1
**传输层**
- Windows：命名管道 `\\.\pipe\wpd-<会话ID>-<16 字节随机hex>`。DACL 只允许当前用户 SID；创建时带 `FILE_FLAG_FIRST_PIPE_INSTANCE`，防止被别的进程抢注；管道名通过命令行参数传给子进程【文档/推断】。
- macOS：目前不需要（全部进程内）。转码 helper 用 stdin/stdout 管道，格式相同。
- mpv：**直接使用 mpv 自己的 JSON IPC**（`--input-ipc-server`），不套我们的协议【文档】mpv manual “JSON IPC”。

**帧格式**：NDJSON，即每条消息一行 UTF-8 JSON，以 `\n` 结尾；单条上限 1 MiB，超过就断开连接（防止内存被撑爆）。选它的理由是和 mpv 一致，便于调试，消息量很低（每秒不到 1 条）【推断】。

**信封**
```json
{"v":1,"t":"req","id":17,"m":"setPause","p":{"level":2}}
{"v":1,"t":"res","id":17,"ok":true,"r":{}}
{"v":1,"t":"res","id":18,"ok":false,"err":{"code":"E_STATE","msg":"not loaded"}}
{"v":1,"t":"evt","m":"firstFrame","p":{"ms":412}}
```
| 字段 | 含义 |
|---|---|
| `v` | 协议主版本，不一致就断开连接，由 supervisor 报 “版本不匹配”（安装损坏时会出现） |
| `t` | `req` / `res` / `evt` |
| `id` | 请求方单调递增的 uint32；`res` 原样带回 |
| `m` / `p` | 方法名和参数；**未知字段一律忽略**，以便向前兼容 |

**方法（控制进程发给宿主）**
| 方法 | 参数 | 说明 |
|---|---|---|
| `hello` | `{proto:1, app:"1.2.3"}` → 返回 `{pid, caps:[...]}` | 握手，必须是第一条消息，5 s 内没完成就杀掉进程 |
| `load` | `{entry, props, fpsCap, renderScale, bounds, dpi}` | 加载壁纸包里的入口文件，路径限制在包目录内（10 章） |
| `setPause` | `{level:0..3}` | 对应 07 章的 L0–L3；L4 由控制进程直接结束宿主 |
| `setProps` | `{key:value...}` | 用户属性，注入页面（10.5） |
| `setBounds` | `{x,y,w,h,dpi}` | 物理像素 |
| `snapshot` | `{path}` | 截一帧 PNG，供 L4 和崩溃兜底使用 |
| `ping` | `{}` | 心跳 |
| `shutdown` | `{}` | 宿主 2 s 内没退出就 TerminateProcess |

**事件（宿主发给控制进程）**：`ready`、`firstFrame{ms}`、`error{code,msg,fatal}`、`procFailed{kind}`（WebView2 ProcessFailed 的透传）、`stats{...}`（只在诊断模式下发送，每 10 s 一次）。

**错误码**：`E_PROTO`、`E_STATE`、`E_PATH`（路径越界）、`E_LOAD`、`E_GPU`、`E_TIMEOUT`、`E_INTERNAL`。

> 第 19 轮：原型的 Media Foundation 渲染子进程（`wpproto -mfhost`）已按本节实现（`prototype/ipc.go`、`mfhost_windows.go`），并在 Wine 中跑通 hello/load/setPause/shutdown 和 ready/firstFrame/stats/error。实现中补充了两点：`load` 增加 `wid`（目标子 HWND）和 `paused`（以暂停状态启动，用于 Recovering 后按级别就位）；`hello` 的返回增加 `inJob`。原型的管道还没有设置显式 DACL。

## 2.4 健康检查与崩溃恢复（Supervisor）
| 对象 | 检测 | 动作 | 依据 |
|---|---|---|---|
| mpv 进程退出 | 进程句柄被 signal，或者 IPC EOF | 按退避规则重启，用上次保存的 `time-pos` 续播 | 【推断】 |
| mpv **卡住不退出**（例如 D3D11 设备丢失后画面停住） | 处于 L0 时每 5 s 读一次 `time-pos`，连续 2 次没前进；或者 mpv 日志里出现 `device was removed` / `0x887A0005/0006` | 杀掉 mpv 再重启 | 【issue】mpv #3469（DEVICE_HUNG 后视频停住）、#7190（resize 时 DEVICE_REMOVED）|
| Web 宿主 | `ping` 每 2 s 一次，连续 3 次没回复；或者进程退出 | 重启宿主 | 【推断】 |
| WebView2 `GpuProcessExited` | ProcessFailed 事件 | **不处理**，WebView2 会自动重建 GPU 进程（画面可能闪一下），只计数 | 【文档】WebView2 process-related events |
| WebView2 `RenderProcessExited` | 同上 | `Reload()` | 【文档】同上，官方示例 |
| WebView2 `BrowserProcessExited` / `RenderProcessUnresponsive` | 同上 | 重建 WebView（等同于重启宿主） | 【文档】同上 |
| WKWebView WebContent 进程终止 | `webViewWebContentProcessDidTerminate:` | `reload` | 【文档】WKNavigationDelegate |
| 自绘 Shader 渲染器 | `Present`/`ResizeBuffers` 返回 `DXGI_ERROR_DEVICE_REMOVED/RESET` | 释放全部 D3D 资源，重建设备和交换链；用 `GetDeviceRemovedReason` 记录原因 | 【文档】MS Learn “Handle device removed scenarios in Direct3D 11” |
| WorkerW 被销毁 / Explorer 重启 | WinEventHook `EVENT_OBJECT_DESTROY`，以及 `TaskbarCreated` 消息 | 重新挂载（03 章） | 【源码】Lively |
| 睡眠唤醒 | `Power.OnResume` | 所有显示器执行一次健康检查；可选 “唤醒后重启壁纸”（Lively #3078 维护者给的绕过方法） | 【issue】|

**退避规则**：重启间隔依次为 0.5、1、2、4、8、16、32、60 s（封顶）。10 分钟内崩溃 5 次，该显示器进入 `Failed`：显示最近一次的截图静态壁纸，只通知用户一次；用户手动重试或换壁纸后清零【推断】。

## 2.5 控制进程自身崩溃
1. **不留孤儿**：Windows 靠 Job Object 自动结束所有子进程；macOS 的东西都在进程内，进程一死窗口就没了。
2. **画面兜底**：每次应用壁纸，就把它的首帧截图**同时设为系统壁纸**（Windows `IDesktopWallpaper::SetWallpaper` 按显示器设；macOS `NSWorkspace.setDesktopImageURL:forScreen:`）。这样崩溃后露出来的系统壁纸和动态壁纸几乎一样【文档 API / 推断 设计】。退出时也不必恢复用户原来的壁纸（原来的壁纸会记录下来，卸载时恢复）。
3. **自动拉起**：
   - Windows：`RegisterApplicationRestart`，交给 WER 重启（进程至少运行满 60 s 才会生效；只对 WER 能识别的崩溃或无响应有效，被 TerminateProcess 结束不会触发【文档/推断】）。注意：Go 运行时自己处理 panic 并退出，这种情况 WER 不一定会当作崩溃，需要 W-T 实测；如果不行，就在 `SetCrashOutput` 之后自行调用重启，或者用一个计划任务兜底【推断】。
   - macOS 直接分发版：通过 `SMAppService.agent(plistName:)` 注册一个带 `KeepAlive` 的 LaunchAgent【文档】SMAppService。
   - macOS MAS 版：只用 `SMAppService.mainApp` 作为登录项，不做 KeepAlive（审核指南 2.4.5(iii) 要求必须征得用户同意）【文档】。
4. **启动自检**：如果 `state.json` 里的 `cleanShutdown=false`，并且 2 分钟内已经发生过 3 次 → 进入**安全模式**：不加载壁纸，只显示托盘提示。这样可以避免某个坏的壁纸包导致无限崩溃循环【推断】。

## 2.6 Go 包结构
```
cmd/
  wallpaperd/          main：MyGo App、托盘、设置 UI 入口
  wp-webhost/          Windows WebView2 宿主（GOOS=windows）
  wp-transcode/        导入转码 helper（MF / VideoToolbox）
internal/
  app/                 编排：Library × Displays → Assignments，驱动 supervisor
  desktop/             DesktopHost 接口
    win/ mac/ linux/   各平台实现（build tag）；linux 只做 X11（xgb）和 D-Bus 代理（05 章）
  visibility/          Probe 接口、各平台探针、状态机（07 章）
  render/              Renderer 接口
    mpv/               Windows 视频：进程 + mpv JSON IPC 客户端
    avf/               macOS 视频：AVQueuePlayer/AVPlayerLooper（purego/objc）
    webhost/           Windows Web：IPC 客户端，对接 wp-webhost
    wk/                macOS Web：WKWebView 进程内
    shader/            D3D11 / Metal
    image/             静态图（L4 的兜底也用它）
  supervisor/          进程生命周期、退避、Job Object、健康检查
  ipc/                 NDJSON 编解码、命名管道
  wpk/                 壁纸包：解析、校验、安全解压（10 章）
  importer/lively/ importer/we/
  transcode/           转码规范（06 章）+ helper 调度
  config/              配置、库索引、运行状态，原子写入（16 章）
  telemetry/           日志、本地指标、可选上报（16 章）
  platform/win/ platform/mac/   syscall/purego 绑定（只放 FFI，不放逻辑）
```
规则：`internal/app` 只依赖接口包；平台实现放在 `desktop/win` 这类目录，靠 build tag 隔离；`platform/*` 是唯一允许出现 `unsafe` 的地方【推断】。

## 2.7 接口签名
```go
package desktop
type DisplayID string // 稳定 ID：Win=DisplayConfig 的 monitorDevicePath 哈希；mac=CGDisplayCreateUUIDFromDisplayID
type Display struct {
    ID DisplayID; Bounds image.Rectangle /*物理像素, 虚拟屏坐标*/; Scale float64
    RefreshHz float64; HDR bool; Primary bool; Name string
}
type Surface interface {
    Native() uintptr                 // Win: 子 HWND; mac: NSView*; GNOME Wayland: 0（外部进程自管）
    SetBounds(image.Rectangle) error
    Close() error
}
type Caps struct { Video, Web, Shader, PerDisplay, ExternalSurfaceOnly bool }
type EventKind int // DisplaysChanged, ShellRestarted, SpaceChanged, SessionLocked, ...
type Event struct { Kind EventKind; Display DisplayID }
type Host interface {
    Displays() []Display
    CreateSurface(ctx context.Context, id DisplayID) (Surface, error)
    Health(id DisplayID) error       // nil=正常; ErrDetached / ErrZOrder ...
    Events() <-chan Event
    Caps() Caps
    SetSystemWallpaper(id DisplayID, pngPath string) error // 2.5 兜底
}

package render
type Kind uint8 // Video, Web, Shader, Image
type Level uint8 // L0 运行 … L4 销毁（07 章）
type Item struct { PkgDir, Entry string; Kind Kind; Props map[string]any; Perf wpk.Perf }
type Stats struct { HWDec string; Dropped uint64; FPS float64; LastErr string }
type Renderer interface {
    Load(ctx context.Context, it Item, s desktop.Surface) error
    SetLevel(l Level) error          // 幂等
    SetFPSCap(fps int) error
    SetRenderScale(f float64) error
    SetProps(p map[string]any) error
    Snapshot(path string) error
    Stats() Stats
    Done() <-chan error              // 渲染器死亡时发送原因，supervisor 订阅
    Close() error
}
type Factory interface { New(k Kind) (Renderer, error) }

package visibility
type Reason uint32 // 位标志，定义见 07 章
type Update struct { Display desktop.DisplayID /* ""=全局 */; Reason Reason; Active bool; At time.Time }
type Probe interface { Name() string; Run(ctx context.Context, out chan<- Update) error }
type Machine interface { // 07 章
    Feed(Update); Tick(now time.Time)
    Level(id desktop.DisplayID) render.Level
    Changes() <-chan LevelChange
}
```

## 2.8 线程
- macOS 的 AppKit、AVFoundation、WebKit 调用必须在主线程。MyGo 提供 `RunOnMain`【源码】`loop.go:86`。CGWindowList 轮询放在后台 goroutine 里做，结果再回到主线程【源码】Wallnetic 的做法。
- purego 回调在启动时一次性注册（每个进程大约有 2000 个上限，而且不释放）【文档】MyGo 架构文档。
- Windows：WinEventHook 需要所在线程有消息循环，所以在一条 `LockOSThread` 的专用 goroutine 里跑 GetMessage 循环【文档】SetWinEventHook。

### F-22 / F-26 输入进程边界

原生 shaderhost 和已授权的 webhost 在各自宿主内拥有 `internal/reactive.Session`。主进程只保存包授权、总开关及可见性策略，经现有 IPC 发出阻断命令；不接收 PCM。音频 FFT 与鼠标坐标转换都在宿主中执行。暂停、锁屏/睡眠和原始全屏信号可独立于播放偏好撤销输入；撤销授权会重建宿主。完整安全模型及其事件延迟/TCC 限制见 [8.5](08-web-shader.md#85-f-22--f-26-原生输入新产品决策)。
