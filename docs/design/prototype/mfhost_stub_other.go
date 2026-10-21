//go:build !windows

package main

func runMFHost() { logf("FATAL -mfhost 只在 Windows 上可用") }
