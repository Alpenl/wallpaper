// WPD 最小 GNOME Shell 扩展（GNOME 46–48，ESM）。原型，不是产品代码。
//
// 职责（见 05 §5.3 与 linux-dbus/README.md）：
//   1. 通过 D-Bus 代理（由 io.github.wpd.Shell1.xml 生成）与 Go 守护进程通信；
//   2. 收到 StartRenderer 后，用 Meta.WaylandClient 以“受信任客户端”身份启动 mpv（参照 Hanabi / DING）；
//   3. mpv 窗口出现后：从窗口列表隐藏、最小化，并用 Clutter.Clone 把它的画面接到对应显示器的背景 actor 上；
//   4. 上报显示器、全屏/概览可见性、渲染器启动与退出。
//
// API 差异【源码，mutter 头文件】：mutter 46–48 是 new Meta.WaylandClient(context, launcher) + spawnv(display, argv)，
// 并提供 hide_from_window_list()；mutter 49 起改为 Meta.WaylandClient.new_subprocess(context, launcher, argv)，
// 且去掉了 hide_from_window_list/make_desktop。这里做了特性检测，但只声明支持 46–48。

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Meta from 'gi://Meta';
import Clutter from 'gi://Clutter';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as Config from 'resource:///org/gnome/shell/misc/config.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const BUS_NAME = 'io.github.wpd.Daemon';
const OBJ_PATH = '/io/github/wpd/Shell1';
const PROTOCOL = 1;

class Renderer {
    constructor(ext, monitor, title, argv) {
        this._ext = ext;
        this.monitor = monitor;
        this.title = title;
        this.client = null;
        this.subprocess = null;
        this.window = null;
        this.clone = null;
        this._cancellable = new Gio.Cancellable();
        this._spawn(argv);
    }

    _spawn(argv) {
        const launcher = new Gio.SubprocessLauncher({flags: Gio.SubprocessFlags.NONE});
        if (typeof Meta.WaylandClient.new_subprocess === 'function') {
            // mutter 49+
            this.client = Meta.WaylandClient.new_subprocess(global.context, launcher, argv);
            this.subprocess = this.client.get_subprocess();
        } else {
            // mutter 46–48
            this.client = new Meta.WaylandClient(global.context, launcher);
            this.subprocess = this.client.spawnv(global.display, argv);
        }
        launcher.close?.();
        const pid = Number(this.subprocess?.get_identifier() ?? 0);
        this.subprocess?.wait_async(this._cancellable, (proc, res) => {
            let status = -1;
            try {
                proc.wait_finish(res);
                status = proc.get_if_exited() ? proc.get_exit_status() : -1;
            } catch (e) {
                if (e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.CANCELLED))
                    return;
            }
            this._unclone();
            this._ext.call('RendererExited', GLib.Variant.new('(ii)', [this.monitor, status]));
        });
        this._pid = pid;
    }

    owns(window) {
        try {
            return this.client !== null && this.client.owns_window(window);
        } catch {
            return false;
        }
    }

    adopt(window, actor) {
        this.window = window;
        this.client.hide_from_window_list?.(window); // 46–48；49+ 需要另行过滤（Hanabi 的 get_window_actors 覆盖）
        window.minimize();
        window.stick?.();
        this._minId = window.connect('notify::minimized', () => {
            if (!window.minimized)
                window.minimize();
        });
        this._cloneInto(actor);
    }

    _cloneInto(actor) {
        const bg = this._ext.backgroundActorFor(this.monitor);
        if (!bg) {
            log(`[wpd] no background actor for monitor ${this.monitor}`);
            return;
        }
        const geo = global.display.get_monitor_geometry(this.monitor);
        this.clone = new Clutter.Clone({source: actor, width: geo.width, height: geo.height});
        bg.add_child(this.clone);
        this._ext.call('RendererStarted', GLib.Variant.new('(iub)', [this.monitor, this._pid, true]));
    }

    _unclone() {
        if (this.clone) {
            this.clone.destroy();
            this.clone = null;
        }
    }

    // 背景 actor 被 GNOME 重建时（换壁纸、显示器变化）重新挂上
    reclone() {
        const actor = this.window?.get_compositor_private();
        if (actor) {
            this._unclone();
            this._cloneInto(actor);
        }
    }

    destroy() {
        this._cancellable.cancel();
        this._unclone();
        if (this.window && this._minId)
            this.window.disconnect(this._minId);
        this.subprocess?.force_exit();
        this.client = null;
    }
}

export default class WpdShellExtension extends Extension {
    enable() {
        this._renderers = new Map();
        this._signals = [];
        this._proxySignals = [];

        const [, bytes] = this.dir.get_child('io.github.wpd.Shell1.xml').load_contents(null);
        const xml = new TextDecoder().decode(bytes);
        const Proxy = Gio.DBusProxy.makeProxyWrapper(xml);
        this._proxy = new Proxy(Gio.DBus.session, BUS_NAME, OBJ_PATH, (proxy, error) => {
            if (error) {
                log(`[wpd] proxy error: ${error.message}`);
                return;
            }
            this._onProxyReady();
        }, null, Gio.DBusProxyFlags.DO_NOT_AUTO_START);

        this._proxySignals.push(this._proxy.connectSignal('StartRenderer', (_p, _s, [monitor, title, argv]) => {
            this._renderers.get(monitor)?.destroy();
            this._renderers.set(monitor, new Renderer(this, monitor, title, argv));
        }));
        this._proxySignals.push(this._proxy.connectSignal('StopRenderer', (_p, _s, [monitor]) => {
            this._renderers.get(monitor)?.destroy();
            this._renderers.delete(monitor);
        }));
        this._proxySignals.push(this._proxy.connectSignal('ShowSnapshot', (_p, _s, [monitor, _png]) => {
            // 原型：移除 Clone 即露出 GNOME 自己的背景；产品中应加载 png 作为 Clutter.Image
            this._renderers.get(monitor)?._unclone();
        }));
        this._nameOwner = this._proxy.connect('notify::g-name-owner', () => {
            if (this._proxy.g_name_owner)
                this._onProxyReady();
        });

        // 渲染器窗口出现
        this._signals.push([global.window_manager, global.window_manager.connect_after('map', (_wm, actor) => {
            const win = actor.get_meta_window();
            for (const r of this._renderers.values()) {
                if (r.window === null && r.owns(win) && (win.get_title() ?? '').includes(r.title)) {
                    r.adopt(win, actor);
                    break;
                }
            }
        })]);
        // 可见性：全屏、概览
        this._signals.push([global.display, global.display.connect('in-fullscreen-changed', () => this._reportFullscreen())]);
        this._signals.push([Main.overview, Main.overview.connect('showing', () => this._reportAll('Overview', true))]);
        this._signals.push([Main.overview, Main.overview.connect('hidden', () => this._reportAll('Overview', false))]);
        // 显示器与背景重建
        this._signals.push([Main.layoutManager, Main.layoutManager.connect('monitors-changed', () => this._reportMonitors())]);
        this._signals.push([Main.layoutManager, Main.layoutManager.connect('startup-complete', () => this._recloneAll())]);
        this._bgSignals = [];
        this._watchBackgrounds();
    }

    disable() {
        for (const [obj, id] of this._signals)
            obj.disconnect(id);
        this._signals = [];
        this._unwatchBackgrounds();
        for (const id of this._proxySignals)
            this._proxy?.disconnectSignal(id);
        this._proxySignals = [];
        if (this._nameOwner)
            this._proxy?.disconnect(this._nameOwner);
        for (const r of this._renderers.values())
            r.destroy();
        this._renderers.clear();
        this._proxy = null;
    }

    call(method, params) {
        if (!this._proxy?.g_name_owner)
            return;
        this._proxy.call(method, params, Gio.DBusCallFlags.NONE, 1000, null, (p, res) => {
            try {
                p.call_finish(res);
            } catch (e) {
                log(`[wpd] ${method} failed: ${e.message}`);
            }
        });
    }

    _onProxyReady() {
        const session = Meta.is_wayland_compositor() ? 'wayland' : 'x11';
        this._proxy.HelloRemote(Config.PACKAGE_VERSION, session, PROTOCOL, (result, error) => {
            if (error) {
                log(`[wpd] Hello failed: ${error.message}`);
                return;
            }
            const [daemonVersion, enabled] = result;
            log(`[wpd] daemon ${daemonVersion} enabled=${enabled}`);
            this._reportMonitors();
            this._reportFullscreen();
        });
    }

    _reportMonitors() {
        const mons = Main.layoutManager.monitors.map(m => [
            m.index, m.connector ?? '', m.x, m.y, m.width, m.height, m.geometry_scale ?? 1.0,
            m.index === Main.layoutManager.primaryIndex,
        ]);
        this.call('ReportMonitors', new GLib.Variant('(a(isiiiidb))', [mons]));
        this._watchBackgrounds();
    }

    _reportFullscreen() {
        const n = global.display.get_n_monitors();
        for (let i = 0; i < n; i++)
            this.call('ReportVisibility', GLib.Variant.new('(isb)', [i, 'Fullscreen', global.display.get_monitor_in_fullscreen(i)]));
    }

    _reportAll(reason, active) {
        const n = global.display.get_n_monitors();
        for (let i = 0; i < n; i++)
            this.call('ReportVisibility', GLib.Variant.new('(isb)', [i, reason, active]));
    }

    // GNOME 46–48：Main.layoutManager._bgManagers[i].backgroundActor 是该显示器桌面背景（私有字段，版本升级需复核）
    backgroundActorFor(monitor) {
        const mgr = Main.layoutManager._bgManagers?.[monitor];
        return mgr?.backgroundActor ?? null;
    }

    _watchBackgrounds() {
        this._unwatchBackgrounds();
        for (const mgr of Main.layoutManager._bgManagers ?? [])
            this._bgSignals.push([mgr, mgr.connect('changed', () => this._recloneAll())]);
    }

    _unwatchBackgrounds() {
        for (const [obj, id] of this._bgSignals ?? [])
            obj.disconnect(id);
        this._bgSignals = [];
    }

    _recloneAll() {
        // BackgroundManager 的 'changed' 在新背景 actor 淡入后发出；延后一帧再挂
        GLib.idle_add(GLib.PRIORITY_DEFAULT_IDLE, () => {
            for (const r of this._renderers.values())
                r.reclone();
            return GLib.SOURCE_REMOVE;
        });
    }
}
