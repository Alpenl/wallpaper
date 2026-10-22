# 本轮验证记录与发布门槛

**正式应用基线为主分支 `2710033ef169852bd7c4a1b5c063d29f3f3e89fd`。原生改动已经移入主分支工作区，React 仅作设计参考且不包含在交付源码中。新增原生 MVP 实现已完成本轮根模块普通/race、vet、无 cgo、统一五目标构建与文档检查；最后 Windows MF 生命周期修正之后，16:48:32–16:49 UTC 又完成整树统一检查及五目标构建，另保留聚焦检查记录。16:08–16:09 UTC 的旧布局检查点独立保留，不能冒充新增源码的结果。Windows/macOS 真机与发行门槛仍未关闭。**

本文件保留本轮已实际执行的阶段验证；旧 README 中的 Wine/Xvfb/安装器/更新 e2e 结果保存在 [历史快照](history/IMPLEMENTATION_0.2.1.md)，不能复制为本轮 PASS。

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
| 研究状态机模块 | `cd docs/design/visibility && go test ./...` | 通过【主分支验证】 | 独立研究模块，不是生产覆盖替代品 |
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
- [`design/visibility`](design/visibility/README.md)
- [`design/linux-dbus`](design/linux-dbus/README.md)
- [`research/wpproto_src`](research/wpproto_src/)

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
