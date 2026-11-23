//go:build !unix && !windows

package telemetry

func processAlive(int) bool { return false }
