# GNOME 扩展 ↔ Go 守护进程 D-Bus 接口（Q-L6，第 13 轮）

- 接口定义：[io.github.wpd.Shell1.xml](io.github.wpd.Shell1.xml)。会话总线，守护进程持有 `io.github.wpd.Daemon`，对象 `/io/github/wpd/Shell1`。
- 方向：**扩展 → 守护进程用方法**（Hello / ReportMonitors / ReportVisibility / RendererStarted / RendererExited）；**守护进程 → 扩展用信号**（StartRenderer / StopRenderer / ShowSnapshot）。暂停/恢复不经过扩展，由守护进程直接走 mpv IPC。
- 为什么由扩展拉起渲染器：GNOME Wayland 下只有通过 `Meta.WaylandClient` 启动的客户端，扩展才能可靠识别其窗口并 Clone 到背景层（Hanabi 的做法，见 05 §5.3）【源码，Hanabi】。
- 版本：属性 `ProtocolVersion`（u）；`Hello` 携带扩展的协议版本，不匹配时返回 `io.github.wpd.Error.Protocol` 错误。
- 选型：`github.com/godbus/dbus/v5` v5.2.4（纯 Go，`CGO_ENABLED=0` 可编译）。

> 第 17 轮修正：XML 注释里原有 `--`（`mpv --title=...`），违反 XML 规范，严格解析器会拒绝；GMarkup（GJS）较宽松未受影响。已改写注释。GJS 端联调结果见 `gnome-ext/README.md`。

## box 实测（2026-10-21，Debian 13，dbus-daemon 会话总线）【实测】
```
go build && dbus-run-session -- ./wpdbus selftest
```
结果：属性读取、Hello（含 StartRenderer 信号回送）、`a(isiiiidb)` 显示器数组、可见性上报、协议版本错误、内省全部通过（输出 `SELFTEST OK`）。
另用非 Go 客户端 `dbus-send` 调用 `ReportVisibility int32:1 string:Covered boolean:true` 成功，内省结果含 5 个方法 + 3 个信号——说明接口不依赖 godbus 自身的编解码（GJS 侧走同一线协议）。
未验证：真实 GJS 扩展（`Gio.DBusProxy.makeProxyWrapper(xml)`）调用——box 没有 GNOME Shell，需真机（见 bench/DEVICE_HANDOFF.md 的 L-2）。
