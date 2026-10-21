# 11 分发、签名、更新、许可证

> 涉及许可证和专利的结论都**不是法律意见**，上线前必须由律师复核。

## 11.1 Windows 安装形式
| 维度 | 普通安装包（MyGo 内置 NSIS，按用户安装） | MSIX（侧载 / Microsoft Store） | 依据 |
|---|---|---|---|
| 安装位置和权限 | `%LOCALAPPDATA%\Programs\<App>`，不需要管理员 | 系统管理的 WindowsApps 目录，只读 | 【文档】MyGo distribution；MS Learn MSIX |
| 桌面挂载（Progman/WorkerW 子窗口） | 没有限制 | full-trust 桌面应用（`runFullTrust`）调用的是普通 Win32 API，**没有找到文档说明会被限制**；Lively 有 Store 版，可以作为侧面证据（我没有核对它的打包清单） | 【推断】；需要在 MSIX 包里做一次实测（W-T8） |
| 开机自启 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` | `windows.startupTask` 扩展：用户必须先手动启动一次应用才会生效；用户在任务管理器里禁用后，应用**不能用代码重新启用** | 【文档】MS Learn desktop-to-uwp-extensions、StartupTask 类 |
| 数据目录 | 原样 | AppData 的写入被重定向到包私有位置，卸载时一起删除 | 【文档】desktop-to-uwp-behind-the-scenes |
| 子进程 mpv.exe / wp-webhost.exe | 原样 | 包内的 exe 可以作为子进程运行，继承包身份【推断】 | |
| 更新 | MyGo 内置的签名增量更新 | Store 自动更新，或者用 App Installer 文件 | 【文档】 |
| SmartScreen | 依赖签名身份积累信誉，新版本仍可能弹警告 | Store 安装的应用由微软重新签名，**不会出现 SmartScreen 警告** | 【文档】MS Learn smartscreen-reputation |

## 11.2 Windows 代码签名与 SmartScreen
- 2024 年起，EV 证书**不再自动获得 SmartScreen 信誉**，和 OV 证书一样需要靠下载量积累；信誉来自文件哈希和发布者证书两方面，用同一个签名身份持续发布，可以把发布者信誉带到后续版本【文档】MS Learn “Current status of Windows app distribution features”、“SmartScreen reputation”。
- 推荐的签名服务是 Azure **Artifact Signing**（原名 Trusted Signing），约 $9.99/月，不需要硬件令牌，可以接 CI。**地区限制**：组织需要在美国、加拿大、欧盟或英国；个人开发者目前只限美国和加拿大。其他地区只能用 OV 证书【文档】MS Learn code-signing-options。
- 所有 PE 文件都要签名（主程序、wp-webhost、wp-transcode、mpv.exe、FFmpeg dll），并加时间戳【推断】。

## 11.3 macOS
| 维度 | 直接分发（Developer ID） | Mac App Store | 依据 |
|---|---|---|---|
| 签名与公证 | Developer ID 签名 + Hardened Runtime + `notarytool` 公证 + staple；MyGo 的 `mygo build` 支持签名、公证和 dmg（只能在 macOS 主机上执行） | 用 Xcode 提交，App Sandbox 必须开启 | 【文档】MyGo distribution；App Review 2.4.5(i)(ii) |
| 登录项 | `SMAppService.mainApp.register()`（macOS 13+），需要用户批准；崩溃自动拉起可以用 `SMAppService.agent(plistName:)` 注册 KeepAlive 的 LaunchAgent | 只用 `mainApp`，并且在 UI 里由用户主动开启；**不能未经同意自动启动** | 【文档】SMAppService；App Review 2.4.5(iii) |
| 更新 | MyGo 内置更新器，或 Sparkle 2。Sparkle 只有在沙盒应用里才需要 Installer XPC（`SUEnableInstallerLauncherService`）加上 mach-lookup 临时例外；直接分发版不开沙盒，所以不需要 | **只能**通过 App Store 更新 | 【文档】Sparkle sandboxing；App Review 2.4.5(vii) |
| 锁屏动态壁纸（替换 Aerial 文件） | 实验功能，需要管理员权限（macOS 14–15）| **不可行**（沙盒限制，且不能提权，2.4.5(v)） | 【文档】LiveDesk README；App Review |
| 下载额外代码 | 可以 | 不能下载或安装 “改变应用功能的额外代码或资源”（2.4.5(iv)）。Web 壁纸包里的 JS 是否算在内，**尚不明确**：壁纸内容属于用户数据，并且运行在 WebKit 沙盒里，倾向于认为可以接受，但存在审核风险 | 【文档】App Review；【推断】Q-D3 |
| 素材访问 | 任意路径 | 通过打开面板选择，或者使用 security-scoped bookmark；导入时把文件复制进容器，之后不再依赖原路径 | 【文档】App Sandbox |
| 转码 helper | 普通子进程 | 必须嵌在 bundle 里，带 `com.apple.security.inherit` 继承沙盒 | 【文档】App Sandbox（嵌入的 helper 工具） |
| FFmpeg/libavformat（用于 WebM 解复用） | 动态链接的 dylib，用同一个 Team ID 签名 | 同左；整个应用必须是自包含的单一 bundle（2.4.5(ii)） | 【文档】 |

**MVP 决策（ADR-006）**：macOS 先做直接分发版（功能完整、更新快）；MAS 版作为第二阶段，砍掉锁屏和 KeepAlive，Web 壁纸默认禁止网络权限。

## 11.4 mpv / libmpv 许可证
- 默认是 GPLv2+。用 `-Dgpl=false` 构建可以排除只有 GPL 授权的文件，结果为 LGPLv2.1+；但这个开关 “本身不构成 LGPL 授权”，需要自己核对每个文件的授权。LGPL 模式会禁用 X11 VO、vdpau 等，而且官方**不推荐用 LGPL 模式构建 CLI**【文档】mpv Copyright。
- 链接的 FFmpeg 如果是 GPL 构建，最终产物就是 GPL。
- **Windows MVP 的决定（ADR-004）**：mpv.exe 作为**独立的 GPL 程序**随包分发，只通过命令行参数和 JSON IPC 与主程序通信，不链接到主程序里。
  - 依据：FSF GPL FAQ 认为，通过管道、套接字、命令行参数通信，通常属于独立程序（聚合）；但如果交换的是复杂的内部数据结构，就可能不算【文档】gpl-faq MereAggregation。Lively 采用的也是这种做法【源码】。
  - 义务：提供 mpv 及其依赖（包括 FFmpeg）的对应源码或书面要约，附上许可证文本，在 About 页面里注明。
  - 备选：Media Foundation 播放器（Wallpaper Engine 的路线，它的文档说出于法律原因不能捆绑 LAV【文档】），或者 LGPL 构建的 libmpv。
- macOS 用 AVFoundation，不涉及 mpv 的许可证问题。

## 11.5 FFmpeg【文档】https://ffmpeg.org/legal.html
- FFmpeg 本身是 LGPL 2.1+。官方合规清单的要点：
  - 不加 `--enable-gpl` 或 `--enable-nonfree`；
  - 动态链接；
  - 附上源码和 configure 命令行；
  - 在 About 页面和 EULA 里注明；
  - 不能把 dll 改成看不出来源的名字；
  - **不能用 libx264 这类 GPL 库**。
- 所以产品内的**编码**一律用系统编码器：Windows Media Foundation、macOS VideoToolbox（06 章）。

## 11.6 专利（非法律意见）
- FFmpeg 官方说商业产品 “might have a problem”【文档】。
- Via LA AVC：每年 0–10 万件免费，超过后每件 $0.20，超过 500 万件每件 $0.10，设有封顶；并入 PC 操作系统的产品单独计算【文档】via-la.com。
- HEVC Advance（2016 年的条款）：售后下载、并且完全在通用 CPU 上做软件解码的应用层软件不收费（有附加条件）【文档】accessadvance.com。调用 GPU 硬解是否也在免费范围内，**条款里没有说明**【推断】，列为 Q-L2，问律师。
- 降低风险的做法【推断】：Windows 优先用系统组件解码（MF / 驱动的 d3d11va，HEVC 需要用户自己安装 HEVC 扩展）；macOS 只用 VideoToolbox。我们自己不分发 HEVC 编码器，导入时的转码也只调用系统编码器。

## 11.7 更新
- **应用**：Windows 普通安装包和 macOS 直接分发版都用 MyGo 内置的签名增量更新；Store 版和 MAS 版只走商店渠道。
- **mpv、FFmpeg 的二进制**和应用一起发版（固定版本，便于对应源码要约）。
- **壁纸内容**和应用分开更新；转码产物带规范版本号（06 章），规范升级后在 L3/L4 空闲时段后台重新转码。
