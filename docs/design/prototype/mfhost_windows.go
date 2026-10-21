//go:build windows

package main

// Media Foundation 渲染子进程（-mfhost）。与 mpv.exe 地位相同：
//   - 由主进程启动，继承主进程的 Job Object（KILL_ON_JOB_CLOSE），主进程死则它死；
//   - 通过 IPC v1（02 章 2.3）与主进程通信：主进程创建命名管道并把名字放在命令行上，本进程作为客户端连接；
//   - 渲染到主进程创建的子 HWND（跨进程绑定交换链，与 mpv --wid 相同）。
// 方法：hello / load / setPause{level 0..3} / ping / shutdown；事件：ready / firstFrame{ms} / stats{...} / error{code,msg,fatal}。

import (
	"encoding/json"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var pIsProcessInJob = kernel32.NewProc("IsProcessInJob")

func inJob() bool {
	var r int32
	pIsProcessInJob.Call(^uintptr(0) /*GetCurrentProcess 伪句柄*/, 0, uintptr(unsafe.Pointer(&r)))
	return r != 0
}

func runMFHost() {
	h, err := syscall.CreateFile(u16(*flagPipe), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		logf("MFHOST FATAL 连接管道失败: %v", err)
		os.Exit(2)
	}
	pipe := os.NewFile(uintptr(h), *flagPipe)
	eng := &mfEngine{}
	var conn *ipcConn
	var cur struct {
		wid  uintptr
		w, h int32
	}
	level := 0
	quit := make(chan struct{})
	var quitOnce sync.Once
	stopAll := func() { quitOnce.Do(func() { close(quit) }) }
	conn = newIPC(pipe, func(m string, p json.RawMessage) (any, *ipcErr) {
		switch m {
		case "hello":
			var x struct{ Proto int }
			json.Unmarshal(p, &x)
			if x.Proto != 1 {
				return nil, &ipcErr{Code: "E_PROTO", Msg: "proto"}
			}
			return map[string]any{"pid": os.Getpid(), "caps": []string{"video", "mf"}, "inJob": inJob()}, nil
		case "load":
			var x struct {
				Entry  string
				Wid    uint64
				Bounds struct{ W, H int32 }
				Paused bool
			}
			if err := json.Unmarshal(p, &x); err != nil {
				return nil, &ipcErr{Code: "E_PROTO", Msg: err.Error()}
			}
			*flagVideo = x.Entry
			cur.wid, cur.w, cur.h = uintptr(x.Wid), x.Bounds.W, x.Bounds.H
			eng.paused.Store(x.Paused)
			if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
				return nil, &ipcErr{Code: "E_LOAD", Msg: err.Error()}
			}
			conn.emit("ready", map[string]any{})
			return struct{}{}, nil
		case "setPause":
			var x struct{ Level int }
			json.Unmarshal(p, &x)
			switch {
			case x.Level >= 3 && level < 3: // L3：释放解码器与交换链，进程保留
				eng.stop()
			case x.Level < 3 && level >= 3: // 从 L3 恢复：重新加载（从头播放，见 README 已知简化）
				eng.paused.Store(x.Level > 0)
				if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
					return nil, &ipcErr{Code: "E_LOAD", Msg: err.Error()}
				}
			default:
				eng.setPause(x.Level > 0)
			}
			level = x.Level
			logf("MFHOST setPause level=%d", level)
			return struct{}{}, nil
		case "ping":
			return struct{}{}, nil
		case "shutdown":
			go func() { time.Sleep(50 * time.Millisecond); stopAll() }()
			return struct{}{}, nil
		}
		return nil, &ipcErr{Code: "E_PROTO", Msg: "unknown method " + m}
	}, nil)
	eng.onFirst = func(ms int64) { conn.emit("firstFrame", map[string]int64{"ms": ms}) }
	eng.onStats = func(st map[string]any) { conn.emit("stats", st) }
	eng.onFatal = func(why string) {
		code := "E_GPU"
		if why == "stall" {
			code = "E_INTERNAL"
		}
		conn.emit("error", ipcErr{Code: code, Msg: why, Fatal: true})
		time.Sleep(100 * time.Millisecond)
		stopAll()
	}
	select {
	case <-quit:
	case <-conn.Done(): // 主进程断开
		logf("MFHOST 管道断开: %v", conn.Err())
	}
	eng.stop()
	conn.Close()
}
