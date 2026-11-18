// Package apperr maps internal errors to the stable UI contract.
package apperr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/rootxtrem3/VaultKey_Companion/internal/prompt"
	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// Classify returns a secret-free machine-readable error object.
func Classify(err error) vaultkey.ErrorInfo {
	if err == nil {
		return vaultkey.ErrorInfo{}
	}

	if errors.Is(err, context.Canceled) {
		return vaultkey.ErrorInfo{
			Kind: "interrupted", Code: "interrupted", Message: "interrupted",
			ExitCode: vaultkey.ExitInterrupted,
		}
	}

	var ambiguous *vaultkey.AmbiguousError
	if errors.As(err, &ambiguous) {
		return vaultkey.ErrorInfo{
			Kind:      "ambiguous",
			Code:      "ambiguous_write",
			Message:   ambiguous.Error(),
			Hint:      "the command may have been applied. Reconnect and verify before retrying.",
			ExitCode:  vaultkey.ExitAmbiguousOrTimeout,
			Ambiguous: true,
		}
	}

	var request *vaultkey.RequestError
	if errors.As(err, &request) {
		return vaultkey.ErrorInfo{
			Kind: "usage", Code: "invalid_request", Message: request.Error(),
			ExitCode: vaultkey.ExitUsage,
		}
	}

	var protocol *vaultkey.ProtocolError
	if errors.As(err, &protocol) {
		return vaultkey.ErrorInfo{
			Kind: "protocol", Code: "protocol_error", Message: protocol.Error(),
			Hint:     "reconnect the device. If this persists, update the app or firmware so both use protocol v1.",
			ExitCode: vaultkey.ExitProtocol,
		}
	}

	var device *vaultkey.DeviceError
	if errors.As(err, &device) {
		return classifyDevice(device)
	}

	var classified vaultkey.Classified
	if errors.As(err, &classified) {
		return vaultkey.ErrorInfo{
			Kind:     classified.Kind(),
			Code:     classified.Code(),
			Message:  classified.Error(),
			Hint:     classified.Hint(),
			ExitCode: classified.ExitCode(),
		}
	}

	if errors.Is(err, prompt.ErrNoTerminal) {
		return vaultkey.ErrorInfo{
			Kind: "usage", Code: "no_terminal",
			Message:  "no interactive terminal is available",
			Hint:     "run from a terminal, or use the command's non-interactive option.",
			ExitCode: vaultkey.ExitUsage,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return vaultkey.ErrorInfo{
			Kind: "timeout", Code: "timeout",
			Message:  "the device did not respond before the timeout",
			Hint:     "check the cable and port, or increase the timeout",
			ExitCode: vaultkey.ExitAmbiguousOrTimeout,
		}
	}
	if errors.Is(err, vaultkey.ErrClosed) {
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "connection_closed",
			Message:  "the device connection was closed",
			Hint:     "reconnect the device and try again.",
			ExitCode: vaultkey.ExitConnection,
		}
	}
	if errors.Is(err, transport.ErrUnsupported) {
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "unsupported_platform",
			Message:  "serial ports are not supported on this platform",
			ExitCode: vaultkey.ExitConnection,
		}
	}
	if transport.IsBusy(err) {
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "port_busy",
			Message:  "the serial port is in use by another program",
			Hint:     "close the other program using this port; serial ports are exclusive.",
			ExitCode: vaultkey.ExitConnection,
		}
	}
	if os.IsPermission(err) {
		hint := "adjust the device permissions."
		if runtime.GOOS == "windows" {
			hint = "check Device Manager and the USB serial driver permissions."
		} else {
			hint = "on Linux add your user to the dialout or uucp group, or adjust the device permissions."
		}
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "permission_denied",
			Message: err.Error(), Hint: hint,
			ExitCode: vaultkey.ExitConnection,
		}
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "port_open_failed",
			Message:  err.Error(),
			Hint:     "check that the device is connected and the port is correct.",
			ExitCode: vaultkey.ExitConnection,
		}
	}
	if transport.IsNotSerial(err) {
		return vaultkey.ErrorInfo{
			Kind: "connection", Code: "not_serial",
			Message:  err.Error(),
			Hint:     "choose the VaultKey CDC port (for example /dev/ttyACM0).",
			ExitCode: vaultkey.ExitConnection,
		}
	}

	return vaultkey.ErrorInfo{
		Kind: "internal", Code: "internal_error", Message: err.Error(),
		ExitCode: vaultkey.ExitFailure,
	}
}

func classifyDevice(device *vaultkey.DeviceError) vaultkey.ErrorInfo {
	info := vaultkey.ErrorInfo{
		Kind:       "device",
		Code:       device.Code,
		DeviceCode: device.Code,
		Detail:     device.Detail,
	}
	switch device.Code {
	case "device_locked":
		info.Kind = "locked"
		info.Message = "the device is locked"
		info.Hint = "unlock the device with its PIN, then retry."
		info.ExitCode = vaultkey.ExitLocked
	case "provisioning_not_authorized":
		info.Kind = "not_authorized"
		info.Message = "CDC provisioning is not authorized"
		info.Hint = `on the device open USB Functions and choose "Authorize CDC 5 min".`
		info.ExitCode = vaultkey.ExitNotAuthorized
	case "rtc_time_invalid":
		info.Message = "the RTC time is not valid"
		info.Hint = "set the clock with the time command, or from the device settings."
		info.ExitCode = vaultkey.ExitDeviceOperation
	case "storage_failed":
		info.Message = "the device could not save the change"
		info.ExitCode = vaultkey.ExitDeviceOperation
	case "delete_failed":
		info.Message = "the device could not delete the entry"
		info.ExitCode = vaultkey.ExitDeviceOperation
	case "entry_not_found":
		info.Message = "the entry was not found"
		info.ExitCode = vaultkey.ExitDeviceOperation
	case "invalid_credential", "invalid_query", "invalid_pagination",
		"invalid_time", "invalid_uri", "invalid_totp", "invalid_entry_type":
		info.Kind = "usage"
		info.Message = "the device rejected the request: " + device.Code
		info.ExitCode = vaultkey.ExitUsage
	default:
		info.Message = "the device returned an error"
		info.ExitCode = vaultkey.ExitFailure
	}
	if device.Code == "invalid_totp" && device.Detail != "" {
		info.Hint = "the TOTP URI was rejected: " + device.Detail
	}
	return info
}

// FormatError renders the human form used by the one-shot CLI.
func FormatError(info vaultkey.ErrorInfo) string {
	if info.DeviceCode != "" {
		return fmt.Sprintf("error: %s (%s)", info.Message, info.DeviceCode)
	}
	return "error: " + info.Message
}
