//go:build windows

package telemetry

import (
	"errors"
	"syscall"
)

// retryableChildLogRename reports transient Windows sharing/lock violations.
// ERROR_ACCESS_DENIED is included because a file opened without delete sharing
// can surface as either code depending on the handle and antivirus filter.
func retryableChildLogRename(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case 5 /* ERROR_ACCESS_DENIED */, 32 /* ERROR_SHARING_VIOLATION */, 33 /* ERROR_LOCK_VIOLATION */ :
		return true
	}
	return false
}
