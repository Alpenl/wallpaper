//go:build !windows

// Package mf 是 Windows 的 Media Foundation 渲染后端；其他平台只有占位。
package mf

// HostMain 在非 Windows 平台不可用。
func HostMain(pipe, swap, logPath string) int { return 2 }
