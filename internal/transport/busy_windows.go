//go:build windows

package transport

import (
	"errors"
	"syscall"
)

func isBusy(err error) bool {
	// ERROR_ACCESS_DENIED on a COM open usually means the port is in use;
	// ERROR_SHARING_VIOLATION is unambiguous.
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		errors.Is(err, syscall.Errno(32))
}
