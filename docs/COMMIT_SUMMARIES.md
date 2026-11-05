# 提交总结

从 2026-11-05 开始，代码、测试、脚本、配置和资源提交必须在本文件**末尾追加独立条目**，
与改动一起提交。此文件记录“为何改、改了什么、验证了什么、还有什么限制”，
不替代 [工程规则](ENGINEERING.md)、[验证记录](VALIDATION.md) 或 [发行说明](releases/README.md)。
前两条补记已完成的分支修复与合并，不重写既有 Git 历史；后续每个代码提交一条。

## 条目模板

下面只作格式示例，不作为任何提交的实际总结：

```markdown
## YYYY-MM-DD | 唯一主题（同日多次提交可加序号）

- 目的：需求、问题及为什么修改。
- 改动：实现要点、影响范围与关键文件。
- 验证：实际执行的命令、平台及通过/失败/未运行结果。
- 限制：未验证项、兼容性、已知问题与发布边界；没有则明确写“无已知新增限制”。

可补充提交前基线、已有相关提交、根因、回滚注意等；不要预填不存在的当前提交 SHA。
```

---

## 2026-11-05 | 补记两分支的三项合并阻碍修复

- 目的：使 `codex/status-menu` 与 `we-sceneprobe` 能在修复已复现阻碍后合入主分支，而不是仅凭分支 CI 绿色判断安全。
- 改动：分别提交四目标发布契约/回归测试修复，以及 sceneprobe helper 输出预算修复，两个提交保持独立。
- 验证：合并前独立 LF 组合源码通过 Windows Go 普通测试、sceneprobe 定点重复测试、三平台 vet、五目标无 CGO 构建、文档与工程规则检查；WSL 发布测试与 sceneprobe 测试通过，具体范围如下。
- 限制：Windows 和 WSL 均缺可用 C 编译器，本轮 race 受阻，不能称 `scripts/check.sh` 整体通过；不是完整功能分支验收，也没有新安装、签名、公证或真机证据。

### 提交来源

| 原分支及基线 | 修复分支 | 修复提交 |
|---|---|---|
| `codex/status-menu` / `9f04e48` | `fix/status-menu-merge-blockers` | `c299795c6a7a1890fb350e9f729844f1c220ae86` |
| `we-sceneprobe` / `6127045` | `fix/sceneprobe-output-budget` | `8636fdc5d0af89737a0141ed818314dd7735b2d7` |

### 三项阻碍与修复

1. **发布器仍要求 Linux 包。** 发布入口已经只构建 Windows/macOS 四目标，
   `scripts/publish-release.py` 却继续要求 Linux 产物。修复后要求 Windows amd64/arm64
   installer、macOS amd64/arm64 app ZIP 和三个元数据文件。DMG 可选；
   `updater_signing=signed` 时要求更新归档；所有资产仍受非空普通文件、非符号链接、
   SHA256 全覆盖与远端摘要检查约束。未知额外包和 Linux 包拒绝发布。
2. **测试依赖已移除的 Linux assertion。** `scripts/test_release_pipeline.py` 从 shell
   脚本搜索特定 `assert re.fullmatch(...)`，断言移除后 `next(...)` 抛 `StopIteration`。
   改为执行 `release-verify.sh` 的完整内嵌 Python 验证器，用夹具模拟打包工具，
   并与 publisher 测试共同覆盖同一四目标资产契约，不再依赖一行实现文本。
3. **sceneprobe 输出预算被复制优化路径绕过。** `probeOutput` 匿名嵌入 `bytes.Buffer`，
   提升的 `ReadFrom` 让 `io.Copy` 跳过自定义 `Write`。修复为具名私有缓冲字段，
   stdout 限额保持 64 KiB，stderr 保持 16 KiB；超限 kill，`cmd.Run` 等待并回收。
   新增真实 pipe 复制、精确边界、超出 1 byte、合法 stdout 加超限 stderr、
   超限后驻留进程提前终止/回收回归，不把 JSON 解析失败冒充预算生效。

关键文件：`scripts/publish-release.py`、`scripts/test_publish_release.py`、
`scripts/test_release_pipeline.py`、`cmd/wallpaperd/sceneprobe.go`、
`cmd/wallpaperd/sceneprobe_test.go`；发布范围、实现状态与验证文档同步更新。
sceneprobe 是只读本地离线特征报告，不是 WE scene 播放能力，也未接入导入器或 UI。

### 合并前验证细节

- Windows/amd64、Go 1.27.3：`go test -timeout 180s ./...` 通过；sceneprobe 定点回归
  `-count=10` 通过。三平台 vet、五目标无 CGO 命令构建、gofmt、工程规则及文档检查通过。
- WSL Ubuntu 26.04：Windows 交叉编译的 Linux/amd64 sceneprobe 测试二进制实际运行，
  `-test.count=10` 通过；不代表 Linux 桌面验收或恢复 Linux 开发。
- WSL Python 3.14.4：66 项发布脚本测试和 6 项打包守卫通过。
  Windows Python 3.12.10 的符号链接测试受 `WinError 1314` 阻碍，未削弱或跳过测试，
  而是在 WSL 完整验证。
- Windows checkout 的 CRLF 会造成 shell/gofmt 误报，验证使用 Git tree 的独立 LF 副本。
  `go test -race` 因找不到 `gcc` 未能运行；旧章节在其他环境的 race PASS 不适用于本轮。

## 2026-11-05 | 补记合并到 master 与推送结果

- 目的：将两条独立修复连同原分支功能合入仓库实际默认分支 `master`；仓库没有 `main`，合并目标已由用户确认。
- 改动：创建两个 no-fast-forward 合并提交，先合并状态菜单/发布修复，再合并离线 scene 探测/输出预算修复；保留修复分支。
- 验证：实际合并后的 LF 源码再次通过 Windows Go 普通测试与 sceneprobe 回归、三平台 vet、五目标构建、文档检查、WSL 66 项发布测试和 6 项打包守卫。
- 限制：race、真机、安装与签名限制未解除；推送代码不等于创建 GitHub Release，没有创建 tag、发布安装包或删除远端分支。

| 顺序 | 合并提交 | 内容 |
|---|---|---|
| 1 | `730cd238f351bb3af7b5c9b6980784cf501d7758` | 状态菜单/事件循环改进与四目标发布契约修复 |
| 2 | `9f09dbdd7e5fccdb6a2a311888ceae24a4ee7095` | 离线 scene 探测与输出预算修复 |

两条修复分支已推送；`master` 从 `9a96e2d` 推进到 `9f09dbd` 并推送。
文档补记开始时，本地 `master` 与本地记录的 `origin/master` 都指向 `9f09dbd`。
合并后的 Windows 普通测试覆盖根模块全部包；文档检查当时为 46 个 Markdown 文件、
417 个本地链接。三平台 vet 为 Windows/macOS/Linux；五目标构建为 Windows/macOS
各 amd64、arm64，加 Linux amd64，均关闭 CGO。

推送后的最后一次远端状态观察：Docs 已成功、CI 尚在运行；本条没有将该观察改写成
最终 CI 成功，也没有在文档补记时重新轮询。真机结果应查具体日期的原始记录，
不能从本次代码合并推断 macOS/Windows 的全部场景已完成验收。

## 2026-11-05 | 建立逐次提交总结与修正文档状态

- 目的：落实“从本次开始，每次代码提交都追加总结”，避免只在聊天说明或合并后追补，同时消除当前导航和验证记录的过期状态。
- 改动：新增本文件、根 `CLAUDE.md`、暂存区检查器及回归测试、Git pre-commit/pre-merge-commit 钩子与 Claude SessionStart 提示；更新工程规则、导航、合并后验证记录和可选 DMG 说明。
- 验证：Windows Python 3.12.10 的 20 项检查器回归通过，包含真实临时 Git 仓库中的提交拦截；文档链接检查及其自测通过；项目 JSON 结构、SessionStart 标准输入测试和隔离暂存区检查通过。WSL Ubuntu 运行当前全部 86 项脚本测试时，8 个发布资产夹具失败于正在并行修改的图标资源校验（`wallpaperd.exe has no icon resources`）；不将其归因于本流程，亦不将全量脚本测试标为通过。
- 限制：钩子需要每个克隆单独启用；Claude 新增设置需要新会话加载。检查只保证新增条目的存在、格式及旧记录不被改写，不能证明总结真实或测试已运行；不覆盖网页合并和绕过钩子的操作。

基线：`9f09dbd`。本轮不修改应用运行代码，不改变发布平台范围或签名配置。
原有历史验证记录保留，当前主分支状态单独补充；现有 CI 已通过 `test_*.py` 自动发现新测试，
没有增加历史文档治理系统或远端提交策略。新流程本身也使用独立条目记录，不重写已推送提交。

## 2026-11-05 | 图标夹具修复后的脚本复验补记

- 目的：复核先前 8 项图标资源夹具失败是否消除，保留首次失败记录并追加最新结果。
- 改动：本条仅补记验证结论；图标工作已更新发布测试中的 Windows PE 图标资源及 macOS 应用图标夹具，本次复验未修改图标实现。
- 验证：在 WSL 使用 `python3 -m unittest discover -s scripts -p 'test_*.py'` 独立复跑当前工作区全部 86 项脚本测试，全部通过，包括 20 项提交总结回归；`python scripts/check-docs.py` 通过（48 个 Markdown、435 个本地链接）。先前记录的 8 项发布资产夹具失败已消除。
- 限制：结果针对 `9f09dbd` 加当前未提交变更的工作区，不是已提交版本或远端 CI 结果；未重新执行 Go 全量测试、race、安装包构建、图标真机显示、签名或公证验收。本次未提交或推送。

## 2026-11-05 | 应用图标与逐次提交流程首次入库

- 目的：为 Windows/macOS 应用、安装器和托盘提供统一的应用图标，并把已建立的逐次提交总结流程、检查器、钩子和相应文档基线变更一并纳入版本库。
- 改动：新增 1024×1024 RGBA 母版、9 帧 ICO（16–64 为 32 位 DIB，128/256 为 PNG）、11 元素 ICNS、Windows 帧和 go-winres 配置，以及 amd64/arm64 `.syso`；`build-icons.py` 为 16–32 px 生成单峰简化母版，`check-icons.py` 校验 PNG/ICO/ICNS 与 `.syso` 的机器类型、重定位、9 帧图标组和内嵌 PNG 尺寸。接入 NSIS 安装/卸载图标与 `DisplayIcon`、macOS `CFBundleIconFile` 和 Resources 复制、`release-verify.sh` 的安装器/可执行文件图标断言、`release.sh`/`check.sh` 的图标检查；Windows 托盘继续运行时绘制同构图标。同一提交还加入 `CLAUDE.md`、`.claude/settings.json`、`.githooks`、提交总结检查器和回归测试、`docs/COMMIT_SUMMARIES.md`，并更新导航、工程规则、验证与发布文档。
- 验证：`scripts/check-icons.py` 通过；windows/amd64、windows/arm64、darwin/amd64、darwin/arm64、linux/amd64 无 CGO 交叉编译通过；从新构建的 `wallpaperd.exe` 实际提取到 32×32 图标；`.syso` 与全部图标资产再生成后字节一致；`go test ./...` 通过；`go vet ./...` 返回 0（保留既有 `unsafe.Pointer` 提示）；`scripts/test_release_pipeline.py` 11 项通过；`scripts/check-docs.py` 通过（48 个 Markdown、435 个本地链接）；Windows 下 `scripts.test_publish_release` 与 `scripts.test_commit_summary` 合计 75 项中 74 项通过，唯一错误为无符号链接权限的 `WinError 1314`。
- 限制：本机没有 `makensis`，NSIS 安装器未实际构建或运行；未在真实资源管理器、任务栏或 Finder 中验收图标外观；macOS `iconutil`、签名与公证未运行；race 测试因本机无 cgo 未运行，`gofmt` 对 CRLF 工作区中的 `internal/ui/shell.go` 报告整文件差异，属于行尾格式而非代码格式问题。真实图标外观与安装行为仍以对应平台的原始验收记录为准。

基线：`9f09dbd`。本次提交同时包含同一工作区中先前未提交的文档、流程和图标改动，没有逐项重跑这些历史改动的完整验证。

## 2026-11-05 | Windows 安装器本地构建兼容修复

- 目的：在本机用 NSIS 3.13 实际构建安装器时，`UnIcon` 不是有效命令，且传给 `makensis.exe` 的 `SRC`/`OUT` 是 Git Bash 的 `/c/...` 路径、`File` 又拒绝 `C:\...\dir/file.exe` 混合分隔符，导致两个架构都无法打包；修复后本地 Windows 构建和原有 Linux 构建路径都能工作。
- 改动：`packaging/windows/installer.nsi` 把 `UnIcon` 改为 `UninstallIcon`，所有 `${SRC}/文件名` 改为 `${SRC}文件名`，并在头部说明 `SRC` 必须以平台路径分隔符结尾；`scripts/package-windows.sh` 给 `SRC` 追加平台分隔符，存在 `cygpath` 时同时转换 `ICON`、`SRC`、`OUT` 为 Windows 路径。Linux 无 `cygpath` 时仍使用 `$src/` 和原 POSIX 路径。
- 验证：用 NSIS 3.13 构建出 `0.3.5-local.1` 的 amd64/arm64 安装器；7-Zip 26.05 解包成功，载荷 `wallpaperd.exe` 与 `dist/windows-*` 构建产物逐字节一致，元数据文件一致，未捆绑 mpv；安装器为 x86 GUI bootstrap，载荷分别为 amd64/arm64，均含 `RT_GROUP_ICON`；从安装器和解包出的卸载器实际提取到 32×32 图标。产物 SHA-256：amd64 `348886e3e2278b0364bf72fa51172217c8cd5c9eb3ef60a4bffd9712ecb5ea74`，arm64 `5ebf37caf1d851b5accb3f90d93193c6e5724998386cf38b25c3f05bc197231b`。
- 限制：安装器未实际执行安装或卸载，快捷方式、注册表、覆盖升级和卸载清理未做运行验证；安装器未签名，SmartScreen 会警告；未在 Linux/CI 重新运行 `package-windows.sh`；本次只改打包脚本和 NSIS 脚本，未重跑 Go 测试。上述安装器构建于本提交之前，内嵌的 `BUILD-INFO.txt` 记录 `dirty=true`，不代表本次提交后的干净构建。

基线：`49a4e32`。

## 2026-11-05 | 收缩为图片/视频壁纸并移除 Web/Shader 引擎

- 目的：按新的产品定位，只保留图片与视频壁纸，永久移除 WebView2/WKWebView Web 壁纸、GLSL→HLSL/MSL Shader 渲染、音频与鼠标响应式输入、WE scene 离线探测，以及它们对应的配置、导入、UI、示例和发布路径。
- 改动：删除 internal/app 的 web/reactive 实现、internal/config 的 web 配置与迁移、macOS/Windows 的 Web/Shader 宿主、cmd/wallpaperd 的 sceneprobe 与 shader 探测、examples/shader-waves 与 examples/web-orbit、相关测试和 CI 步骤；壁纸类型与 wpk schema 收敛为 video|image，render.Item 去掉 Viewport/PackageDir/FPSCap/Kind；导入器拒绝 Shader/Web 来源；library schema 升到 5，旧 schema 不迁移——启动时忽略旧索引与分配并立即落盘为空库和空 assignments，旧磁盘壁纸目录保持不动；同步 i18n、示例、README、设计/验证/状态文档与 DECISIONS_PENDING（D-16 等）。为通过提交总结检查，本分支引入 master 已有的 docs/COMMIT_SUMMARIES.md 与 scripts/check-commit-summary.py。
- 验证：go test -timeout 240s ./...、go vet -unsafeptr=false ./...、CGO_ENABLED=0 go build ./... 均通过；darwin/amd64、darwin/arm64、windows/arm64、linux/amd64 交叉构建通过；python scripts/check-docs.py 通过（46 个 Markdown、408 个本地链接）。
- 限制：未做 Windows/macOS 真机运行与安装包验收，未运行 race 与真机性能测试；旧 library schema 数据被有意丢弃且不提供迁移或旧壁纸目录清理，新 schema 仍保持只读保护；本分支基于 9f09dbd，尚未合入 master 的图标与提交总结工具提交，后续合并时 COMMIT_SUMMARIES.md 需保留本分支的追加内容。
