// 在没有 GNOME Shell 的 box 上验证扩展的 D-Bus 侧：与 extension.js 相同的 makeProxyWrapper / HelloRemote /
// connectSignal / proxy.call(GLib.Variant) 用法，对接 Go 守护进程（linux-dbus 的 wpdbus daemon）。
// 用法：dbus-run-session -- sh -c 'wpdbus daemon & sleep 1; gjs -m gjs_proxy_test.js <xml>'
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import System from 'system';

const xmlPath = ARGV[0];
const [, bytes] = Gio.File.new_for_path(xmlPath).load_contents(null);
const xml = new TextDecoder().decode(bytes);
let Proxy;
try {
    Proxy = Gio.DBusProxy.makeProxyWrapper(xml);
} catch (e) {
    print(`XML_REJECTED_BY_GIO: ${e.message}`);
    System.exit(3);
}
const loop = new GLib.MainLoop(null, false);
let ok = 0;
const fail = m => { print(`FAIL ${m}`); System.exit(1); };
const proxy = new Proxy(Gio.DBus.session, 'io.github.wpd.Daemon', '/io/github/wpd/Shell1', (p, err) => {
    if (err) fail(err.message);
    print(`ProtocolVersion(cached prop) = ${p.ProtocolVersion}`);
    p.connectSignal('StartRenderer', (_p, _s, [monitor, title, argv]) => {
        print(`signal StartRenderer monitor=${monitor} title=${title} argv0=${argv[0]} argc=${argv.length}`);
        ok++;
        const call = (m, v) => new Promise((res, rej) => p.call(m, v, Gio.DBusCallFlags.NONE, 1000, null,
            (pp, r) => { try { pp.call_finish(r); res(); } catch (e) { rej(e); } }));
        call('ReportMonitors', new GLib.Variant('(a(isiiiidb))', [[[0, 'eDP-1', 0, 0, 2560, 1600, 1.5, true]]]))
            .then(() => call('ReportVisibility', GLib.Variant.new('(isb)', [0, 'Overview', true])))
            .then(() => call('RendererStarted', GLib.Variant.new('(iub)', [0, 1234, true])))
            .then(() => call('RendererExited', GLib.Variant.new('(ii)', [0, 0])))
            .then(() => { print('calls OK'); ok++; loop.quit(); })
            .catch(e => fail(e.message));
    });
    p.HelloRemote('48.4', 'wayland', 1, (result, error) => {
        if (error) fail(error.message);
        print(`Hello -> ${result[0]} enabled=${result[1]}`);
        ok++;
    });
});
GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 5, () => fail('timeout'));
loop.run();
print(ok === 3 ? 'GJS_PROXY_TEST OK' : `partial ${ok}`);
