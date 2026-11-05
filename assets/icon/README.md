# 应用图标

层叠的壁纸卡片、山景和播放符号：卡片表示壁纸库，播放符号表示动态壁纸。
深海军蓝、亮蓝与米白为主色，与原生界面的蓝色强调色属于同一色系。

## 文件

| 路径 | 用途 |
|---|---|
| `wallpaper-source.png` | 1024×1024 RGBA 母版，圆角外透明；所有平台资产由它生成 |
| `windows/icon-<size>.png` | 16–256 的 Windows 帧，供 `winres.json` 和 go-winres 使用 |
| `wallpaper.ico` | 9 帧 ICO：16/20/24/32/40/48/64 为 32 位 DIB，128/256 为 PNG |
| `AppIcon.icns` | macOS Big Sur 比例（内容 824/1024）的 11 个 PNG 元素 |
| `winres.json` | go-winres 配置，只嵌入图标组，不覆盖 manifest 或版本信息 |

16/20/24/32 px 的帧由 `scripts/build-icons.py` 用几何绘制的小尺寸母版生成：单峰山形加放大的
播放符号，并沿用主图的底部蓝色光晕，避免缩小后两个山峰粘成两块；40 px 以上使用 1024 母版。
macOS 的 16/32/64 元素同样使用简化版，128 以上使用主图。

`cmd/wallpaperd/rsrc_windows_amd64.syso` 和 `rsrc_windows_arm64.syso` 由
`assets/icon/winres.json` 生成，随 Go 链接器进入 Windows 可执行文件。文件名中的
`_windows_<arch>` 是 Go 的隐式构建约束，因此 darwin 构建不会读取它们。

## 再生成

需要 [Pillow](https://python-pillow.org/)（只在重新生成图标时需要，发布构建不依赖）
和 [go-winres](https://github.com/tc-hib/go-winres)。本仓库资产由 Pillow 12.3.0 与
go-winres v0.3.3（winres v0.2.1）生成；go-winres 用 Go 的 PNG 编码器重新编码帧，
换 Go 版本可能改变 `.syso` 字节，但发布只使用已提交的 `.syso`。

```sh
python scripts/build-icons.py
go-winres make --in assets/icon/winres.json --arch amd64,arm64 --out cmd/wallpaperd/rsrc
python scripts/check-icons.py
```

`python3 scripts/check-icons.py` 已加入 `scripts/check.sh` 和 `scripts/release.sh`
（后者在 `SKIP_TESTS=1` 时仍执行）。它只用标准库校验 PNG/ICO/ICNS 结构、ICO 帧的
DIB/PNG 分布、ICNS 各元素的尺寸，以及两个 `.syso` 的机器类型、重定位、图标组和
内嵌 PNG 尺寸。

## 验证边界

已在本机验证：`check-icons.py` 通过；windows/amd64、windows/arm64、darwin/amd64、
darwin/arm64、linux/amd64 交叉编译通过；Windows PE 含 9 个 `RT_ICON` 和 1 个
`RT_GROUP_ICON`，`ExtractAssociatedIcon` 能取出 32×32 图标；`go test ./...` 与
11 项发布流水线测试通过。未验证：真实
资源管理器/任务栏/Finder 外观、NSIS 安装器的实际构建与运行（本机无 `makensis`）、
macOS `iconutil` 与签名/公证。

## 集成

- Windows：`.syso` 让 `wallpaperd.exe` 在资源管理器、任务栏和快捷方式中显示图标；
  `scripts/package-windows.sh` 把 `wallpaper.ico` 传给 NSIS，安装器和卸载器使用同一图标
  （`Icon`、`UnIcon`、`MUI_ICON`、`MUI_UNICON`）。
- macOS：`scripts/package-macos.sh` 把 `AppIcon.icns` 复制到
  `Wallpaper.app/Contents/Resources/`，`Info.plist` 的 `CFBundleIconFile` 指向它。
- 托盘：Windows 托盘图标仍在 `internal/ui/shell.go` 运行时绘制（32×32，8× 超采样），
  与主图标同构；macOS 保留单色 template 菜单栏图标。

## 来源

2026-11-05 使用内部 magpie 生图服务（`gpt-image-2`）生成候选，人工选定层叠壁纸卡片方向，
再做去背景、统一圆角和尺寸导出。原始候选和过程文件不进入仓库。
