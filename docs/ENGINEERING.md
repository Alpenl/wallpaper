# 工程规则

适用于当前原生 Go 应用；历史实验不是生产实现或验证替代品。

## 1. 正确性与验证
沿用 `scripts/check.sh` 的格式、测试、race、vet、构建和文档检查，以及既有五目标构建。
本次修改不得引入新失败；按改动边界补有意义的回归，不设统一覆盖率门槛。
跳过、环境受限、交叉编译与目标平台实测分别报告，不能相互替代。

## 2. 大小预算
文件 >600 行、函数/闭包 >80 行、复杂度 >15 是 review 信号，不是拆分门槛。
超过 1000 / 150 / 30 时明确写出保留或提取理由；表格、FFI 初始化和校验可保持整体。
唯一大小硬限：`cmd/`、`internal/` 的非测试 `.go` 不得超过 2000 物理行，豁免 `internal/ui/i18n.go`。
检查所有 GOOS 文件；2000 不是目标，不为数字制造碎片文件。

## 3. 依赖与抽象
不新增 UI→具体 renderer/platform、config→app/UI 依赖；composition root 可组合具体实现。
新接口、包或配置项必须有当前调用方和实际边界、平台差异或测试用途；无行为选项不宣传。
不建插件系统、DI container/service locator，不拆多 module/repo，不加 DTO/repository/use-case 空层。
不造万能 GPU/media backend；仅共享已证实可移植的策略，不为少量重复创建通用 subprocess 框架。
不建遥测后端、OpenTelemetry/log server、crash upload、minidump pipeline，不常开 verbose/per-frame 日志。
不全面改写 scripts 到 Make/Bazel/workflow generator，不双维护历史代码，不重建旧研究或增加文档治理层。
不设全库 panic/sleep/interface/ignored-error 配额，不做强制英文扫库。

## 4. 持久化
成功写入、迁移备份、更新替换/回滚、IPC 序列化不得静默丢错；清理或明确 fallback 可说明例外。
修改持久化边界时加入失败 I/O 回归；迁移备份失败不得覆盖原文件。
config schema 变更验证 old→current 和 future→read-only，未来版本文件不得被回写破坏。
不引入未获需求支持的降级/双向迁移。

## 5. 并发
每个新 goroutine 有一个 owner 和退出条件；关键关闭工作可取消或有有限期限，并在必要时 join。
连接关闭必须能释放相关 handler；进程/线程资源遵循所在平台的生命周期。
新测试不用固定 sleep 猜测调度完成；使用 ack、可控时钟或有期限的条件观察。
真实计时、重试和消息泵可保留等待，但说明测量目的与界限。

## 6. 日志与诊断
transition 在阻塞工作前记录 begin，返回时记录 end/error；生命周期 IPC 区分 write/response/timeout。
统一 `session/display/item/backend/child_pid/generation/op`；未知身份明确表示，不复用含混 `id`。
不为 stats/per-frame 加常规阶段日志；mpv warn/error 转发主日志，不增父 watchdog 或 timeout raw-tail reader。
本地日志有界：child 日志每次启动轮换并保留一份 `.1`，不做运行中轮换；MF 和 stderr 写入侧限额 2 MiB；mpv 保留 `--log-file`（排查问题离不开它）。logger 报告首个 I/O 错误和丢弃数，Close 时等待写完。
每进程在 dispatch 前配置 Go crash output，child stderr 有界；不承诺原生崩溃转储。
导出按格式清洗、保留尾部和截断/省略标记，最终 ZIP ≤20 MiB；包括脱敏 assignments 与 logger 状态。
测试保留 mpv warning、MF failure、child crash，移除 home/空格路径、JSON 私密值和 URL 凭据。

## 7. 版本与兼容
release tag 是应用版本唯一来源；未注入 release 版本时为 `dev`，不参与自动更新排序。
同一 buildinfo 供 CLI、启动、About、诊断使用；commit time 不是 build time，未知值如实表示。
release 版本须与注入值、installer、manifest 一致；SemVer prerelease 数字按数字比较。
IPC major、update/config/WPK schema、cache version 独立；不支持的协议/schema 在副作用前拒绝。
修改兼容边界必须有 mismatch 测试；只在既有 reply 加 helper PID/build，不新建 handshake。
不建全局版本号、兼容矩阵服务，不要求 mpv 与 app SemVer 相等。

## 8. 文档
用户可见契约变化更新一个当前权威入口，并在发版时写真实 release note。
历史设计/实验明确标注且指向生产路径；删除历史实现时同步修正可运行性说明与引用。
发版前复核 README 的版本、依赖、限制及实际测试平台，不把旧结果冒充新验证。

## 9. 语言
log messages、error strings、identifiers 使用 English；注释沿用所在文件主导语言。
用户文案通过既有 i18n；不做批量翻译或语言 lint。

## 10. Agent 工作流与已知债务
每份任务 brief 列出范围、文件、验证命令和目标环境；reviewer 独立复跑并记录结果。
生产 update feed 启用前必须修复事务记录/替换/回滚恢复，并统一稳定启动确认；schema reject 已列本期。
既有大函数、goroutine ownership、UI→mpv 依赖和 fixed-sleep 测试仅在相关边界触及时整改。
