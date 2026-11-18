package vaultkey

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
)

func usePorts(t *testing.T, ports []string,
	open func(string) (transport.Port, error)) {
	t.Helper()
	originalCandidates := candidatePorts
	originalOpen := openPort
	candidatePorts = func() ([]string, error) { return ports, nil }
	openPort = open
	t.Cleanup(func() {
		candidatePorts = originalCandidates
		openPort = originalOpen
	})
}

func scriptedPort(t *testing.T, name string, hello map[string]any) transport.Port {
	t.Helper()
	server, client := net.Pipe()
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		writer := bufio.NewWriter(server)
		request := readRequest(t, reader)
		if request == nil {
			return
		}
		respond(t, writer, request, hello)
		// Hold the pipe open until the client closes it. Closing right after
		// the response races the reader's EOF against frame delivery.
		_, _ = reader.ReadByte()
	}()
	return pipePort{Conn: client, name: name}
}

func vaultKeyHello() map[string]any {
	return map[string]any{
		"product": "VaultKey", "firmware": "1.0.0",
		"hardware": "rev-D", "protocol": 1,
	}
}

func TestDiscoverReportsEveryState(t *testing.T) {
	usePorts(t, []string{"vault", "foreign", "silent"}, func(name string) (transport.Port, error) {
		switch name {
		case "vault":
			return scriptedPort(t, name, vaultKeyHello()), nil
		case "foreign":
			return scriptedPort(t, name, map[string]any{
				"product": "Other", "firmware": "2", "hardware": "x", "protocol": 1,
			}), nil
		case "silent":
			return scriptedPort(t, name, map[string]any{
				"product": "VaultKey", "firmware": "1", "hardware": "rev-D", "protocol": 1,
			}), nil
		}
		return nil, errors.New("unexpected port")
	})

	infos, err := Discover(context.Background(), OpenOptions{ProbeTimeout: time.Second})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("got %d rows", len(infos))
	}
	if infos[0].State != "vaultkey" || infos[1].State != "other-device" ||
		infos[2].State != "vaultkey" {
		t.Fatalf("unexpected states: %+v", infos)
	}
}

func TestDiscoverUnreachable(t *testing.T) {
	usePorts(t, []string{"missing"}, func(name string) (transport.Port, error) {
		return nil, errors.New("no such device")
	})
	infos, err := Discover(context.Background(), OpenOptions{ProbeTimeout: time.Second})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(infos) != 1 || infos[0].State != "unreachable" {
		t.Fatalf("unexpected rows: %+v", infos)
	}
}

func TestOpenNoCandidates(t *testing.T) {
	usePorts(t, nil, func(string) (transport.Port, error) {
		return nil, errors.New("should not open")
	})
	_, err := Open(context.Background(), OpenOptions{})
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want NotFoundError, got %T %v", err, err)
	}
	if notFound.ExitCode() != ExitConnection || notFound.Code() != "device_not_found" {
		t.Fatalf("unexpected classification: %+v", notFound)
	}
}

func TestOpenMultipleDevices(t *testing.T) {
	usePorts(t, []string{"one", "two"}, func(name string) (transport.Port, error) {
		return scriptedPort(t, name, vaultKeyHello()), nil
	})
	_, err := Open(context.Background(), OpenOptions{})
	var multiple *MultipleDevicesError
	if !errors.As(err, &multiple) {
		t.Fatalf("want MultipleDevicesError, got %T %v", err, err)
	}
	if len(multiple.Ports) != 2 || multiple.ExitCode() != ExitConnection {
		t.Fatalf("unexpected multiple error: %+v", multiple)
	}
}

func TestOpenForeignDevice(t *testing.T) {
	usePorts(t, []string{"foreign"}, func(name string) (transport.Port, error) {
		return scriptedPort(t, name, map[string]any{
			"product": "Other", "firmware": "2", "hardware": "x", "protocol": 1,
		}), nil
	})
	_, err := Open(context.Background(), OpenOptions{})
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want NotFoundError, got %T %v", err, err)
	}
}

func TestOpenProtocolMismatch(t *testing.T) {
	usePorts(t, []string{"/dev/fake"}, func(name string) (transport.Port, error) {
		hello := vaultKeyHello()
		hello["protocol"] = 2
		return scriptedPort(t, name, hello), nil
	})
	_, err := Open(context.Background(), OpenOptions{Port: "/dev/fake"})
	var protocol *ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("want ProtocolError, got %T %v", err, err)
	}
}

func TestOpenExplicitPort(t *testing.T) {
	usePorts(t, []string{"/dev/fake"}, func(name string) (transport.Port, error) {
		return scriptedPort(t, name, vaultKeyHello()), nil
	})
	device, err := Open(context.Background(), OpenOptions{Port: "/dev/fake"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer device.Close()
	if device.PortName() != "/dev/fake" || device.Hello().Product != "VaultKey" {
		t.Fatalf("unexpected device: %s %+v", device.PortName(), device.Hello())
	}
}

func TestSessionListAllPagination(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request map[string]any
			if err := json.Unmarshal(line, &request); err != nil {
				t.Errorf("decode request: %v", err)
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
			respond(t, writer, request, map[string]any{
				"offset": offset, "limit": 25,
				"credential_total": 30, "totp_total": 0,
				"credentials": credentials, "totp": []any{},
			})
		}
	})
	device := &Session{client: client}
	all, err := device.ListAll(context.Background(), "", time.Second)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if all.CredentialTotal != 30 || len(all.Credentials) != 30 {
		t.Fatalf("unexpected aggregate: %+v", all)
	}
	if all.Offset != 0 || all.Limit != 30 {
		t.Fatalf("unexpected aggregate paging: offset=%d limit=%d", all.Offset, all.Limit)
	}
	seen := map[uint32]bool{}
	for _, entry := range all.Credentials {
		if seen[entry.ID] {
			t.Fatalf("duplicate ID %d", entry.ID)
		}
		seen[entry.ID] = true
	}
}

type idleEchoPort struct {
	mu      sync.Mutex
	empty   int
	pending []byte
	written bool
}

func (p *idleEchoPort) Name() string { return "idle" }

func (p *idleEchoPort) Read(buffer []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.empty < 150 {
		p.empty++
		return 0, nil
	}
	if len(p.pending) > 0 {
		count := copy(buffer, p.pending)
		p.pending = p.pending[count:]
		return count, nil
	}
	if p.written {
		return 0, io.EOF
	}
	return 0, nil
}

func (p *idleEchoPort) Write(data []byte) (int, error) {
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		return 0, err
	}
	id, _ := request["id"].(string)
	response, _ := json.Marshal(map[string]any{
		"v": 1, "id": id, "ok": true,
		"product": "VaultKey", "firmware": "1.0.0",
		"hardware": "rev-D", "protocol": 1,
	})
	p.mu.Lock()
	p.pending = append(response, '\n')
	p.written = true
	p.mu.Unlock()
	return len(data), nil
}

func (p *idleEchoPort) Close() error { return nil }

func TestIdleReaderSurvivesEmptyReads(t *testing.T) {
	originalDelay := idleReadDelay
	idleReadDelay = 0
	t.Cleanup(func() { idleReadDelay = originalDelay })

	client := New(&idleEchoPort{})
	defer client.Close()
	hello, err := client.Hello(context.Background())
	if err != nil {
		t.Fatalf("Hello after empty reads: %v", err)
	}
	if hello.Product != "VaultKey" {
		t.Fatalf("unexpected hello: %+v", hello)
	}
}

func TestSessionDoctorNeverExposesSecrets(t *testing.T) {
	client := newPipeClient(t, func(reader *bufio.Reader, writer *bufio.Writer) {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request map[string]any
			if err := json.Unmarshal(line, &request); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			switch request["cmd"] {
			case "GET_STATUS":
				respond(t, writer, request, map[string]any{
					"locked": false, "rtc": "ready", "storage": "ready",
					"startup": "home", "display": "ready", "button_event_drops": 0,
					"usb_driver": true, "usb_mounted": true, "cdc": true,
					"hid": true, "provisioning": true, "credentials": 1, "totp": 0,
				})
			case "GET_TIME":
				respond(t, writer, request, map[string]any{"unix": 1794528000, "utc": true})
			}
		}
	})
	device := &Session{
		client: client,
		hello:  Hello{Product: "VaultKey", Firmware: "1", Hardware: "rev-D", Protocol: 1},
	}
	report, err := device.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if !report.OK || len(report.Checks) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"password", "secret", "otpauth"} {
		if strings.Contains(strings.ToLower(string(encoded)), secret) {
			t.Fatalf("doctor report contains %q", secret)
		}
	}
}
