# 待办

## Rust + GPUI 技术验证（暂缓，代码在 `rust-spike` 分支）

已完成：Mac 上启动到 confirmed 约 2 秒、壁纸窗口在桌面层、SIGTERM 干净退出、损坏视频退出码为 1。

通过标准中尚未完成的项：

- [ ] 提交并推送 `rust-spike`，由 GitHub Actions 构建 Windows exe。
- [ ] Mac 人工确认：画面在桌面图标下方、设置窗口内容正常、关窗后从托盘重开、托盘暂停和退出、无 Dock 图标。
- [ ] Mac 锁屏解锁后继续播放。
- [ ] Mac 30 分钟自动验收：`scripts/accept/spike-mac.sh`，需要屏幕录制权限。
- [ ] Windows 真机跑 `scripts/accept/spike-windows.ps1`：桌面挂载、跨进程交换链、杀 worker 后恢复、5 秒退出。脚本还没有在 PowerShell 中解析或执行过。
- [ ] 之后验证 Explorer 重启、双屏混合 DPI、GPU 设备丢失。

## 验收单

- [ ] `docs/ACCEPTANCE.md` 补充最糟的三个真实场景。
- [ ] 决定 Windows 的 MF / mpv 对比要求是否保留。
