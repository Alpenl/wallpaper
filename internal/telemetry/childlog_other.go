//go:build !windows

package telemetry

// Unix rename succeeds while the previous file is still open, so no retry is
// needed for the diagnostic log.
func retryableChildLogRename(error) bool { return false }
