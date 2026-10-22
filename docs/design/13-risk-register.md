# 13 风险登记表（第 9 轮修订）

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

| ID | 风险 | 平台 | 概率 | 影响 | 依据 | 对策 | 状态 / 验证项 |
|---|---|---|---|---|---|---|---|
| R1 | WorkerW 结构随 Windows 更新变化，或被 Explorer 销毁 | Win | 高 | 高 | lively #2374/#2777；25H2 与 24H2 同一代码库 | 自检、退避重挂、降级为静态；每个版本做回归测试 | 开放；W-T1、W-T6 |
| R2 | 第三方桌面或安全软件冲突（360 等） | Win | 中（国内高） | 高 | lively #3103 | 冲突检测与提示；维护兼容矩阵 | 开放；W-T6 |
| R3 | MyGo DComp 窗口 SetParent 后黑屏 | Win | 未知 | 中 | compositor.go | **已规避**：壁纸表面用自建子 HWND（ADR-001） | 关闭 |
| R4 | desktop level 窗口的 occlusionState 来回跳变 | mac | **已确认** | 中 | Wallnetic 源码注释 | 3 次采样去抖 + CGWindowList（07 章） | 缓解；M4 |
| R5 | macOS 下 mpv 不可用 | mac | 确定 | 低 | mpv-examples #29 | **已规避**：用 AVFoundation | 关闭 |
| R6 | webm/VP9 素材 AVFoundation 解不了 | mac | 高 | 中 | WebKit bug 216652 | VT supplemental VP9 解码 + VT 重编码（06 章 6.6） | 缓解；Q-M4 |
| R7 | mpv CLI 是 GPL，许可边界 | Win | 中 | 高 | mpv Copyright；FSF FAQ | 独立程序 + 源码要约（ADR-004）；律师复核 | 开放；Q-L1 |
| R8 | H.264/HEVC 专利 | 全部 | 中 | 高 | ffmpeg legal；Via LA；Access Advance | 优先系统编解码器；律师评估 | 开放；Q-L2 |
| R9 | WebView2 用调试器冻结进程导致不稳定 | Win | 中 | 中 | Lively 源码 | **改用 TrySuspend**（ADR-005） | 缓解；B-T2 |
| R10 | purego 回调上限 | mac | 低 | 中 | MyGo 文档 | 启动时集中注册 | 缓解 |
| R11 | 锁屏动态壁纸依赖非公开路径 | mac | 高 | 低 | LiveDesk | 只在直接分发版作为实验功能 | 开放 |
| R12 | 循环点卡顿 | 全部 | 中 | 中 | WE 文档；AVPlayerLooper | 导入时统一时长、去音轨、交叉淡化 | 开放；W-T3、M3 |
| R13 | 驱动 TDR 或睡眠后 mpv 卡住不退出 | Win | 中 | 高 | mpv #3469；lively #3078 | time-pos 停滞检测 + 重启（02 章 2.4） | **新增**；W-T9 |
| R14 | 恶意壁纸包（路径穿越、zip bomb、外传数据、挖矿） | 全部 | 中 | 高 | Go archive/zip 默认不拒绝不安全路径 | 10.4 / 10.6 的校验、沙箱、默认禁网、CPU 看门狗 | **新增**；单元测试加 fuzz |
| R15 | SmartScreen 警告影响首次安装转化 | Win | 高 | 中 | MS Learn（EV 不再自动获得信誉） | Artifact Signing 或 OV 签名积累信誉；第二阶段上 Store | **新增** |
| R16 | MAS 审核拒绝 Web 壁纸（2.4.5(iv) 关于额外代码的条款） | mac | 中 | 中 | App Review Guidelines | MAS 版首发只支持视频和 shader，Web 壁纸作为后续功能 | **新增**；Q-D3 |
| R17 | 坏包导致无限崩溃循环 | 全部 | 低 | 高 | 【推断】 | 安全模式（02 章 2.5） | **新增** |
