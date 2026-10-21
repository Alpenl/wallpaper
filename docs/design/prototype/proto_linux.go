//go:build linux

// Linux（X11 / XWayland 之外的原生 X11 会话）兼容参考：纯 Go xgb 建 _NET_WM_WINDOW_TYPE_DESKTOP 窗口 + mpv --wid。
package main

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func osName() string { return "linux-x11" }

func atom(c *xgb.Conn, name string) xproto.Atom {
	r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
	if err != nil {
		logf("ATOM_ERR %s %v", name, err)
		return 0
	}
	return r.Atom
}

func run() {
	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		logf("WARN 当前是 Wayland 会话：X11 桌面窗口只会作为 XWayland 普通窗口出现，不会成为壁纸（05 章）")
	}
	c, err := xgb.NewConn()
	if err != nil {
		logf("FATAL X11 连接失败: %v", err)
		return
	}
	defer c.Close()
	scr := xproto.Setup(c).DefaultScreen(c)
	w, h := scr.WidthInPixels, scr.HeightInPixels
	wid, _ := xproto.NewWindowId(c)
	xproto.CreateWindow(c, scr.RootDepth, wid, scr.Root, 0, 0, w, h, 0,
		xproto.WindowClassInputOutput, scr.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask, []uint32{0x000000, xproto.EventMaskStructureNotify})
	typ := atom(c, "_NET_WM_WINDOW_TYPE")
	desk := atom(c, "_NET_WM_WINDOW_TYPE_DESKTOP")
	xproto.ChangeProperty(c, xproto.PropModeReplace, wid, typ, xproto.AtomAtom, 32, 1, u32le(uint32(desk)))
	st := atom(c, "_NET_WM_STATE")
	var states []byte
	for _, n := range []string{"_NET_WM_STATE_BELOW", "_NET_WM_STATE_STICKY", "_NET_WM_STATE_SKIP_TASKBAR", "_NET_WM_STATE_SKIP_PAGER"} {
		states = append(states, u32le(uint32(atom(c, n)))...)
	}
	xproto.ChangeProperty(c, xproto.PropModeReplace, wid, st, xproto.AtomAtom, 32, uint32(len(states)/4), states)
	title := "wpproto-desktop"
	xproto.ChangeProperty(c, xproto.PropModeReplace, wid, xproto.AtomWmName, xproto.AtomString, 8, uint32(len(title)), []byte(title))
	xproto.MapWindow(c, wid)
	xproto.ConfigureWindow(c, wid, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeBelow})
	c.Sync()
	logf("WINDOW xid=0x%x size=%dx%d type=_NET_WM_WINDOW_TYPE_DESKTOP", uint32(wid), w, h)

	sock := filepath.Join(os.TempDir(), "wpproto-mpv-"+strconv.Itoa(os.Getpid())+".sock")
	hw := *flagHwdec
	if hw == "" {
		hw = "auto-safe"
	}
	extra := []string{}
	if v := os.Getenv("WPPROTO_MPV_VO"); v != "" { // 无 GPU 的 Xvfb 用 WPPROTO_MPV_VO=x11
		extra = append(extra, "--vo="+v)
	}
	cmd := exec.Command(*flagMpv, mpvArgs(uintptr(wid), sock, hw, extra...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		logf("FATAL mpv 启动失败: %v", err)
		return
	}
	defer cmd.Process.Kill()
	var conn net.Conn
	for i := 0; i < 50; i++ {
		if conn, err = net.Dial("unix", sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		logf("FATAL IPC 连接失败: %v", err)
		return
	}
	mc := newMpvClient(conn)
	stop := make(chan struct{})
	go statsLoop(mc, *flagStats, func() bool { return false }, func() { logf("（Linux 原型不做重启）") }, stop)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	var timeout <-chan time.Time
	if *flagDuration > 0 {
		timeout = time.After(*flagDuration)
	}
	select {
	case <-sig:
	case <-timeout:
	}
	close(stop)
	mc.cmd("quit")
	cmd.Wait()
}

func u32le(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
