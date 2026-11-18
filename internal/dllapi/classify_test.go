package dllapi

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

func TestClassifyAmbiguousWrappedTimeout(t *testing.T) {
	info := Classify(&vaultkey.AmbiguousError{
		Command: "ADD_CREDENTIAL",
		Cause:   context.DeadlineExceeded,
	})
	if info == nil {
		t.Fatal("Classify returned nil")
	}
	if info.Kind != "ambiguous" || info.Code != "ambiguous_write" {
		t.Fatalf("kind/code = %q/%q, want ambiguous/ambiguous_write", info.Kind, info.Code)
	}
	if !info.Ambiguous {
		t.Fatal("ambiguous flag is false")
	}
	if info.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", info.ExitCode)
	}
}

func TestClassifyAmbiguousWrappedWriteError(t *testing.T) {
	info := Classify(&vaultkey.AmbiguousError{
		Command: "DELETE_ENTRY",
		Cause:   errors.New("write failed"),
	})
	if info == nil || info.Kind != "ambiguous" || !info.Ambiguous || info.ExitCode != 7 {
		t.Fatalf("got %+v, want ambiguous exit 7", info)
	}
}

func TestClassifyPlainTimeoutIsNotAmbiguous(t *testing.T) {
	info := Classify(context.DeadlineExceeded)
	if info == nil || info.Kind != "timeout" || info.Ambiguous || info.ExitCode != 7 {
		t.Fatalf("got %+v, want plain timeout exit 7", info)
	}
}

func TestClassifyCanceled(t *testing.T) {
	info := Classify(context.Canceled)
	if info == nil || info.Kind != "interrupted" || info.ExitCode != 130 {
		t.Fatalf("got %+v, want interrupted exit 130", info)
	}
}

func TestClassifyRequestError(t *testing.T) {
	info := Classify(&vaultkey.RequestError{Message: "bad input"})
	if info == nil || info.Kind != "usage" || info.Code != "invalid_request" || info.ExitCode != 2 {
		t.Fatalf("got %+v, want usage/invalid_request exit 2", info)
	}
}

func TestClassifyDeviceErrors(t *testing.T) {
	tests := []struct {
		code     string
		kind     string
		exitCode int
	}{
		{"device_locked", "locked", 4},
		{"provisioning_not_authorized", "not_authorized", 5},
		{"invalid_credential", "usage", 2},
		{"rtc_time_invalid", "device", 8},
		{"unknown_code", "device", 1},
	}
	for _, test := range tests {
		info := Classify(&vaultkey.DeviceError{Code: test.code})
		if info == nil || info.Kind != test.kind || info.ExitCode != test.exitCode {
			t.Errorf("code %q: got %+v, want kind %q exit %d",
				test.code, info, test.kind, test.exitCode)
		}
	}
}

func TestClassifyConnectionErrors(t *testing.T) {
	closed := Classify(vaultkey.ErrClosed)
	if closed == nil || closed.Kind != "connection" || closed.Code != "connection_closed" || closed.ExitCode != 3 {
		t.Fatalf("closed: got %+v", closed)
	}

	unsupported := Classify(transport.ErrUnsupported)
	if unsupported == nil || unsupported.Kind != "connection" ||
		unsupported.Code != "unsupported_platform" || unsupported.ExitCode != 3 {
		t.Fatalf("unsupported: got %+v", unsupported)
	}

	permission := Classify(&os.PathError{Op: "open", Path: "COM3", Err: os.ErrPermission})
	if permission == nil || permission.Code != "permission_denied" || permission.ExitCode != 3 {
		t.Fatalf("permission: got %+v", permission)
	}

	openFailed := Classify(&os.PathError{Op: "open", Path: "COM3", Err: errors.New("not found")})
	if openFailed == nil || openFailed.Code != "port_open_failed" || openFailed.ExitCode != 3 {
		t.Fatalf("open failed: got %+v", openFailed)
	}
}

func TestClassifyNil(t *testing.T) {
	if info := Classify(nil); info != nil {
		t.Fatalf("Classify(nil) = %+v, want nil", info)
	}
}
