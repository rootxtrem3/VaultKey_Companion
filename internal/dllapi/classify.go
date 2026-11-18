// Package dllapi contains the pure error mapping used by the Windows DLL
// prototype. It has no cgo or build tags so it can be tested on any host.
package dllapi

import (
	"context"
	"errors"
	"os"

	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// Info is the JSON error object returned by VKCall.
type Info struct {
	Kind       string `json:"kind"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Ambiguous  bool   `json:"ambiguous,omitempty"`
	DeviceCode string `json:"device_code,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Usage builds an invalid-request error (exit 2).
func Usage(code, message string) *Info {
	return &Info{Kind: "usage", Code: code, Message: message, ExitCode: 2}
}

// Connection builds a connection error (exit 3).
func Connection(code, message, hint string) *Info {
	return &Info{Kind: "connection", Code: code, Message: message, Hint: hint, ExitCode: 3}
}

// Classify maps a Go error to the DLL response contract.
func Classify(err error) *Info {
	if err == nil {
		return nil
	}

	var ambiguous *vaultkey.AmbiguousError
	var request *vaultkey.RequestError
	var protocol *vaultkey.ProtocolError
	var device *vaultkey.DeviceError

	switch {
	case errors.Is(err, context.Canceled):
		return &Info{Kind: "interrupted", Code: "interrupted",
			Message: "the request was interrupted", ExitCode: 130}
	// Check ambiguity before the wrapped cause. AmbiguousError unwraps to
	// context.DeadlineExceeded or the write error, and losing the ambiguity
	// would tell a UI that a mutating command is safe to retry.
	case errors.As(err, &ambiguous):
		return &Info{Kind: "ambiguous", Code: "ambiguous_write",
			Message: ambiguous.Error(), Ambiguous: true,
			Hint:     "the command may have been applied; verify with status/list before retrying.",
			ExitCode: 7}
	case errors.Is(err, context.DeadlineExceeded):
		return &Info{Kind: "timeout", Code: "timeout",
			Message: "the request timed out", Hint: "check that the device is connected and authorized.",
			ExitCode: 7}
	case errors.As(err, &request):
		return &Info{Kind: "usage", Code: "invalid_request",
			Message: request.Message, ExitCode: 2}
	case errors.As(err, &protocol):
		return &Info{Kind: "protocol", Code: "protocol_error",
			Message: protocol.Message, ExitCode: 6}
	case errors.As(err, &device):
		return ClassifyDevice(device)
	case errors.Is(err, vaultkey.ErrClosed):
		return Connection("connection_closed", "the device connection is closed",
			"reconnect the device and try again.")
	case errors.Is(err, transport.ErrUnsupported):
		return Connection("unsupported_platform", "serial ports are not supported on this platform", "")
	case transport.IsNotSerial(err):
		return Connection("not_serial", err.Error(),
			"the selected port is not a serial device.")
	case os.IsPermission(err):
		return Connection("permission_denied", err.Error(),
			"check port permissions or close the program that owns the port.")
	default:
		var path *os.PathError
		if errors.As(err, &path) {
			return Connection("port_open_failed", err.Error(),
				"check that the port name exists and is not in use.")
		}
		return &Info{Kind: "internal", Code: "internal_error",
			Message: err.Error(), ExitCode: 1}
	}
}

// ClassifyDevice maps a protocol device error to the DLL response contract.
func ClassifyDevice(err *vaultkey.DeviceError) *Info {
	info := &Info{Kind: "device", Code: err.Code, Message: err.Error(),
		DeviceCode: err.Code, Detail: err.Detail}
	switch err.Code {
	case "device_locked":
		info.Kind = "locked"
		info.ExitCode = 4
		info.Message = "the device is locked"
		info.Hint = "unlock the device with its PIN, then retry."
	case "provisioning_not_authorized":
		info.Kind = "not_authorized"
		info.ExitCode = 5
		info.Message = "CDC provisioning is not authorized"
		info.Hint = `on the device open USB Functions and choose "Authorize CDC 5 min".`
	case "rtc_time_invalid":
		info.ExitCode = 8
		info.Hint = "set the clock with time.set, or from the device menu."
	case "storage_failed":
		info.ExitCode = 8
		info.Hint = "the device could not save the change; check storage health."
	case "delete_failed":
		info.ExitCode = 8
		info.Hint = "the device could not delete the entry."
	case "entry_not_found":
		info.ExitCode = 8
		info.Hint = "refresh the list and use an ID that still exists."
	case "invalid_credential", "invalid_query", "invalid_pagination",
		"invalid_time", "invalid_uri", "invalid_totp", "invalid_entry_type":
		info.Kind = "usage"
		info.ExitCode = 2
		info.Message = "the device rejected the request: " + err.Code
	default:
		info.ExitCode = 1
	}
	return info
}
