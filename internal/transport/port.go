// Package transport provides the small serial-port layer used by the
// VaultKey companion. It deliberately has no third-party dependencies.
package transport

import (
	"errors"
	"fmt"
	"io"
)

// Port is an open serial port.
type Port interface {
	io.ReadWriteCloser
	Name() string
}

// ErrUnsupported is returned when serial ports are not implemented on the
// current platform.
var ErrUnsupported = errors.New("serial ports are not supported on this platform")

// errNotSerial reports a device that opened but rejected serial configuration.
var errNotSerial = errors.New("not a serial port")

func notSerial(name string, err error) error {
	return fmt.Errorf("%s: %w (%v)", name, errNotSerial, err)
}

// IsNotSerial reports whether a path opened but could not be configured as a
// serial port.
func IsNotSerial(err error) bool {
	return errors.Is(err, errNotSerial)
}

// IsBusy reports whether the platform refused the port because another
// process owns it.
func IsBusy(err error) bool {
	return isBusy(err)
}
