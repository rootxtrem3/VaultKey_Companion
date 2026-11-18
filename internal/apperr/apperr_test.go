package apperr

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rootxtrem3/VaultKey_Companion/internal/prompt"
	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind string
		code string
		exit int
	}{
		{"canceled", context.Canceled, "interrupted", "interrupted", 130},
		{"ambiguous", &vaultkey.AmbiguousError{
			Command: "ADD_CREDENTIAL", Cause: context.DeadlineExceeded,
		}, "ambiguous", "ambiguous_write", 7},
		{"request", &vaultkey.RequestError{Message: "bad"}, "usage", "invalid_request", 2},
		{"protocol", &vaultkey.ProtocolError{Message: "bad"}, "protocol", "protocol_error", 6},
		{"locked", &vaultkey.DeviceError{Code: "device_locked"}, "locked", "device_locked", 4},
		{"unauthorized", &vaultkey.DeviceError{Code: "provisioning_not_authorized"},
			"not_authorized", "provisioning_not_authorized", 5},
		{"storage", &vaultkey.DeviceError{Code: "storage_failed"},
			"device", "storage_failed", 8},
		{"invalid credential", &vaultkey.DeviceError{Code: "invalid_credential"},
			"usage", "invalid_credential", 2},
		{"unknown device", &vaultkey.DeviceError{Code: "surprise"},
			"device", "surprise", 1},
		{"no terminal", prompt.ErrNoTerminal, "usage", "no_terminal", 2},
		{"deadline", context.DeadlineExceeded, "timeout", "timeout", 7},
		{"closed", vaultkey.ErrClosed, "connection", "connection_closed", 3},
		{"unsupported", transport.ErrUnsupported, "connection", "unsupported_platform", 3},
		{"permission", &os.PathError{Op: "open", Path: "/dev/x", Err: os.ErrPermission},
			"connection", "permission_denied", 3},
		{"open failed", &os.PathError{Op: "open", Path: "/dev/x", Err: errors.New("x")},
			"connection", "port_open_failed", 3},
		{"not found", &vaultkey.NotFoundError{}, "connection", "device_not_found", 3},
		{"multiple", &vaultkey.MultipleDevicesError{Ports: []string{"a", "b"}},
			"connection", "multiple_devices", 3},
		{"internal", errors.New("boom"), "internal", "internal_error", 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			info := Classify(test.err)
			if info.Kind != test.kind || info.Code != test.code || info.ExitCode != test.exit {
				t.Fatalf("got %+v, want kind=%s code=%s exit=%d",
					info, test.kind, test.code, test.exit)
			}
			if test.kind == "ambiguous" && !info.Ambiguous {
				t.Fatal("ambiguous flag is not set")
			}
		})
	}
}

func TestDeviceErrorKeepsCodeAndDetail(t *testing.T) {
	info := Classify(&vaultkey.DeviceError{
		Code: "invalid_totp", Detail: "invalid_secret",
	})
	if info.DeviceCode != "invalid_totp" || info.Detail != "invalid_secret" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if info.Hint == "" {
		t.Fatal("expected the TOTP detail hint")
	}
}
