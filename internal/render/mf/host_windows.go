//go:build windows

package mf

// Media Foundation 渲染子进程（wallpaperd -mfhost -pipe <名字>）。与 mpv.exe 地位相同：继承 Job Object，
// 通过 IPC v1 与主进程通信，渲染到主进程创建的子 HWND。
// 方法：hello / load / setPause{level 0..3} / setPlayback / setFPS / ping / shutdown。
// 事件：ready / firstFrame{ms} / stats{...} / error{code,msg,fatal}。

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"wpapp/internal/ipc"
	"wpapp/internal/render"
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
	h, err := syscall.CreateFile(u16(pipeName), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return fmt.Errorf("mfhost: connect pipe: %w", err)
	}
	pipe := os.NewFile(uintptr(h), pipeName)
	if statsEvery <= 0 {
		statsEvery = 5 * time.Second
	}
	eng := &mfEngine{swap: swap, statsEvery: statsEvery, options: render.DefaultPlaybackOptions()}
	var conn *ipc.Conn
	var cur struct {
		wid  uintptr
		w, h int32
	}
	var mu sync.Mutex
	level := 0
	loaded := false
	quit := make(chan struct{})
	var quitOnce sync.Once
	stopAll := func() { quitOnce.Do(func() { close(quit) }) }
	initialized := make(chan struct{})
	conn = ipc.New(pipe, func(m string, p json.RawMessage) (any, *ipc.Error) {
		<-initialized
		mu.Lock()
		defer mu.Unlock()
		switch m {
		case "hello":
			var x struct{ Proto int }
			if err := json.Unmarshal(p, &x); err != nil {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			if x.Proto != 1 {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: "proto"}
			}
			return map[string]any{"pid": os.Getpid(), "caps": []string{"video", "mf", "fit", "speed", "audio", "resume", "fps", "live-playback"}, "inJob": inJob()}, nil
		case "load":
			x, options, err := decodeLoad(p)
			if err != nil {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			if err := eng.stop(); err != nil {
				return nil, lifecycleFailure("stop before load", err)
			}
			loaded = false
			eng.path, eng.options, eng.startAt = x.Entry, options, x.Start
			cur.wid, cur.w, cur.h = uintptr(x.Wid), x.Bounds.W, x.Bounds.H
			eng.paused.Store(x.Paused)
			level = 0
			if x.Paused {
				level = 1
			}
			if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
				return nil, &ipc.Error{Code: "E_LOAD", Msg: err.Error()}
			}
			loaded = true
			_ = conn.Emit("ready", map[string]any{"pos": eng.currentPosition()})
			return map[string]any{"pos": eng.currentPosition()}, nil
		case "setPause":
			next, err := decodePause(p)
			if err != nil {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			if !loaded {
				return nil, &ipc.Error{Code: "E_STATE", Msg: "media not loaded"}
			}
			switch {
			case next >= 3 && level < 3:
				if err := eng.stop(); err != nil {
					return nil, lifecycleFailure("enter L3", err)
				}
				eng.startAt = eng.currentPosition()
				eng.paused.Store(true)
			case next < 3 && level >= 3:
				eng.paused.Store(next > 0)
				if err := eng.start(cur.wid, cur.w, cur.h); err != nil {
					return nil, lifecycleFailure("resume from L3", err)
				}
			case next < 3:
				if err := eng.setPause(next > 0); err != nil {
					return nil, &ipc.Error{Code: "E_STATE", Msg: err.Error()}
				}
			}
			level = next
			return map[string]any{"pos": eng.currentPosition()}, nil
		case "setPlayback":
			options, err := decodePlayback(p)
			if err != nil {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			if !loaded || level >= 3 {
				return nil, &ipc.Error{Code: "E_STATE", Msg: "media not active"}
			}
			if err := eng.updatePlayback(options); err != nil {
				return nil, &ipc.Error{Code: "E_STATE", Msg: err.Error()}
			}
			return struct{}{}, nil
		case "setFPS":
			fps, err := decodeFPS(p)
			if err != nil {
				return nil, &ipc.Error{Code: "E_PROTO", Msg: err.Error()}
			}
			eng.fpsCap.Store(int32(fps))
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
	close(initialized)
	select {
	case <-quit:
	case <-conn.Done():
		logf("mfhost: pipe closed: %v", conn.Err())
	}
	conn.Close() // unblock pending writes before waiting for render/stats shutdown
	mu.Lock()
	err = eng.stop()
	mu.Unlock()
	return err
}
