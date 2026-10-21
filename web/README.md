# Wallpaper UI 原型（v1）

`wallpaper` 桌面应用设置界面与托盘面板的可交互 Web 原型，用于界面设计与评审。
信息架构和配置项参照 `internal/ui/` 与 `docs/design/`。

## 内容

- 设置页：壁纸库、显示器、播放与性能、暂停与电源、通用、版本管理、关于与诊断
- 托盘面板
- 明暗主题切换、中文 / English 切换
- 响应式布局（宽屏桌面到手机宽度）

全部为前端模拟数据，不连接 Go 后端。

## 运行

```sh
cd web
pnpm install   # 或 npm install
pnpm dev
pnpm build     # 类型检查并输出到 dist/
```

## 结构

- `src/App.tsx`：页面、组件、图标与 i18n 文案
- `src/index.css`：设计令牌（明暗主题变量）与组件样式
