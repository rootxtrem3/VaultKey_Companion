package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/vaultkey"
)

func TestParseGlobalsAnywhere(t *testing.T) {
	t.Setenv("VAULTKEY_PORT", "")
	t.Setenv("VAULTKEY_TIMEOUT", "")
	options, remaining, err := parseGlobals([]string{
		"status", "--port", "/dev/ttyACM3", "--timeout=2s", "--debug",
	})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if options.port != "/dev/ttyACM3" || options.timeout != 2*time.Second || !options.debug {
		t.Fatalf("unexpected options: %+v", options)
	}
	if len(remaining) != 1 || remaining[0] != "status" {
		t.Fatalf("unexpected remaining arguments: %v", remaining)
	}
}

func TestParseGlobalsEnvironment(t *testing.T) {
	t.Setenv("VAULTKEY_PORT", "/dev/ttyACM9")
	t.Setenv("VAULTKEY_TIMEOUT", "3s")
	options, _, err := parseGlobals([]string{"status"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if options.port != "/dev/ttyACM9" || options.timeout != 3*time.Second {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestParseGlobalsRejectsBadTimeout(t *testing.T) {
	t.Setenv("VAULTKEY_TIMEOUT", "")
	for _, value := range []string{"0s", "2m", "not-a-duration"} {
		if _, _, err := parseGlobals([]string{"--timeout", value, "status"}); err == nil {
			t.Errorf("accepted timeout %q", value)
		}
	}
}

func TestParseGlobalsTerminator(t *testing.T) {
	t.Setenv("VAULTKEY_TIMEOUT", "")
	_, remaining, err := parseGlobals([]string{"--", "status", "--port", "x"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if len(remaining) != 3 || remaining[0] != "status" {
		t.Fatalf("unexpected remaining: %v", remaining)
	}
}

func TestReadOneLine(t *testing.T) {
	value, err := readOneLine(strings.NewReader("secret with spaces\r\nignored"), 64)
	if err != nil {
		t.Fatalf("readOneLine: %v", err)
	}
	if value != "secret with spaces" {
		t.Fatalf("unexpected value %q", value)
	}

	const limit = 5
	exact := strings.Repeat("a", limit)
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"exact limit with LF", exact + "\n", exact, false},
		{"exact limit with CRLF", exact + "\r\n", exact, false},
		{"exact limit without newline", exact, exact, false},
		{"over limit with LF", exact + "a\n", "", true},
		{"over limit with CRLF", exact + "a\r\n", "", true},
		{"over limit without newline", exact + "a", "", true},
		{"empty", "", "", true},
	}
	for _, test := range cases {
		got, err := readOneLine(strings.NewReader(test.input), limit)
		if test.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got %q", test.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}

func TestDeviceMessages(t *testing.T) {
	cases := map[string]int{
		"device_locked":               exitLocked,
		"provisioning_not_authorized": exitNotAuthorized,
		"storage_failed":              exitDeviceOperation,
		"invalid_credential":          exitUsage,
	}
	for code, expected := range cases {
		_, actual := deviceMessage(&vaultkey.DeviceError{Code: code})
		if actual != expected {
			t.Errorf("%s: got exit %d, want %d", code, actual, expected)
		}
	}
}

type pipePort struct {
	net.Conn
}

func (pipePort) Name() string { return "test" }

func TestFetchAllPagination(t *testing.T) {
	server, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close(); _ = server.Close() })

	go func() {
		reader := bufio.NewReader(server)
		writer := bufio.NewWriter(server)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request map[string]any
			if err := json.Unmarshal(line, &request); err != nil {
				return
			}
			offset := uint32(request["offset"].(float64))
			credentials := []map[string]any{}
			if offset == 0 {
				for id := 1; id <= 25; id++ {
					credentials = append(credentials, map[string]any{
						"id": id, "service": "service",
					})
				}
			} else {
				for id := 26; id <= 30; id++ {
					credentials = append(credentials, map[string]any{
						"id": id, "service": "service",
					})
				}
			}
			response := map[string]any{
				"v": 1, "id": request["id"], "ok": true,
				"offset": offset, "limit": 25,
				"credential_total": 30, "totp_total": 0,
				"credentials": credentials, "totp": []any{},
			}
			encoded, _ := json.Marshal(response)
			_, _ = writer.Write(append(encoded, '\n'))
			_ = writer.Flush()
		}
	}()

	client := vaultkey.New(pipePort{Conn: clientSide})
	t.Cleanup(func() { _ = client.Close() })
	application := &app{
		stdout:  &strings.Builder{},
		stderr:  &strings.Builder{},
		timeout: time.Second,
		ctx:     context.Background(),
	}
	aggregate, err := application.fetchAll(client, "")
	if err != nil {
		t.Fatalf("fetchAll: %v", err)
	}
	if aggregate.CredentialTotal != 30 || len(aggregate.Credentials) != 30 {
		t.Fatalf("unexpected aggregate: total=%d len=%d",
			aggregate.CredentialTotal, len(aggregate.Credentials))
	}
	seen := map[uint32]bool{}
	for _, entry := range aggregate.Credentials {
		if seen[entry.ID] {
			t.Fatalf("duplicate ID %d", entry.ID)
		}
		seen[entry.ID] = true
	}
}
