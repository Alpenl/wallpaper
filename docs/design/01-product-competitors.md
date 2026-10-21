# 01 产品与竞品

市场数据的完整版和来源链接见 `docs/research/wallpaper_research.md`。本章只保留和技术决策相关的要点。

| 竞品 | 平台 | 技术要点 | 依据 |
|---|---|---|---|
| Wallpaper Engine | Windows | 视频走 **Media Foundation**；循环卡顿官方归因于 Windows/驱动；可选 LAV/DirectShow；重心在 Scene 编辑器 | 【文档】help.wallpaperengine.io/en/videos/lav.html；Steam 讨论帖里开发者的回复 |
| Lively | Windows | mpv/VLC/WMF 多种播放器；WebView2/CEF；4 种暂停算法；Watchdog 进程 | 【源码】rocksdanister/lively |
| Plash | macOS | 网页壁纸；**已闭源** | 【文档】README |
| LiveDesk / MacLiveEngine / Vantage / phonto | macOS | AVPlayerLayer；desktop level（或 -1）；CGWindowList 遮挡检测；锁屏靠替换 Aerial 文件或做屏保 | 【文档/源码】各自 README，phonto 的 `src/backend/macos/mod.rs` |
| Fresco / mpvpaper / Hanabi | Linux | mpv 硬解；layer-shell；GNOME 扩展 | 【源码】 |

**和性能相关的差异化机会**【推断】：
- 目前没有一款同时覆盖 Windows 和 macOS、并且公开功耗基准的产品。Fresco 是唯一公开过 turbostat 功耗数据的（只有 Linux）。
- 可以把“每块屏幕的实时功耗或资源徽章”和“导入时自动转码到最优格式”做成卖点。
