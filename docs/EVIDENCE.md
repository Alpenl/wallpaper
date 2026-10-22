# 证据约定与历史差异

正式应用基线：主分支 `master`，提交 `2710033ef169852bd7c4a1b5c063d29f3f3e89fd`，树 `5f58cc982598f01813040eff3c684b0b906a50dc`。设计参考：[UI 分支 `5f00d334`](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web)。源树核对结果：主分支的 182 个文件与 UI 分支对应文件的 blob 和 mode 一致，UI 分支只额外包含 11 个 `web/` 设计稿文件；这些 React 文件不进入主分支交付。

本轮原生改动已经移到主分支工作区；未修改的基线文件来自该主分支提交。工作区迁移前的聚焦结果保留为阶段证据，最终产品结果需看 [验证记录](VALIDATION.md) 的主分支检查。本轮日期以 2026-10-21 UTC 记录；引用旧报告时保留其原始日期和时区。

## 证据层级

| 标记 | 能说明什么 | 不能说明什么 |
|---|---|---|
| 【当前源码】 | 本轮检查过对应代码；需链接到文件/符号 | 不证明代码能编译、某平台能运行或达到性能 AC |
| 【迁移前验证】 | 迁移到主分支之前的阶段测试，供回归对照 | 不证明最终主分支实现通过，必须迁移后重跑 |
| 【主分支验证】 | 在已核对主分支工作区执行的指定检查；以最终整体记录为准 | 不能外推未执行的平台、签名或功耗验证 |
| 【本轮验证】 | 本轮指定命令、环境、结果；见验证记录 | 交叉编译不等于运行，模拟控制器不等于真实桌面 |
| 【历史记录】 | 原 README、研究/设计文档记录过该结果 | 不能给当前改动盖 PASS；缺少原始日志时只能作为报告 |
| 【外部文献】 | 该来源在其日期/版本下的陈述 | 本轮未重新核实时不能称“最新”；第三方测量不等于本产品 |
| 【设计目标】 | F-* AC、PB 预算、ADR、测试步骤 | 不是实现状态，也不是实际测量 |
| 【推断】/【待验证】 | 明示尚缺数据或仅依据 API 语义 | 不应包装成确定效果 |

旧文的【源码】【文档】【实测】【issue】保留原样，需结合原文上下文解释。新的当前状态统一用上表的明确标记。当前结果必须写进 [VALIDATION.md](VALIDATION.md)，至少包含命令/场景、范围、环境、结果及限制。

## 本轮发现并对齐的差异

| 旧表述/位置 | 当前解释 | 证据/处理 |
|---|---|---|
| D-07、设计首页说“fps15/限帧未实现” | 针对研究原型，不再描述生产代码。生产 mpv/MF/AVF 已有 `SetFPSCap`；限帧不代表已经证实功耗减半 | [`render/mpv`](../internal/render/mpv/mpv.go)、[`render/mf`](../internal/render/mf/client_windows.go)、[`render/avf`](../internal/render/avf/avf_darwin.go)；D-07 已添加当前说明 |
| ADR-010 / D-15 建议 v1 opt-in 远程遥测 | 生产配置强制 `Telemetry=false`；本轮不增加远程遥测 | [`config.Normalize`](../internal/config/files.go)；后续若变更需独立隐私/服务决策 |
| 旧 README 的“10 个页面”“发现占位” | 改造前 UI；新 UI 以七页信息架构和验收矩阵为准 | [历史快照](history/IMPLEMENTATION_0.2.1.md)、[UI-*](IMPLEMENTATION_STATUS.md) |
| 旧 README 的 race、Wine、Xvfb、安装/更新 e2e PASS | 是以前某环境/版本的报告，不是本轮重新执行的结果 | 全文移入历史快照；本轮结果单列，不继承通过 |
| 设计 F-13 标为 v1 | 本地名称搜索和网格/列表在基线已有；本轮补作者/说明/标签/颜色，1000 条查询微基准已记录；仍不把云端微基准外推成真实窗口时延认证 | [F-13 状态](IMPLEMENTATION_STATUS.md)、[`internal/ui/library.go`](../internal/ui/library.go) |
| F-09 说关设置后 WebView 进程退出 | 当前设置 UI 是 MyGo 原生绘制，不是 WebView；应验证窗口按需销毁/重建和后台资源，而不是不存在的设置 WebView 进程 | [`internal/ui/shell.go`](../internal/ui/shell.go) |
| “已实现安装与更新” | 本地签名清单验证/下载/回滚机制与“公开签名发行、线上更新已启用”是不同状态 | [`internal/update/`](../internal/update/)、[发布门槛](VALIDATION.md)；公钥、更新源、证书、签名/公证与真机验收仍需各自验证 |
| 早期“全部是 Go / CGO_ENABLED=0”旁列 `go test -race` | 发布构建可关闭 cgo；Go race 测试通常需要 cgo 与可用编译器，不能要求该测试也用 `CGO_ENABLED=0` | [构建说明](../README.md)、[验证命令](VALIDATION.md) |
| 16 章的 schema v1 与旧配置样例 | 本轮配置/库/分配/状态 schema 均已升到 2；设计中的旧样例是规格历史，不是当前字段全集 | [`config/files.go`](../internal/config/files.go)、[`config/options_test.go`](../internal/config/options_test.go) |
| 早期文档只有 avconvert、mac GIF 尚无严格转码 | 当前默认是应用隔离 AVFoundation helper，可选 ffmpeg；avconvert 仅有限回退。原生路线源码不是 mac 真机通过证明 | [转换路线](../internal/transcode/README.md)、[研究补充](research/NATIVE_MVP_ADDENDUM.md) |
| 早期“跨屏画布/本周统计未提供” | 当前已有每屏原生裁切空间跨屏和实际 UTC 周暂停计时；没有逐帧同步、单解码器或节电量测量承诺 | [跨屏](SPANNING.md)、[F-03/F-17](IMPLEMENTATION_STATUS.md) |
| 16:08–16:09 的“最终检查通过” | 是七页布局阶段检查点，不能覆盖其后新增转码、跨屏、指标和 MF 控制代码；最终整体验证须另记 | [验证台账](VALIDATION.md) |
| 研究目录里的实验模块 | `docs/design/prototype` 等是独立 `go.mod`，不是根包的测试覆盖 | [测试范围](VALIDATION.md) 明确拆分 |

## 新技术依据与历史外部事实的区别

本轮原生转码、MF 播放属性及 purego 调用选择已由实现和代码复核对照主要 API 文档/源码；[转码来源列表](../internal/transcode/README.md#primary-references) 保留对应入口。这不等于 AVFoundation/MF 已在真实平台执行，也不表示三份旧调研中的市场、价格、许可或系统版本已全面重新核验。

## 不在此 checkout 中的历史资产

研究记录提及研究机上的 clone、r6/r9/r12/r15/r18 备份、`prototype/bin/`、`bench/webview/webgl_test.html` 和 `bench/webview/`。本轮没有在 checkout 找到这些资产。保留原引用以便追溯，但不宣称可以直接运行缺失素材的 Web 基准。运行前需补齐已知来源的素材，或另外创建并说明新的等价测试，不得伪造原始结果。

`docs/screenshots/*.png` 是已入库的旧 UI 图；除非本轮明确重新生成、检查并记录，不标成本轮截图。外部链接不在离线文档校验器的检查范围。

## 维护规则

1. 状态只在 [实现矩阵](IMPLEMENTATION_STATUS.md) 维护；测试结果只在 [验证记录](VALIDATION.md) 维护，其他入口链接过去。
2. 新结果需给出来源/命令、平台与适用范围；不要单写“全部通过”或“已完成”。
3. 历史结论被取代时加注释/转接入口，不删除原始研究或把旧测量日期改成本轮。
4. D-* 的“建议”“代码默认”不是负责人批准记录；Q-* 理论上关闭不等于平台验证关闭。
5. 任何签名、商店审核、功耗与线上服务的“完成”必须有对应外部或真机证据，界面按钮、构建标志与模拟数据都不算。
