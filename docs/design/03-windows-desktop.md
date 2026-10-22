# 03 Windows 桌面集成

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

## 3.1 挂载流程（参照 Lively `Core/WinDesktopCore.cs` 约 150–230 行【源码】）
1. `progman = FindWindow("Progman", NULL)`
2. `SendMessageTimeout(progman, 0x052C, 0xD, 0x1, SMTO_NORMAL, 1000, &r)`。这是未公开文档的消息，作用是让 Progman 生成 WorkerW。
3. 判断 `GetWindowLongPtr(progman, GWL_EXSTYLE) & WS_EX_NOREDIRECTIONBITMAP`：
   - 有这个样式（24H2 起）：`workerw = FindWindowEx(progman, 0, "WorkerW", 0)`；
   - 没有（旧版本）：`EnumWindows` 找到含 `SHELLDLL_DefView` 的顶层窗口，再取它**后面的** `WorkerW`。
4. 建一个**自有子 HWND**（`WS_CHILD|WS_VISIBLE|WS_CLIPCHILDREN`），按显示器矩形摆放（先用 `MapWindowPoints` 把屏幕坐标换成父窗口的客户区坐标）：
   - **24H2 起（raised desktop）**：父窗口是 **Progman**，不是 WorkerW。子窗口加 `WS_EX_LAYERED` 并设 `SetLayeredWindowAttributes(alpha=255)`，然后 `SetWindowPos(child, hWndInsertAfter=DefView)`，让它紧贴在 DefView 下面；再确认 WorkerW 仍是 Progman 的最后一个子窗口，否则把 WorkerW 用 `HWND_BOTTOM` 放到最底【源码】Lively `WinDesktopCore.cs:1041-1080`（`TryAttachToDesktop`、`EnsureWorkerWZOrder`），以及 `:160-173` 引用的微软说明 “create its own WS_EX_LAYERED child HWND that is z-ordered under the DefView window but above the WorkerW window”。
   - **旧版**：`SetParent(child, workerw)`。
   - **更正**：第 1–9 轮的文档在这里写的是一律 `SetParent(child, workerw)`，第 12 轮核对源码后改正。
5. 把 mpv 的 `--wid` 或 D3D11 交换链交给这个子 HWND。
- 原型：[prototype/proto_windows.go](prototype/proto_windows.go) 实现了上面两条分支，外加 Job Object、命名管道 IPC、TaskbarCreated 重挂、健康检查（父窗口是否正确）、前台全屏暂停。windows/amd64 和 arm64 都用 `CGO_ENABLED=0` 交叉编译通过【实测】，**还没在真机上跑过**。旧的骨架代码 `docs/research/wpproto_src/` 保留未删。

## 3.2 版本差异
| 版本 | 结构 | 依据 |
|---|---|---|
| Win10、Win11 ≤23H2 | WorkerW 是顶层窗口 | 【源码】Lively 注释里的 Spy++ 结构 |
| 24H2（26100） | Progman 带 NOREDIRECTIONBITMAP，DefView 是 layered 子窗口，WorkerW 是 Progman 的子窗口。改动原因是支持 HDR 壁纸 | 【源码】注释；lively #2746 作者回复 |
| 24H2 早期正式版 | 必须开桌面图标，否则失效；KB5050009（26100.2894）修复 | 【issue】lively #2374、discussion #2464 |
| 25H2（26200） | 25H2 是在 24H2 同一代码库上的启用包，系统文件相同，**没有证据表明桌面结构有变化**。#3195 报告了刷新后壁纸消失（open，维护者未回复）；相近的 #3078（25H2，睡眠后）里维护者认为通常是浏览器或播放器崩溃（与显示驱动有关） | 【文档】KB5054156；【issue】lively #3195、#3078 |

## 3.3 故障与恢复
| 故障 | 依据 | 对策 |
|---|---|---|
| Explorer 反复重载桌面，WorkerW 被销毁，壁纸不断重建 | lively #2777、#2746 | 用 WinEventHook 监听 `EVENT_OBJECT_DESTROY`（只盯 workerw），并注册 `RegisterWindowMessage("TaskbarCreated")`；重挂用指数退避（1、2、4、8 s，最多 5 次），全失败就降级为系统静态壁纸（截一帧后设成系统壁纸）【推断】 |
| 第三方桌面软件改动层级（360 桌面助手） | lively #3103 | 自检：`GetParent(child)` 等于预期父窗口（24H2 上是 Progman，旧版是 WorkerW），并检查 z 序（24H2：child 在 DefView 之下、WorkerW 之上）；不满足就提示可能有冲突的软件 |
| 24H2 下 mpv 边框和对齐问题 | lively #2415；`--no-border`【源码】 | 用自建子 HWND，mpv 只在 `--wid` 里绘制 |
| WebView2 作为 WorkerW 子窗口时 DPI 不对 | 【源码注释】`WallpaperPluginFactory.cs:219` | 见 08 章：用 composition 模式或手动设置缩放 |
| “隐藏桌面图标” | DefView 发 `WM_COMMAND 0x7402`【源码】`DesktopUtil.cs:20` | 只作为可选功能，并且必须能恢复 |

| 25H2 睡眠或驱动重置后壁纸消失 | lively #3078、#3195 | 不是挂载问题，而是渲染器死掉或卡住。用 02 章 2.4 的健康检查（mpv 的 time-pos 停止前进、WebView2 ProcessFailed）加唤醒后自检来恢复 |
| GPU 驱动 TDR（设备丢失） | mpv #3469、#7190；MS Learn device-lost | mpv：检测到卡住后重启进程；自绘 shader：处理 `DXGI_ERROR_DEVICE_REMOVED`，重建设备；WebView2 GPU 进程会自动恢复（02 章 2.4） |

## 3.4 不在范围内 / 未验证
- 在 WorkerW 下面直接使用 DirectComposition visual：**没找到开源实现**，列为原型项 W-T2b。
- Windows 虚拟桌面不影响 WorkerW（壁纸本来就全局），但每个虚拟桌面单独一张壁纸需要 IVirtualDesktopManager（未公开的 COM 接口），**MVP 不做**。

链接：https://github.com/rocksdanister/lively/issues/2374 · /2746 · /2777 · /3103 · /3195 · /3078 · /2415 · https://support.microsoft.com/help/5054156
