//go:build !windows

package main

func runCoverTest() { logf("FATAL -covertest 只在 Windows 上可用") }
