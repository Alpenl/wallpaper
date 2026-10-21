# 最小 GNOME Shell 扩展（第 17 轮，GNOME 46–48）

目录：`wpd-shell@wpd.github.io/`（`metadata.json`、`extension.js`、`io.github.wpd.Shell1.xml`）。原型，未在真实 GNOME Shell 中运行过。

## 做了什么
- 用 `Gio.DBusProxy.makeProxyWrapper(xml)` 从 `linux-dbus/` 的接口 XML 生成代理；`Hello` → 上报显示器、全屏、概览状态；订阅 `StartRenderer/StopRenderer/ShowSnapshot`。
- 收到 `StartRenderer` 后用 **Meta.WaylandClient** 启动 mpv（受信任客户端，参照 Hanabi `waylandSubprocess.ts`、DING）；`map` 时用 `owns_window()` + 标题认领窗口，`hide_from_window_list()`、最小化并保持最小化（Hanabi `windowManager.ts` 的做法），再用 `Clutter.Clone` 接到 `Main.layoutManager._bgManagers[i].backgroundActor`；背景重建（`BackgroundManager` 的 `changed` 信号）时重新挂上。
- 渲染器退出 → `RendererExited`。

## 版本差异【源码】
| | mutter 46–48 | mutter 49+ |
|---|---|---|
| 启动 | `new Meta.WaylandClient(context, launcher)` + `spawnv(display, argv)` | `Meta.WaylandClient.new_subprocess(context, launcher, argv)` + `get_subprocess()` |
| 隐藏窗口 | `hide_from_window_list()`、`make_desktop()` 可用 | 两者都已移除 → 需要像 Hanabi 那样覆盖 `global.get_window_actors` |
依据：mutter `src/meta/meta-wayland-client.h` 46.0 / 47.0 / 48.0 / 49.0 标签。`_bgManagers`、`backgroundActor`、`changed` 信号核对自 gnome-shell 46.0 与 48.0 的 `js/ui/layout.js:323/511`、`js/ui/background.js:718/742`（48.0 行号 −1/−2）。扩展对 49+ 做了特性检测，但 metadata 只声明 46–48。

## box 上的静态/半动态检查（2026-10-21）【实测】
| 检查 | 结果 |
|---|---|
| `node --check`（ESM 语法） | 通过 |
| ESLint 9（`eslint.config.mjs`：GJS 全局 + no-undef/no-unused-vars/prefer-const/eqeqeq） | 0 错误 0 警告 |
| `check.py`：metadata 字段与 45+ 规则（uuid=目录名、shell-version 为主版本号字符串）、XML 良构、JS 调用的方法/信号都在 XML 中且 **GVariant 签名一致**、XML 中的信号都被处理 | 通过 |
| `test/gjs_proxy_test.js`：用 **gjs 1.82.3** 与 extension.js 相同的 makeProxyWrapper / HelloRemote / connectSignal / call(GLib.Variant) 用法，对接 Go 守护进程（godbus） | `GJS_PROXY_TEST OK`：Hello、StartRenderer 信号、4 个方法全通 |
- 发现并修正：第 13 轮的 XML 注释里有 `--`（来自 `mpv --title=...`），**严格 XML 解析器（Python ElementTree）拒绝**；GLib 的 GMarkup 较宽松，GJS 仍可用。已改注释。
- 无法在 box 上验证：`Meta`/`Main`/`Clutter` 相关逻辑（需要 GNOME Shell 进程）。见 `bench/DEVICE_HANDOFF.md` 6.2。

运行检查：
```
python3 check.py
npx eslint wpd-shell@wpd.github.io/extension.js    # 需在本目录安装 eslint@9
dbus-run-session -- sh -c '../linux-dbus/wpdbus daemon & sleep 1; gjs -m test/gjs_proxy_test.js ../linux-dbus/io.github.wpd.Shell1.xml'
```
