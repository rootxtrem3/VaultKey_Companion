//go:build linux || darwin

package transport

import (
	"errors"
	"syscall"
)

func isBusy(err error) bool {
	return errors.Is(err, syscall.EBUSY)
}
