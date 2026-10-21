//go:build windows

package main

// 渲染后端：mpv.exe 子进程（mpv JSON IPC）或 mfhost 子进程（本程序 -mfhost，IPC v1）。
// 两者地位对等：都是独立进程、继承 Job Object、渲染到主进程的子 HWND —— 这样 W-T3b 的进程开销可比。
// 约定：start 阻塞直到就绪或失败；setLevel 0=播放 1=暂停 3=释放（保留进程）；kill 是“有意结束”，不会上报退出。
// 意外退出通过 onExit 回调（PostMessage 到 UI 线程）上报。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

type renderBackend interface {
	name() string
	start(hwnd uintptr, w, h int32, paused bool) error
	setLevel(l int) error
	kill()
}

func pickBackend(id int) renderBackend {
	exit := func() { pPostMessageW.Call(msgWnd, wmAppBackend, uintptr(id), beExited) }
	if *flagBackend == "mf" {
		return &mfProcBackend{id: id, onExit: exit}
	}
	return &mpvBackend{id: id, onExit: exit}
}

// ---------------- mpv ----------------
type mpvBackend struct {
	id      int
	onExit  func()
	mu      sync.Mutex
	cmd     *exec.Cmd
	c       *mpvClient
	stop    chan struct{}
	level   atomic.Int32
	killing *atomic.Bool // 每个进程实例一个
	pos     string
}

func (b *mpvBackend) name() string { return "mpv" }

func (b *mpvBackend) start(hwnd uintptr, w, h int32, paused bool) error {
	b.kill()
	pipe := fmt.Sprintf(`\\.\pipe\wpproto-%d-%d-%d`, os.Getpid(), b.id, time.Now().UnixNano()%1e6)
	hw := *flagHwdec
	if hw == "" {
		hw = "d3d11va"
	}
	cmd := exec.Command(*flagMpv, mpvArgs(hwnd, pipe, hw, "--vo=gpu-next", "--gpu-api=d3d11", fmt.Sprintf("--pause=%v", boolYesNo(paused)))...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mpv 启动失败: %v", err)
	}
	logf("MPV screen=%d pid=%d wid=0x%x pipe=%s", b.id, cmd.Process.Pid, hwnd, pipe)
	var f *os.File
	for i := 0; i < 50 && f == nil; i++ {
		pWaitNamedPipeW.Call(up(u16(pipe)), 100)
		hd, err := syscall.CreateFile(u16(pipe), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err == nil {
			f = os.NewFile(uintptr(hd), pipe)
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if f == nil {
		cmd.Process.Kill()
		return fmt.Errorf("命名管道连接失败")
	}
	killing := &atomic.Bool{}
	c := newMpvClient(f)
	stop := make(chan struct{})
	b.mu.Lock()
	b.cmd, b.c, b.stop, b.killing = cmd, c, stop, killing
	b.mu.Unlock()
	if paused {
		b.level.Store(1)
	} else {
		b.level.Store(0)
	}
	go statsLoop(c, *flagStats, func() bool { return b.level.Load() > 0 }, func() {
		logf("MPV screen=%d 卡住/IPC 断开 → 结束进程，交给 supervisor", b.id)
		cmd.Process.Kill() // 不置 killing：按意外退出上报
	}, stop)
	go func() {
		err := cmd.Wait()
		if !killing.Load() {
			logf("MPV_EXIT screen=%d %v", b.id, err)
			b.onExit()
		}
	}()
	return nil
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func (b *mpvBackend) setLevel(l int) error {
	b.mu.Lock()
	c := b.c
	b.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not running")
	}
	prev := int(b.level.Load())
	var err error
	switch {
	case l >= 3 && prev < 3:
		b.pos = c.getString("time-pos")
		_, err = c.cmd("stop") // --idle=yes：进程保留，解码器和视频输出释放
	case l < 3 && prev >= 3:
		_, err = c.cmd("loadfile", *flagVideo, "replace", -1, "start="+b.pos) // mpv 0.38+ 语法（index 参数）
		if err == nil {
			err = c.setPause(l > 0)
		}
	default:
		err = c.setPause(l > 0)
	}
	b.level.Store(int32(l))
	logf("MPV screen=%d level %d→%d pos=%s err=%v", b.id, prev, l, b.pos, err)
	return err
}

func (b *mpvBackend) kill() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd == nil {
		return
	}
	b.killing.Store(true)
	close(b.stop)
	b.c.cmd("quit")
	b.c.Close()
	b.cmd.Process.Kill()
	b.cmd, b.c = nil, nil
}

// ---------------- mfhost（IPC v1）----------------
var (
	pCreateNamedPipeW  = kernel32.NewProc("CreateNamedPipeW")
	pConnectNamedPipe  = kernel32.NewProc("ConnectNamedPipe")
	pGetCurrentSession = kernel32.NewProc("ProcessIdToSessionId")
)

type mfProcBackend struct {
	id      int
	onExit  func()
	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *ipcConn
	killing *atomic.Bool
}

func (b *mfProcBackend) name() string { return "mf(子进程, IPC v1)" }

// 02 章 2.3：\\.\pipe\wpd-<会话ID>-<16 字节随机 hex>，FIRST_PIPE_INSTANCE 防抢注。
// 原型用默认安全描述符（创建者与 SYSTEM/管理员可访问）；产品要显式设置只含当前用户 SID 的 DACL。
func newPipeServer() (string, syscall.Handle, error) {
	var sess uint32
	pGetCurrentSession.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&sess)))
	rb := make([]byte, 16)
	rand.Read(rb)
	name := fmt.Sprintf(`\\.\pipe\wpd-%d-%s`, sess, hex.EncodeToString(rb))
	h, _, err := pCreateNamedPipeW.Call(up(u16(name)), 0x3 /*DUPLEX*/ |0x00080000, /*FIRST_PIPE_INSTANCE*/
		0 /*BYTE|WAIT*/, 1, 64<<10, 64<<10, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return "", 0, fmt.Errorf("CreateNamedPipe: %v", err)
	}
	return name, syscall.Handle(h), nil
}

func (b *mfProcBackend) start(hwnd uintptr, w, h int32, paused bool) error {
	b.kill()
	name, ph, err := newPipeServer()
	if err != nil {
		return err
	}
	exe, _ := os.Executable()
	logPath := fmt.Sprintf("%s.mfhost%d.log", *flagLog, b.id)
	cmd := exec.Command(exe, "-mfhost", "-pipe", name, "-log", logPath, "-swap", *flagSwap, "-stats", flagStats.String())
	if err := cmd.Start(); err != nil {
		syscall.CloseHandle(ph)
		return fmt.Errorf("mfhost 启动失败: %v", err)
	}
	// 等子进程连上（5 s）。超时则结束子进程，并自己连一次把阻塞的 ConnectNamedPipe 放出来
	connected := make(chan bool, 1)
	go func() {
		r, _, e := pConnectNamedPipe.Call(uintptr(ph), 0)
		connected <- r != 0 || e == syscall.Errno(535) /*ERROR_PIPE_CONNECTED*/
	}()
	select {
	case ok := <-connected:
		if !ok {
			cmd.Process.Kill()
			syscall.CloseHandle(ph)
			return fmt.Errorf("ConnectNamedPipe 失败")
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		if hh, err := syscall.CreateFile(u16(name), syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0); err == nil {
			syscall.CloseHandle(hh)
		}
		<-connected
		syscall.CloseHandle(ph)
		return fmt.Errorf("E_TIMEOUT: mfhost 5 s 内未连接管道")
	}
	killing := &atomic.Bool{}
	conn := newIPC(os.NewFile(uintptr(ph), name), nil, func(m string, p json.RawMessage) {
		if m == "stats" {
			logf("MFSTATS screen=%d %s", b.id, p)
		} else {
			logf("MFHOST_EVT screen=%d %s %s", b.id, m, p)
		}
	})
	var hello struct {
		Pid   int
		Caps  []string
		InJob bool
	}
	if err := conn.call("hello", map[string]any{"proto": 1, "app": "wpproto"}, 5*time.Second, &hello); err != nil {
		cmd.Process.Kill()
		conn.Close()
		return fmt.Errorf("hello: %v", err)
	}
	logf("MFHOST screen=%d pid=%d caps=%v inJob=%v pipe=%s", b.id, hello.Pid, hello.Caps, hello.InJob, name)
	abs, _ := filepath.Abs(*flagVideo)
	load := map[string]any{"entry": abs, "wid": uint64(hwnd), "bounds": map[string]int32{"w": w, "h": h}, "paused": paused}
	if err := conn.call("load", load, 15*time.Second, nil); err != nil {
		cmd.Process.Kill()
		conn.Close()
		return fmt.Errorf("load: %v", err)
	}
	b.mu.Lock()
	b.cmd, b.conn, b.killing = cmd, conn, killing
	b.mu.Unlock()
	go func() {
		err := cmd.Wait()
		conn.Close()
		if !killing.Load() {
			logf("MFHOST_EXIT screen=%d %v", b.id, err)
			b.onExit()
		}
	}()
	return nil
}

func (b *mfProcBackend) setLevel(l int) error {
	b.mu.Lock()
	c := b.conn
	b.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not running")
	}
	err := c.call("setPause", map[string]int{"level": l}, 15*time.Second, nil)
	logf("MFHOST screen=%d setPause level=%d err=%v", b.id, l, err)
	return err
}

func (b *mfProcBackend) kill() {
	b.mu.Lock()
	cmd, c, k := b.cmd, b.conn, b.killing
	b.cmd, b.conn = nil, nil
	b.mu.Unlock()
	if cmd == nil {
		return
	}
	k.Store(true)
	c.call("shutdown", nil, 500*time.Millisecond, nil) // 02 章：宿主 2 s 内没退出就 TerminateProcess；原型等 1 s
	done := make(chan struct{})
	go func() { cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		cmd.Process.Kill()
	}
	c.Close()
}
