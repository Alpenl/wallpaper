# visibility：历史状态机实验索引

本目录是冻结的历史研究入口。重复的 Go 实现、测试与独立模块文件已删除，旧实验结果**不能在当前 checkout 重跑**，也不代表生产覆盖率或平台验收。

- 当前生产实现与测试位于 [`internal/visibility`](../../../internal/visibility/)，在仓库根目录执行 `go test ./internal/visibility`；该结果应独立记录。
- 历史源码和测试可在仓库根目录用 `git log -- docs/design/visibility` 定位，再用 `git show <历史提交>:docs/design/visibility/machine.go` 查看；重现旧实验须从 Git 历史恢复完整旧目录及其工具链环境。
- 历史记录：第 18 轮 28 个测试、语句覆盖率 98.2%、race 通过（2026-10-21，Go 1.27.3，box）；第 19 轮增加 `Controller.NextDeadline()`/`IDs()` 后记录为 29 个、98.3%。这些数字未在本次重新验证，不转用于生产模块。
- 规格修订见 [07 章 7.8](../07-pause-power-state-machine.md)，验证范围见[验证记录](../../VALIDATION.md)。
