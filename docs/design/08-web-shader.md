# 08 网页与 Shader 壁纸

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
