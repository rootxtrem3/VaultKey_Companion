package vaultkey

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipePort struct {
	net.Conn
	name string
}

func (p pipePort) Name() string { return p.name }

func newPipeClient(t *testing.T, handler func(*bufio.Reader, *bufio.Writer)) *Client {
	t.Helper()
	server, client := net.Pipe()
	port := pipePort{Conn: client, name: "test-port"}
	vault := New(port)
	go func() {
		defer server.Close()
		writer := bufio.NewWriter(server)
		handler(bufio.NewReader(server), writer)
		_ = writer.Flush()
	}()
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func readRequest(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Errorf("read request: %v", err)
		return nil
	}
	var request map[string]any
	if err := json.Unmarshal(line, &request); err != nil {
		t.Errorf("decode request: %v", err)
		return nil
	}
	return request
}

func respond(t *testing.T, writer *bufio.Writer, request map[string]any, body map[string]any) {
	t.Helper()
	response := map[string]any{"v": 1, "id": request["id"], "ok": true}
	for key, value := range body {
		response[key] = value
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Errorf("encode response: %v", err)
		return
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		t.Errorf("write response: %v", err)
		return
	}
	if err := writer.Flush(); err != nil {
		t.Errorf("flush response: %v", err)
	}
}

func TestHelloSuccess(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		if request["v"] != float64(1) || request["cmd"] != "HELLO" {
			t.Errorf("unexpected request: %v", request)
		}
		if id, ok := request["id"].(string); !ok || !strings.HasPrefix(id, "vk-") {
			t.Errorf("unexpected request id: %v", request["id"])
		}
		respond(t, writer, request, map[string]any{
			"product":  "VaultKey",
			"firmware": "0.1.0-prototype",
			"hardware": "rev-D",
			"protocol": 1,
		})
	})

	hello, err := client.Hello(context.Background())
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if hello.Product != "VaultKey" || hello.Hardware != "rev-D" || hello.Protocol != 1 {
		t.Fatalf("unexpected hello: %+v", hello)
	}
}

func TestDeviceError(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		response := map[string]any{
			"v": 1, "id": request["id"], "ok": false, "error": "provisioning_not_authorized",
		}
		encoded, _ := json.Marshal(response)
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	})

	_, err := client.Status(context.Background())
	var device *DeviceError
	if !errors.As(err, &device) {
		t.Fatalf("want DeviceError, got %T %v", err, err)
	}
	if device.Code != "provisioning_not_authorized" {
		t.Fatalf("unexpected code: %s", device.Code)
	}
	if !IsCode(err, "provisioning_not_authorized") {
		t.Fatal("IsCode did not match")
	}
}

func TestDeviceErrorDetail(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		response := map[string]any{
			"v": 1, "id": request["id"], "ok": false,
			"error": "invalid_totp", "detail": "invalid_secret",
		}
		encoded, _ := json.Marshal(response)
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	})

	_, err := client.AddTOTP(context.Background(),
		"otpauth://totp/Issuer:account?secret=JBSWY3DPEHPK3PXP")
	var device *DeviceError
	if !errors.As(err, &device) || device.Detail != "invalid_secret" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFragmentedResponse(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		encoded, _ := json.Marshal(map[string]any{
			"v": 1, "id": request["id"], "ok": true, "locked": true,
			"rtc": "ready", "storage": "ready", "startup": "locked_home",
			"display": "ready", "button_event_drops": 0,
			"usb_driver": true, "usb_mounted": true, "cdc": true,
			"hid": true, "provisioning": false,
		})
		encoded = append(encoded, '\n')
		for index := 0; index < len(encoded); index += 7 {
			end := index + 7
			if end > len(encoded) {
				end = len(encoded)
			}
			_, _ = writer.Write(encoded[index:end])
			_ = writer.Flush()
			time.Sleep(time.Millisecond)
		}
	})

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked || !status.CDC || status.Provisioning {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestBlankLinesAreIgnored(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		encoded, _ := json.Marshal(map[string]any{
			"v": 1, "id": request["id"], "ok": true, "unix": 1794528000, "utc": true,
		})
		_, _ = writer.WriteString("\n \n")
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	})

	clock, err := client.Time(context.Background())
	if err != nil {
		t.Fatalf("Time: %v", err)
	}
	if clock.Unix != 1794528000 || !clock.UTC {
		t.Fatalf("unexpected clock: %+v", clock)
	}
}

func TestMismatchedIDIsProtocolError(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_ = readRequest(t, reader)
		_, _ = writer.WriteString(`{"v":1,"id":"other","ok":true}` + "\n")
		_ = writer.Flush()
	})

	_, err := client.Hello(context.Background())
	var protocol *ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("want ProtocolError, got %T %v", err, err)
	}
}

func TestMalformedResponseIsProtocolError(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_ = readRequest(t, reader)
		_, _ = writer.WriteString("not json\n")
		_ = writer.Flush()
	})

	_, err := client.Hello(context.Background())
	var protocol *ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("want ProtocolError, got %T %v", err, err)
	}
}

func TestMissingOKIsProtocolError(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		_, _ = fmt.Fprintf(writer, `{"v":1,"id":%q}`+"\n", request["id"])
		_ = writer.Flush()
	})

	_, err := client.Hello(context.Background())
	var protocol *ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("want ProtocolError, got %T %v", err, err)
	}
}

func TestOversizedRequestRejectedLocally(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_, _ = reader.ReadBytes('\n')
	})
	_, err := client.Do(context.Background(), "ADD_CREDENTIAL",
		map[string]any{"notes": strings.Repeat("x", MaxLineLength)}, true)
	var request *RequestError
	if !errors.As(err, &request) {
		t.Fatalf("want RequestError, got %T %v", err, err)
	}
}

func TestReservedFieldRejected(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_, _ = reader.ReadBytes('\n')
	})
	_, err := client.Do(context.Background(), "HELLO",
		map[string]any{"cmd": "LOCK_DEVICE"}, false)
	var request *RequestError
	if !errors.As(err, &request) {
		t.Fatalf("want RequestError, got %T %v", err, err)
	}
}

func TestMutatingTimeoutIsAmbiguous(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		if request["cmd"] != "LOCK_DEVICE" {
			t.Errorf("unexpected command: %v", request["cmd"])
		}
		time.Sleep(500 * time.Millisecond)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := client.Lock(ctx)
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("want AmbiguousError, got %T %v", err, err)
	}
}

func TestReadTimeoutIsNotAmbiguous(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_ = readRequest(t, reader)
		time.Sleep(500 * time.Millisecond)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.Hello(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %T %v", err, err)
	}
}

func TestAddCredentialFieldsAndResponse(t *testing.T) {
	input := CredentialInput{
		Service:  "Example",
		Username: "user@example.test",
		Password: "correct horse battery staple",
		URL:      "https://example.test",
		Notes:    "note",
	}
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		if request["cmd"] != "ADD_CREDENTIAL" {
			t.Errorf("unexpected command: %v", request["cmd"])
		}
		if request["password"] != input.Password {
			t.Errorf("password was not transferred")
		}
		respond(t, writer, request, map[string]any{"entry_id": 42})
	})

	id, err := client.AddCredential(context.Background(), input)
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if id != 42 {
		t.Fatalf("unexpected entry ID: %d", id)
	}
}

func TestListDecoding(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		request := readRequest(t, reader)
		if request["offset"] != float64(0) || request["limit"] != float64(25) {
			t.Errorf("unexpected pagination: %v", request)
		}
		respond(t, writer, request, map[string]any{
			"offset":           0,
			"limit":            25,
			"credential_total": 1,
			"totp_total":       1,
			"credentials":      []map[string]any{{"id": 7, "service": "GitHub"}},
			"totp":             []map[string]any{{"id": 8, "issuer": "Example", "account": "user"}},
		})
	})

	list, err := client.List(context.Background(), "", 0, 25)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list.CredentialTotal != 1 || len(list.Credentials) != 1 ||
		list.Credentials[0].Service != "GitHub" || list.TOTP[0].ID != 8 {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestCloseCausesErrClosed(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		_, _ = reader.ReadBytes('\n')
	})
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := client.Hello(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %T %v", err, err)
	}
}

type failingWritePort struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func (p *failingWritePort) Name() string { return "failing-port" }

func (p *failingWritePort) Read([]byte) (int, error) {
	<-p.closed
	return 0, io.EOF
}

func (p *failingWritePort) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func (p *failingWritePort) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}

func TestNonMutatingWriteFailureIsNotAmbiguous(t *testing.T) {
	client := New(&failingWritePort{closed: make(chan struct{})})
	t.Cleanup(func() { _ = client.Close() })

	_, err := client.Hello(context.Background())
	if err == nil {
		t.Fatal("expected write failure")
	}
	var ambiguous *AmbiguousError
	if errors.As(err, &ambiguous) {
		t.Fatalf("non-mutating write failure must not be ambiguous: %v", err)
	}
}

func TestMutatingWriteFailureIsAmbiguous(t *testing.T) {
	client := New(&failingWritePort{closed: make(chan struct{})})
	t.Cleanup(func() { _ = client.Close() })

	err := client.Lock(context.Background())
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("want AmbiguousError, got %T %v", err, err)
	}
}
