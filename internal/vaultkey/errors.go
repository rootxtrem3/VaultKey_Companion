package vaultkey

import (
	"errors"
	"fmt"
)

// ErrClosed means the serial connection is no longer usable.
var ErrClosed = errors.New("device connection is closed")

// ProtocolError reports a malformed, mismatched, or unsupported response.
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string { return "protocol error: " + e.Message }

// DeviceError is an error code returned by the device.
type DeviceError struct {
	Code   string
	Detail string
}

func (e *DeviceError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s (%s)", e.Code, e.Detail)
	}
	return e.Code
}

// AmbiguousError means a request was written but no definitive response was
// received. The device may have applied a mutating command. Callers must not
// retry blindly.
type AmbiguousError struct {
	Command string
	Cause   error
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s: outcome unknown: %v", e.Command, e.Cause)
}

func (e *AmbiguousError) Unwrap() error { return e.Cause }

// RequestError reports invalid local input rejected before transmission.
type RequestError struct {
	Message string
}

func (e *RequestError) Error() string { return e.Message }

// IsCode reports whether err is a device error with the given code.
func IsCode(err error, code string) bool {
	var device *DeviceError
	return errors.As(err, &device) && device.Code == code
}
