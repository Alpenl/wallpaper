# 文档导航

本主分支工作区包含原生产品代码、前期调研和真机实验代码；React 设计稿仅保留在独立的 UI 分支作为视觉参考。它们的用途不同，不能把原型结果或历史测试当作当前产品验收。

> 正式应用以主分支 `master/2710033e` 为实现基线，`ui` 分支仅提供设计参考。最终源码与测试范围见 [验证记录](VALIDATION.md)。

## 按问题找文档

| 想知道什么 | 入口 | 性质 |
|---|---|---|
| 本次预发布与安装依赖 | [v0.3.0-mvp](releases/v0.3.0-mvp.md) | 未签名检查点、范围与运行门槛 |
| 如何构建和启动 | [项目 README](../README.md) | 当前使用入口 |
| 七页 UI 如何对应原规划、哪些功能实际接线 | [实现与验收矩阵](IMPLEMENTATION_STATUS.md) | 当前源码状态、验收计划 |
| 本轮跑了什么、哪些没有验证 | [验证记录与发布门槛](VALIDATION.md) | 当前结果，不继承历史 PASS |
| 原生跨屏如何裁切、有什么限制 | [空间跨屏实现](SPANNING.md) | 当前源码与目标平台验收边界 |
| 本轮实现怎样承接原研究 | [原生 MVP 研究补充](research/NATIVE_MVP_ADDENDUM.md) | 本地源码复核，不是新增真机或外部研究证据 |
| 如何判断文献、实测、推断与旧结论 | [证据约定与差异说明](EVIDENCE.md) | 来源/适用范围规则 |
| 为什么选择这些架构与功能 | [设计目录和 ADR](design/README.md) | 设计依据，非完成证明 |
| 功能 ID、AC 与性能预算 | [F-01…F-26 规格](design/17-features-mvp.md) | 目标与验收定义 |
| 哪些事必须由负责人决定 | [D-01…D-15 决策清单](design/DECISIONS_PENDING.md) | 决策门槛，非用户已批准 |
| Windows/macOS 应该怎么实测 | [真机测试计划](design/bench/TEST_PLAN.md)、[设备交接](design/bench/DEVICE_HANDOFF.md) | 测试步骤，非测试结果 |
| 还有哪些技术未知量 | [Q-* 问题登记](design/14-open-questions.md) | 历史问题及当前覆盖说明 |
| 前期市场、技术、可行性研究 | [调研索引](research/README.md) | 保留原文，时效性未重核 |
| 本轮七页原生 UI 的样子 | [当前软件渲染截图](screenshots/native-current/README.md) | 假控制器视图，不是真机/浏览器截图 |
| UI 分支改造前的实现/测试说明 | [0.2.1 历史 README](history/IMPLEMENTATION_0.2.1.md) | 历史证据，不是本轮结果 |
| 原规划七页 UI 的设计参考 | [UI 分支 React 设计稿](https://github.com/Alpenl/wallpaper/tree/5f00d334cd32412291964d228fad4f3b9461cc7b/web) | 只作 UI 设计，产品只能使用原生界面 |

## 阅读顺序与优先级

1. 先看当前 [实现状态](IMPLEMENTATION_STATUS.md) 和 [验证记录](VALIDATION.md)。
2. 再按 F-* / UI-* / D-* / Q-* 编号追到设计、决策和测试计划。
3. 需要了解当时的选型过程再看 [调研](research/README.md) 与 [设计变更记录](design/CHANGELOG.md)。
4. 当旧文与当前源码冲突，按 [证据约定](EVIDENCE.md) 判断；不要默默覆盖历史，也不要以界面里有一个开关推断功能已实现。

## 目录边界

- `internal/`、`cmd/`：唯一产品实现与原生 MyGo 界面；本主分支工作区不包含 `web/`，React 设计稿仅从 UI 分支参考。
- `docs/design/`：01–17 章规格、ADR、问题、决策、实验；其中 `prototype/`、`visibility/`、`linux-dbus/` 有独立 Go 模块，根目录 `go test ./...` 不覆盖这些嵌套模块。
- `docs/research/`：2026-10-20 起的三份早期研究，以及早期 MyGo 原型。
- `docs/history/`：被当前实现说明替代的历史快照，保留证据，不删除。
- `docs/screenshots/` 根目录：历史原生 UI 图；`native-current/`：本轮带来源说明的软件绘制截图。两者均不能替代真实平台运行。

## 文档自检

在仓库根目录运行 `python3 scripts/check-docs.py`。它检查本索引、根 README、`docs/**/*.md`的本地 Markdown 链接及实现矩阵编号；不访问网络，也不校验外部网页时效性。
