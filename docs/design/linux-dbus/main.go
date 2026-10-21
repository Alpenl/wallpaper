// 用 godbus/dbus/v5（纯 Go）实现 io.github.wpd.Shell1 的守护进程侧，并自测：
//
//	go run . daemon   —— 导出对象，打印收到的调用
//	go run . selftest —— 在同一总线上起 daemon + 模拟扩展客户端，验证方法、属性、信号、内省
package main

import (
	_ "embed"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

//go:embed io.github.wpd.Shell1.xml
var ifaceXML string

const (
	busName = "io.github.wpd.Daemon"
	objPath = dbus.ObjectPath("/io/github/wpd/Shell1")
	iface   = "io.github.wpd.Shell1"
)

type Monitor struct {
	Index      int32
	Connector  string
	X, Y, W, H int32
	Scale      float64
	Primary    bool
}

type daemon struct{ conn *dbus.Conn }

func (d *daemon) Hello(shellVer, session string, proto uint32) (string, bool, *dbus.Error) {
	fmt.Printf("[daemon] Hello shell=%s session=%s proto=%d\n", shellVer, session, proto)
	if proto != 1 {
		return "", false, dbus.NewError("io.github.wpd.Error.Protocol", []any{"unsupported protocol"})
	}
	// 收到 Hello 后指示扩展为 0 号屏启动渲染器
	go d.conn.Emit(objPath, iface+".StartRenderer", int32(0), "wpd-renderer-0",
		[]string{"mpv", "--title=wpd-renderer-0", "--input-ipc-server=/run/user/1000/wpd-0.sock", "--loop-file=inf", "/path/video.mp4"})
	return "0.1.0", true, nil
}
func (d *daemon) ReportMonitors(m []Monitor) *dbus.Error {
	fmt.Printf("[daemon] ReportMonitors %+v\n", m)
	return nil
}
func (d *daemon) ReportVisibility(mon int32, reason string, active bool) *dbus.Error {
	fmt.Printf("[daemon] ReportVisibility mon=%d reason=%s active=%v\n", mon, reason, active)
	return nil
}
func (d *daemon) RendererStarted(mon int32, pid uint32, cloned bool) *dbus.Error {
	fmt.Printf("[daemon] RendererStarted mon=%d pid=%d cloned=%v\n", mon, pid, cloned)
	return nil
}
func (d *daemon) RendererExited(mon int32, status int32) *dbus.Error {
	fmt.Printf("[daemon] RendererExited mon=%d status=%d\n", mon, status)
	return nil
}

func serve(conn *dbus.Conn) error {
	d := &daemon{conn: conn}
	if err := conn.Export(d, objPath, iface); err != nil {
		return err
	}
	if _, err := prop.Export(conn, objPath, prop.Map{iface: {"ProtocolVersion": {Value: uint32(1)}}}); err != nil {
		return err
	}
	if err := conn.Export(introspect.Introspectable(ifaceXML), objPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	r, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || r != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("name taken: %v %v", r, err)
	}
	return nil
}

func must(err error) {
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
}

func main() {
	mode := "selftest"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	srv, err := dbus.ConnectSessionBus()
	must(err)
	must(serve(srv))
	if mode == "daemon" {
		fmt.Println("[daemon] ready on", busName)
		select {}
	}
	// ---- 模拟 GNOME 扩展客户端（独立连接） ----
	cli, err := dbus.ConnectSessionBus()
	must(err)
	must(cli.AddMatchSignal(dbus.WithMatchObjectPath(objPath), dbus.WithMatchInterface(iface)))
	sig := make(chan *dbus.Signal, 4)
	cli.Signal(sig)
	obj := cli.Object(busName, objPath)

	var pv dbus.Variant
	must(obj.Call("org.freedesktop.DBus.Properties.Get", 0, iface, "ProtocolVersion").Store(&pv))
	fmt.Println("[ext] ProtocolVersion =", pv.Value())

	var ver string
	var enabled bool
	must(obj.Call(iface+".Hello", 0, "48.4", "wayland", uint32(1)).Store(&ver, &enabled))
	fmt.Println("[ext] Hello ->", ver, enabled)
	select {
	case s := <-sig:
		fmt.Printf("[ext] signal %s %v\n", s.Name, s.Body)
	case <-time.After(2 * time.Second):
		must(fmt.Errorf("no StartRenderer signal"))
	}
	must(obj.Call(iface+".ReportMonitors", 0, []Monitor{{0, "eDP-1", 0, 0, 2560, 1600, 1.5, true}, {1, "DP-2", 2560, 0, 3840, 2160, 1, false}}).Err)
	must(obj.Call(iface+".RendererStarted", 0, int32(0), uint32(4242), true).Err)
	must(obj.Call(iface+".ReportVisibility", 0, int32(0), "Fullscreen", true).Err)
	err = obj.Call(iface+".Hello", 0, "48.4", "wayland", uint32(9)).Err
	fmt.Println("[ext] Hello(proto=9) error as expected:", err)
	var xml string
	must(obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml))
	fmt.Println("[ext] introspection bytes:", len(xml))
	fmt.Println("SELFTEST OK")
}
