//go:build windows

package mf

// Media Foundation 渲染子进程（wallpaperd -mfhost -pipe <名字>）。与 mpv.exe 地位相同：继承 Job Object，
// 通过 IPC v1 与主进程通信，渲染到主进程创建的子 HWND。
// 方法：hello / load / setPause{level 0..3} / ping / shutdown；事件：ready / firstFrame{ms} / stats{...} / error{code,msg,fatal}。

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"wpapp/internal/ipc"
)

var (
	kernel32b       = syscall.NewLazyDLL("kernel32.dll")
	pIsProcessInJob = kernel32b.NewProc("IsProcessInJob")
)

func inJob() bool {
	var r int32
	pIsProcessInJob.Call(^uintptr(0), 0, uintptr(unsafe.Pointer(&r)))
	return r != 0
}

// RunHost 连接主进程的命名管道并服务，直到 shutdown 或管道断开。
func RunHost(pipeName string, swap string, statsEvery time.Duration) error {
	h, err := syscall.CreateFile(u16(pipeName), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("mfhost: connect pipe: %w", err)
	}
	pipe := os.NewFile(uintptr(h), pipeName)
	eng := &mfEngine{swap: swap, statsEvery: statsEvery}
	var conn *ipc.Conn
	var cur struct {
		wid  uintptr
		w, h int32
	}
	var mu sync.Mutex
	level := 0
	quit := make(chan struct{})
	var quitOnce sync.Once
	stopAll := func() { quitOnce.Do(func() { close(quit) }) }
	conn = ipc.New(pipe, func(m string, p json.RawMessage) (any, *ipc.Error) {
		mu.Lock()
		defer mu.Unlock()
		switch m {
		case "hello":
			var x struct{ Proto int }
			_ = json.Unmarshal(p, &x)
			if x.Proto != 1 {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: "proto"}
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
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			eng.path = x.Entry
			cur.wid, cur.w, cur.h = uintptr(x.Wid), x.Bounds.W, x.Bounds.H
			eng.paused.Store(x.Paused)
			if x.Paused {
				level = 1
			}
			if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
				return nil, &ipc.Error{Code: "E_LOAD", Msg: err.Error()}
			}
			_ = conn.Emit("ready", map[string]any{})
			return struct{}{}, nil
		case "setPause":
			var x struct{ Level int }
			_ = json.Unmarshal(p, &x)
			switch {
			case x.Level >= 3 && level < 3:
				eng.stop()
			case x.Level < 3 && level >= 3: // 从 L3 恢复：重新加载（从头播放，Q-V3）
				eng.paused.Store(x.Level > 0)
				if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
					return nil, &ipc.Error{Code: "E_LOAD", Msg: err.Error()}
				}
			default:
				eng.setPause(x.Level > 0)
			}
			level = x.Level
			return struct{}{}, nil
		case "setFPS":
			var x struct{ FPS int }
			_ = json.Unmarshal(p, &x)
			if x.FPS < 0 || x.FPS > 240 {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: "fps out of range"}
			}
			eng.fpsCap.Store(int32(x.FPS))
			return struct{}{}, nil
		case "snapshot":
			return nil, &ipc.Error{Code: "E_STATE", Msg: "snapshot not implemented in mf backend"}
		case "ping":
			return struct{}{}, nil
		case "shutdown":
			go func() { time.Sleep(50 * time.Millisecond); stopAll() }()
			return struct{}{}, nil
		}
		return nil, &ipc.Error{Code: "E_PROTO", Msg: "unknown method " + m}
	}, nil)
	eng.onFirst = func(ms int64) { _ = conn.Emit("firstFrame", map[string]int64{"ms": ms}) }
	eng.onStats = func(st map[string]any) { _ = conn.Emit("stats", st) }
	eng.onFatal = func(why string) {
		code := "E_GPU"
		if why == "stall" {
			code = "E_INTERNAL"
		}
		_ = conn.Emit("error", ipc.Error{Code: code, Msg: why, Fatal: true})
		time.Sleep(100 * time.Millisecond)
		stopAll()
	}
	select {
	case <-quit:
	case <-conn.Done():
		logf("mfhost: pipe closed: %v", conn.Err())
	}
	eng.stop()
	conn.Close()
	return nil
}
