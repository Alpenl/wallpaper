# 08 网页与 Shader 壁纸

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

## 8.1 WebView 选择
- MyGo 用的是系统 WebView：WebView2、WKWebView、WebKitGTK 4.1【文档】MyGo 架构文档。macOS 上 hello-world 加 WKWebView 子进程约 62 MB【文档】。
- Lively 同时维护 WebView2 和 CefSharp 两套播放器【源码】`Lively.Player.WebView2`、`Lively.Player.CefSharp`。CEF 设置里有 `mute-audio`、`autoplay-policy=no-user-gesture-required`、`disable-features=HardwareMediaKeyHandling`、`disable-gpu-shader-disk-cache`（`Form1.cs:345-372`）。
- 真机上 WebView2/WKWebView 与 CEF 的对照数据**仍然没有找到**。box 上做了 WebKitGTK 和 Chromium 的参考对比（8.4 节）；真机对比列为 Q-B2。
- **决策（ADR-005）**：用系统 WebView（WebView2/WKWebView），不捆绑 CEF。理由：安装包小，有官方的暂停 API（TrySuspend），系统负责安全更新【推断，加上 8.4 节的内存数据】。

## 8.2 暂停（关键发现）
Lively 的 WebView2 壁纸暂停时做了三件事（`Lively.Player.WebView2/Form1.cs:395-420, 582-600`【源码】）：
1. 对 Chromium 的 **D3D 渲染子进程**调用 `DebugActiveProcess(pid)` 来冻结它，恢复时调用 `DebugActiveProcessStop`。注释说明这两个调用必须在同一个线程上执行，而且 `DebugSetProcessKillOnExit` 默认为 TRUE。
2. `TryPauseMedia()`。
3. 可选地调用页面 JS `livelyWallpaperPlaybackChanged({IsPaused:true})`。

评估【推断】：
- 用调试器冻结进程的副作用：调试器线程如果退出，被冻结的进程会被一起杀掉；还可能和安全软件冲突。
- 改用**官方的 `ICoreWebView2_3::TrySuspend`**。前提是 controller 的 `IsVisible=false`，否则返回 `ERROR_INVALID_STATE`；它是 best-effort，WebView 重新变为可见时会自动恢复，行为类似 Edge 的睡眠标签页【文档】MS Learn ICoreWebView2_3。
- 流程：L1 时先 `IsVisible=false` 再 `TrySuspend`；恢复时 `IsVisible=true`。暂停期间画面是黑的还是保留最后一帧，**需要实测**（B-T2）；如果是黑的，就先在子 HWND 上贴截图再隐藏 WebView【推断】。
- macOS：把 WKWebView 设为 hidden，或从窗口上摘下来，rAF 会节流【推断】。

## 8.3 Shader 壁纸
- 渲染：Windows 用 D3D11 全屏三角形 + flip-model 交换链；macOS 用 `CAMetalLayer`；Linux 用 GL。用户写的 shader 用 GLSL（兼容 Shadertoy 的 `mainImage`）编写，构建时转译（SPIRV-Cross/naga 这类工具；**选型待定**）。
- 性能控制：
  - FPS 上限 15/30/60，默认 30；
  - 渲染分辨率缩放 0.5–1.0，默认 0.75，最后用双线性放大；
  - 每帧 GPU 计时（D3D11 timestamp query / Metal `GPUStartTime`）超过预算（例如 4 ms）就自动降分辨率；
  - 进入 L1 暂停后停止 present，时间轴冻结。
- 参考：OpenWallpaper 用 WASM 加 AOT 编译场景脚本【文档】README。

## 8.4 box 参考实测（8 vCPU Xeon，无 GPU，软件 GL，仅作相对参考）【实测】
| 引擎 / 页面 | 进程数 | CPU 核 | PSS |
|---|---|---|---|
| Chrome 155 headless，WebGL 不限帧 | 14 | 5.159 | 585 MiB |
| Chrome headless，fps=30 | 14 | 2.887 | 569 MiB |
| Chrome headless，静态页 | 14 | 0.017 | 401 MiB |
| WebKitGTK 2 (4.1)，WebGL 不限帧 | 3 | 6.303 | 445 MiB |
| WebKitGTK，fps=30 | 3 | 3.149 | 442 MiB |
| WebKitGTK，静态页 | 3 | 0.000 | 332 MiB |

- 两个引擎的 CPU 数字都由软件光栅主导，不能代表真机 GPU 下的表现。
- 可以确认的结论：WebKit 进程少，内存约省 70–140 MiB；限帧 30 后 CPU 基本减半。
- Chrome 有窗模式在 Xvfb 下 CPU 只有约 0.02，疑似窗口被判定为遮挡而被节流，该数据作废。
- 脚本：bench/webview/run_box_compare.sh 和 wk_host.py。

## 8.5 F-22 / F-26 原生输入（新产品决策）

Shader 仍由 `internal/render/shader` 在独立进程以 D3D11/Metal 渲染，不使用 WebView。包在顶层声明 `permissions: ["audio", "input"]`，每项在导入时默认拒绝；通用设置中的两个总开关默认关闭。侧栏撤销、总开关变化、重新导入都替换旧宿主，旧进程不得沿用授权。预览 helper 永远不采集。

- 音频：Windows WASAPI 默认播放端点 shared-mode loopback，purego COM；macOS ScreenCaptureKit 音频输出，purego objc，要求 macOS 13+。不注册屏幕视频输出或麦克风输出。TCC preflight 为 false 时只能诚实报告“未授予（尚未决定或已拒绝）”，不能区分二者；不自动弹出系统授权请求，需在系统设置授权并重启。Linux 明确 unsupported。
- PCM 只在渲染子进程内存在，折叠为 mono；4096 样本的滚动窗口，Hann-window radix-2 FFT，128 个 20 Hz–min(20 kHz, Nyquist) 的 log-spaced 峰值频带，attack .65/release .2 平滑，另算 RMS。低频频带可能共享 FFT 分辨率限制而为空。结果更新不超过 min(60, 壁纸 FPS) Hz。
- Shader 继续使用已预留的 16 字节槽布局：`iAudioLevel` 为 RMS；`texture(iAudio, vec2(u,0)).r` 从 `clamp(int(u*128),0,127)` 的 cbuffer/Metal 数组取值；RGB 同值。`iMouse.xy` 只在左键按下时更新，z 在按住时正、松开时负，w 仅点击第一帧为正。零点点击使用 0.0001 保留符号。
- 鼠标：Win 每帧 `GetCursorPos`，仅在桌面根窗口命中时读三个鼠标按钮的 `GetAsyncKeyState`；mac NSEvent 全局 mouse-only monitor，CGWindowList 的窗口边界用于指针下遮挡判定。不读取键盘，不安装拦截 hook，不接管焦点，桌面图标点击仍交给系统。
- 映射使用宿主桌面坐标（Win 为物理像素，mac 为逻辑点）和显式 raster 尺寸，原点不乘 DPI；按 viewport 限制输入、按独立/跨屏 canvas 映射，支持负坐标和混合像素密度。Shader y 向上，Web y 向下。
- L1+、锁屏、睡眠、显示器关闭、不可见/全屏均停止采集与转发，清空缓存/鼠标状态；恢复重新打开源。原始全屏/遮挡信号独立于用户的播放暂停偏好，不能被“全屏继续播放”绕过。L4/关闭会终止宿主。
- `type: web` 可以声明相同权限（兼容 `web.permissions`）；仅接收 `window.wpk.onAudio(Float32Array(128))` 和 `window.wpk.onInput({x,y,buttons})`。注入为数值 JSON，不添加网页到宿主的调用桥，网页不能主动申请采集。旧 `audio-reactive` 与外来 WE/Lively 音频 API 未做隐式兼容。

安全边界和限制：进程隔离不等于 Windows/macOS 内核沙箱。频谱仍可能透露系统正在播放的内容；已获 network 权限的网页能发送接收到的派生数据。显示器级可见性/锁屏/睡眠有系统事件与探针延迟，点遮挡采样也存在窗口移动竞态，不能保证零延迟撤回已交给 GPU/网页的数据。多个可见显示器各自拥有采集流，尚未合并为一个共享音频 helper。TCC、默认音频设备变化/蓝牙切换、显示器热插拔、Mission Control/Spaces、全屏切换、混合 DPI 和恢复后的实际节流需要真机验证。采集失败以宿主状态展示；不会静默改用主进程、麦克风或浏览器采集。

API 依据：[WASAPI loopback 初始化](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nf-audioclient-iaudioclient-initialize)、[IAudioCaptureClient GetBuffer](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nf-audioclient-iaudiocaptureclient-getbuffer)、[ScreenCaptureKit capturesAudio](https://developer.apple.com/documentation/screencapturekit/scstreamconfiguration/capturesaudio)。
