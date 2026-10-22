# Wallpaper

[工程规则](docs/ENGINEERING.md)

> **实现基线**：主分支 `master` 的 `2710033ef169852bd7c4a1b5c063d29f3f3e89fd`。`ui` 分支只提供 [React 设计稿](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web)，不作为应用代码或产品运行入口；正式界面只能使用原生 MyGo。

跨平台动态壁纸桌面应用：Go + MyGo 原生设置界面，Windows 使用 mpv/可选 Media Foundation，macOS 使用 AVFoundation，Linux 提供有限的 X11 兼容实现。发布版本由 release tag 注入；普通构建为 `dev`，不参与自动更新排序。发布范围、安装依赖和验证限制见 [版本说明](docs/releases/README.md)。

本轮按 UI 设计稿将原生设置重组为七页，补充每屏播放/暂停、原生空间跨屏、UTC 周暂停统计、macOS 原生隔离转码、应用规则、批量导入、元数据检索、存储管理和双语控件。研究文档已区分当前实现、验收目标和历史证据。**源码完成、自动化通过、真机验收和公开发布是四件不同的事。** 当前范围/缺口见 [实现验收矩阵](docs/IMPLEMENTATION_STATUS.md)，本轮实跑结果见 [验证记录](docs/VALIDATION.md)。

## 从这里开始

- [完整文档导航](docs/README.md)：当前实现、设计、决策、测试与历史研究的入口。
- [本轮原生 UI 截图](docs/screenshots/native-current/README.md)：七页示例及中/英文、浅/深主题，注明软件绘制和模拟数据边界。
- [原生跨屏说明](docs/SPANNING.md)、[本轮研究补充](docs/research/NATIVE_MVP_ADDENDUM.md)：实现、替代方案和剩余技术门槛。
- [七页 UI / F-01…F-26 对照](docs/IMPLEMENTATION_STATUS.md)：壁纸库、显示器、播放与性能、暂停与电源、通用、版本管理、关于与诊断。
- [原始设计与 ADR](docs/design/README.md)、[功能规格与 AC](docs/design/17-features-mvp.md)、[待决策清单](docs/design/DECISIONS_PENDING.md)。
- [前期调研索引](docs/research/README.md)：市场、技术、可行性报告保留原文并注明历史范围。
- [改造前 0.2.1 实现记录](docs/history/IMPLEMENTATION_0.2.1.md)：旧测试、旧十页 UI 与截图说明，仅作历史参考。

## 原生桌面应用：唯一产品运行入口

[`go.mod`](go.mod) 声明 Go 1.27.3。发布构建使用 `CGO_ENABLED=0`；race 测试是独立验证步骤，需要相应 cgo/race 工具链。无法获取指定工具链时应报告受阻，不能把其他版本的诊断当作原配置通过。

```sh
go test ./...
go test -race ./...
go vet -unsafeptr=false ./...
CGO_ENABLED=0 go build ./cmd/wallpaperd
# 在有图形桌面和运行依赖的系统上启动：
go run ./cmd/wallpaperd
```

运行注意：

- Windows 默认视频后端需要 mpv，可选系统 Media Foundation；Linux 视频后端使用 mpv；macOS 播放使用系统 AVFoundation。
- mpv 查找顺序：`config.render.mpvPath` → 可执行目录下 `mpv/mpv(.exe)` → 同目录 `mpv(.exe)` → `PATH`。
- 转码：macOS 默认使用应用自身的隔离 AVFoundation helper，无需安装 ffmpeg；用户可显式选择带 `h264_videotoolbox` 的 ffmpeg。Windows 的可选转码功能使用用户安装且带 `h264_mf` 的 ffmpeg，不回退到软件编码器；Linux 保留外部 ffmpeg 软件兼容路线。不暗中下载或捆绑编码器。
- 转码目标可选主屏当前模式像素尺寸或 1080p/1440p/2160p；严格路线输出偶数尺寸、保比例、不放大小图、H.264 30fps 无音轨，并验证产物后才发布。共享转码产物之外，独立显示器在有效 fill 且静音时，按自身 backing 像素懒生成 center-cover rendition；素材修订、目标尺寸、裁切与变换版本相同则复用同一 key，缓存位于 `cache/<id>/renditions/<key>/`，含 `video.mp4` 与校验后发布的 manifest。无可用缓存时保留共享入口或原文件回退；混合 DPI、热插拔与暂停换源仍需真机验收。`avconvert` 仅为能力不足的有限回退，不保证固定帧率、目标尺寸或去音轨，不支持 GIF。详见 [转码路线与限制](internal/transcode/README.md)。
- `WPAPP_HOME=dir` 将配置、库、缓存和日志重定向到该目录，适合便携运行/隔离测试。一般导入默认只加入库，不自动覆盖桌面；“导入后动作”可配置。
- 正常启动是托盘+设置窗口，有单实例处理。`-headless` 用于测试，不等于平台支持全部桌面行为，macOS 的 AppKit 路径需要正常主线程循环。
- `-export-diagnostics` 导出本地诊断；`-update-apply` / `-update-rollback` 是更新/回滚入口；`-helper`、`-mfhost` 属于内部子进程参数。

### React 仅作 UI 设计参考

[UI 分支的原有 React 设计稿](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web) 只用于七页布局、视觉与信息架构参考。主分支工作区没有 `web/` 目录，不复制 React 代码、不接 Go 后端、不嵌入原生应用。**应用只能使用原生 MyGo 界面**；功能补全、持久化和验收均针对原生代码。

## 构建、检查与打包

```sh
bash scripts/check.sh                  # 原生格式、race、vet、无 cgo 构建、文档检查
CROSS_BUILD=1 bash scripts/check.sh    # 再执行五目标交叉构建
python3 scripts/check-docs.py          # 本地文档链接、锚点和矩阵编号
scripts/build.sh                       # 五个目标，产物在 dist/
go run ./cmd/uishots                    # 假控制器 UI 截图，不是真机认证
scripts/e2e-linux.sh                    # 需 Xvfb、mpv、ffmpeg
scripts/e2e-update.sh                   # 本地更新/回滚集成场景
```

[`scripts/check.sh`](scripts/check.sh) 是统一可复现检查入口，是否本轮完整通过以 [验证记录](docs/VALIDATION.md) 为准。该脚本不构建或测试 React 设计稿。

Windows/macOS 打包和签名钩子在 [`scripts/`](scripts/) 与 [`packaging/`](packaging/)。`dist/`、`third_party/` 不入 Git。`scripts/fetch-mpv.sh` 涉及外部下载，发布前还需核对来源、构建和许可。`scripts/release.sh` 是打包编排，不代表已发布到 GitHub。

更新客户端有 Ed25519 清单校验和回滚代码，但需要发布者配置可信公钥、更新源及发行材料。签名钩子或界面“签名构建”标志不等于经过系统验证的数字签名，更不等于 macOS 公证。见 [发布门槛](docs/VALIDATION.md)。

## 代码布局

| 路径 | 责任 |
|---|---|
| `cmd/wallpaperd` | 原生应用、headless、内部媒体/MF 子进程入口 |
| `cmd/wpupdate` | 更新清单/密钥工具 |
| `cmd/uishots` | 使用假控制器生成 UI 截图 |
| `internal/ui` | 原生七页设置、托盘、主题、交互 |
| `internal/app` | 配置/库/分屏编排与工作线程 |
| `internal/desktop` | Windows/macOS/Linux 平台宿主和事件 |
| `internal/render` | mpv/MF/AVF/静态图渲染 |
| `internal/visibility`、`internal/supervisor` | 暂停/电源状态机与故障恢复 |
| `internal/importer`、`internal/wpk`、`internal/media`、`internal/transcode` | 安全导入、媒体探测、转码 |
| `internal/config`、`internal/telemetry`、`internal/update` | 本地持久化、脱敏日志、签名清单更新 |
| [UI 分支设计稿](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web) | 仅外部设计参考，不包含在主分支应用中 |
| `docs` | [导航](docs/README.md)、实现矩阵、验证与历史研究 |

## 明确的边界

- Windows/macOS 真机挂载、混合 DPI、睡眠/TDR、硬解、功耗、自启和签名发行需分别验收，云端编译或模拟 UI 不可代替。
- Web/Shader 壁纸引擎、在线工坊/画廊/云同步、商店发行等未闭环项在 [F-* 矩阵](docs/IMPLEMENTATION_STATUS.md) 明列；不把占位按钮或本地示例当完成。
- Linux 不是完整 Wayland/GNOME/KDE 产品支持。研究扩展和 D-Bus 原型也不代表已经系统集成。
- 当前生产配置强制不启用远程遥测。本周指标只记录本地动态壁纸已确认的 L0–L4 时长；暂停时长不是实测或估算节电量，不能用来证明硬解/功耗达标。早期 opt-in 研究构想不覆盖当前隐私行为。
- 历史文档中的外部价格、系统/SDK 版本、许可证和市场数字未在本轮重新核验。法律内容不是法律意见。

## MyGo workaround

托盘常驻、窗口关闭后重建、菜单刷新和退出主线程限制见 [历史实现说明](docs/history/IMPLEMENTATION_0.2.1.md) 与当前 [`internal/ui/shell.go`](internal/ui/shell.go)。应结合当前代码和本轮测试使用，不把历史绕行描述视为永久的上游 API 保证。
