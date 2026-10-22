# 本轮原生 UI 截图

生成记录：2026-10-21 16:45 UTC。来源是已核对主分支基线 `2710033e` 上的本轮原生工作区，使用 `cmd/uishots` 与 MyGo 软件绘制测试器，**不是 Windows/macOS 真机桌面截图，也没有使用 React 应用或浏览器**。这里的图片已替换 16:06 的早期布局检查点图。

本次检查覆盖七页中文/英文、浅色/深色，以及小窗口滚动与控件点击。完整预览每种语言 19 张，另有 16 张针对状态的 QA 图；本目录保留 10 张代表图和 4 张状态图。旧 `docs/screenshots/` 根目录图片保持原样，属于历史十页 UI。

## 中文，逻辑 1120×900，scale=1.5

PNG 实际像素为 1680×1350。

| 页面 | 本轮代表截图 |
|---|---|
| UI-01 壁纸库 | [浅色](zh/01-library-light.png) |
| UI-02 显示器 | [浅色](zh/02-displays-light.png) |
| UI-03 播放与性能 | [深色](zh/03-playback-dark.png) |
| UI-04 暂停与电源 | [浅色](zh/04-rules-light.png) |
| UI-05 通用 | [深色](zh/05-general-dark.png) |
| UI-06 版本管理 | [浅色](zh/06-versions-light.png) |
| UI-07 关于与诊断 | [浅色](zh/07-about-light.png) |

## English，逻辑 900×680，scale=1.5

PNG 实际像素为 1350×1020。

- [Library / light](en/01-library-light.png)
- [Playback / dark](en/03-playback-dark.png)
- [General / dark](en/05-general-dark.png)

## 状态与滚动检查，scale=1

- [中文跨屏控件，滚动后 900×680](states/zh-CN-light-01-span-scrolled.png)：真实模拟滚动与点击，检查开启/关闭跨屏后保留独立分配。
- [中文 macOS 编码设置，滚动后 900×680](states/zh-CN-light-02-mac-encoder-scrolled.png)：原生 helper、可选 ffmpeg、目标尺寸控件与持久化路由。
- [English macOS limited fallback，1120×1700](states/en-light-03-mac-fallback-full.png)：有限回退不能承诺固定 FPS/去音轨的提示。
- [中文本地指标演示，1120×1700](states/zh-CN-dark-04-demo-metrics-full.png)：输入的是测试计数，显示未知硬解及“不是节电量”的边界。

## 证据边界

示例缩略图由程序生成；显示器、素材、路径、解码状态、更新信息与指标均来自测试控制器。尤其“本周暂停时长”的图中数值只是测试输入，不是用户实际记录或硬件功耗测量。正式运行界面读取控制器真实指标，与截图 fixture 分开。

本次 QA 验证系统生成文案的中英文、数字步进控件不越界、取消删除、错误反馈、平台差异和滚动后的交互。导入标题/说明、未知来源消息和用户自定义屏幕名保留来源语言。长截图展示相关区段，不表示所有长页都能在普通窗口中不滚动显示。

本次没有验证真实桌面挂载、托盘、物理屏幕排列、跨屏视频、硬解、睡眠/电源、原生编解码与续播、签名/公证或设备辅助功能。界面正确显示后端能力不等于后端已在真实平台通过测试。完整范围见 [实现矩阵](../../IMPLEMENTATION_STATUS.md) 与 [验证记录](../../VALIDATION.md)。

## 重现

在具备项目工具链和运行库的环境中，从仓库根目录输出到独立目录，检查后再替换文档图：

```sh
go run ./cmd/uishots -out /tmp/wallpaper-ui-zh -scale 1.5 -width 1120 -height 900
go run ./cmd/uishots -out /tmp/wallpaper-ui-en -scale 1.5 -lang en -width 900 -height 680
WALLPAPER_QA_SCREENSHOTS=/tmp/wallpaper-ui-states WALLPAPER_QA_THUMBS=/tmp/wallpaper-ui-zh/.thumbs go test ./internal/ui -run TestSmallWindowNativeControlsAndPreviewStates -count=1
```

主图库默认模拟 Windows 控件能力，可用 `-goos` 改变配置；状态测试覆盖 macOS 控件。模拟平台仍不是原生平台运行。测试入口见 [`native_qa_test.go`](../../../internal/ui/native_qa_test.go)。
