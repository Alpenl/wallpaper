# 前期调研索引（历史资料）

以下报告保留原文，说明当时为何选型。它们不是当前产品状态、当前市场事实或发布许可结论。本轮整理没有重新抓取全部外部来源，也没有复跑旧实验。

| 文档 | 原始时间/范围 | 阅读价值 | 当前接续 |
|---|---|---|---|
| [市场与竞品](wallpaper_research.md) | 2026-10-20，包含更早的第三方收入、下载、定价与评论 | 痛点、产品方向；数值与价格必须按决策时点重核 | [产品与竞品](../design/01-product-competitors.md)、[功能规格](../design/17-features-mvp.md) |
| [跨平台技术](dynamic_wallpaper_tech_research.md) | 2026-10-20（原文 PDT） | MyGo、桌面宿主、视频、暂停的早期候选路线 | [架构](../design/02-architecture.md)、[ADR](../design/README.md) |
| [可行性与风险](dynamic_wallpaper_feasibility.md) | 2026-10-20（原文 PDT） | 早期源码审阅、交叉编译与未验证风险 | [问题登记](../design/14-open-questions.md)、[真机交接](../design/bench/DEVICE_HANDOFF.md) |
| [早期原型源码](wpproto_src/) | 前期 MyGo 窗口实验，独立 Go 模块 | 复现当时的结构与编译 | 正式产品是 [`internal/`](../../internal/)；后续实验是 [`design/prototype/`](../design/prototype/README.md) |

## 本轮接续

[原生 MVP 研究补充](NATIVE_MVP_ADDENDUM.md) 对照当前源码梳理空间跨屏、AVFoundation 隔离转码、严格产物验证、暂停指标和后端控制，明确哪些是目标平台验收、哪些仍缺产品架构。它是本地源码分析，不重写三份历史报告，也不伪称新增真机或市场研究。

## 必须保留的上下文

- 旧文中的“本次”“box”“实测”指原报告里的研究环境，不是本轮云环境。
- 技术调研曾讨论直接把 MyGo 顶层窗口挂到 WorkerW；后续 ADR-001 改为 MyGo 只做控制面，平台层另建壁纸表面。不能按早期方案改回去。
- 早期 24H2 的挂载表述由后续 Q-W9 修正；应追到 [Windows 设计](../design/03-windows-desktop.md) 和当前宿主源码，而不是只摘录早期报告。
- 市场预测、竞品价格、系统版本、外部源码行号及许可说明均有时间边界。对当前决策需要重新查官方/原始来源；法律部分不是法律意见。
- 本轮对 `wpproto_src` 执行 `go test ./...` 在 setup 阶段失败：其 `go.mod` 未声明源码导入的 `github.com/egoist/mygo`。保留为早期骨架，不宣称当前可直接复现；详见 [本轮验证](../VALIDATION.md)。
- 没有被提交的克隆仓库、研究机备份、旧二进制和采集原始文件不能被视为此 checkout 内可复现的资产。已知缺件见 [证据约定](../EVIDENCE.md)。

返回 [完整文档索引](../README.md)。
