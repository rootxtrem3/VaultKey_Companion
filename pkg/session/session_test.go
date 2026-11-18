package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

type fakeDevice struct {
	mu sync.Mutex

	port  string
	hello vaultkey.Hello

	status vaultkey.Status
	clock  vaultkey.Clock
	list   vaultkey.ServiceList

	statusErr error
	addErr    error

	addCalls    int
	updateCalls int
	totpCalls   int
	deleteCalls int
	lockCalls   int
	closed      bool

	statusEntered chan struct{}
	statusRelease chan struct{}
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{
		port:  "/dev/fake",
		hello: vaultkey.Hello{Product: "VaultKey", Firmware: "1.0.0", Hardware: "rev-D", Protocol: 1},
		status: vaultkey.Status{
			Locked: false, RTC: "ready", Storage: "ready", Startup: "home",
			Display: "ready", USBDriver: true, USBMounted: true, CDC: true,
			HID: true, Provisioning: true,
		},
		clock: vaultkey.Clock{Unix: 1794528000, UTC: true},
		list: vaultkey.ServiceList{
			Credentials: []vaultkey.CredentialSummary{{ID: 1, Service: "Example"}},
			TOTP:        []vaultkey.TOTPSummary{},
		},
	}
}

func (f *fakeDevice) PortName() string      { return f.port }
func (f *fakeDevice) Hello() vaultkey.Hello { return f.hello }
func (f *fakeDevice) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeDevice) Status(ctx context.Context) (vaultkey.Status, error) {
	if f.statusEntered != nil {
		f.mu.Lock()
		entered := f.statusEntered
		release := f.statusRelease
		f.mu.Unlock()
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return vaultkey.Status{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return vaultkey.Status{}, f.statusErr
	}
	return f.status, nil
}

func (f *fakeDevice) Time(ctx context.Context) (vaultkey.Clock, error) { return f.clock, nil }

func (f *fakeDevice) SetTime(ctx context.Context, unix int64) (vaultkey.Clock, error) {
	f.clock = vaultkey.Clock{Unix: unix}
	return f.clock, nil
}

func (f *fakeDevice) List(ctx context.Context, query string, offset, limit uint32) (vaultkey.ServiceList, error) {
	return f.list, nil
}

func (f *fakeDevice) ListAll(ctx context.Context, query string, pageTimeout time.Duration) (vaultkey.ServiceList, error) {
	return f.list, nil
}

func (f *fakeDevice) AddCredential(ctx context.Context, input vaultkey.CredentialInput) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	if f.addErr != nil {
		return 0, f.addErr
	}
	return 7, nil
}

func (f *fakeDevice) UpdateCredential(ctx context.Context, id uint32, input vaultkey.CredentialInput) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	return id, nil
}

func (f *fakeDevice) AddTOTP(ctx context.Context, uri string) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.totpCalls++
	return 8, nil
}

func (f *fakeDevice) Delete(ctx context.Context, entryType vaultkey.EntryType, id uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	return nil
}

func (f *fakeDevice) Lock(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lockCalls++
	return nil
}

func (f *fakeDevice) Doctor(ctx context.Context) (vaultkey.DoctorReport, error) {
	return vaultkey.DoctorReport{OK: true}, nil
}

type sink struct {
	mu    sync.Mutex
	lines [][]byte
}

func (s *sink) emit(line []byte) error {
	s.mu.Lock()
	s.lines = append(s.lines, append([]byte(nil), line...))
	s.mu.Unlock()
	return nil
}

func (s *sink) decoded(t *testing.T) []map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := make([]map[string]any, 0, len(s.lines))
	for _, line := range s.lines {
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("invalid output line %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func (s *sink) reset() {
	s.mu.Lock()
	s.lines = nil
	s.mu.Unlock()
}

func newTestEngine(t *testing.T, device Device) *Engine {
	t.Helper()
	engine, err := New(context.Background(), Options{
		Connector:      func(ctx context.Context) (Device, error) { return device, nil },
		Version:        "test",
		Commit:         "abc",
		BuildDate:      "2026-01-01T00:00:00Z",
		DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func send(t *testing.T, engine *Engine, sink *sink, request string) map[string]any {
	t.Helper()
	if err := engine.Handle(context.Background(), []byte(request), sink.emit); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.lines) == 0 {
		t.Fatal("no response emitted")
	}
	var message map[string]any
	if err := json.Unmarshal(sink.lines[len(sink.lines)-1], &message); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return message
}

func result(t *testing.T, message map[string]any) map[string]any {
	t.Helper()
	if ok, _ := message["ok"].(bool); !ok {
		t.Fatalf("response failed: %v", message)
	}
	value, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %v", message)
	}
	return value
}

func errorCode(t *testing.T, message map[string]any) string {
	t.Helper()
	value, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error: %v", message)
	}
	code, _ := value["code"].(string)
	return code
}

func TestEngineCapabilitiesStatusAndIdentity(t *testing.T) {
	device := newFakeDevice()
	engine := newTestEngine(t, device)
	sink := &sink{}

	caps := result(t, send(t, engine, sink,
		`{"id":1,"method":"capabilities","params":{}}`))
	if caps["session_protocol"] != float64(1) {
		t.Fatalf("unexpected capabilities: %v", caps)
	}
	methods, _ := caps["methods"].([]any)
	if len(methods) != len(Methods) {
		t.Fatalf("methods: %v", caps["methods"])
	}

	hello := result(t, send(t, engine, sink, `{"id":2,"method":"hello"}`))
	if hello["port"] != "/dev/fake" {
		t.Fatalf("unexpected hello: %v", hello)
	}

	status := result(t, send(t, engine, sink, `{"id":3,"method":"status"}`))
	if status["provisioning"] != true {
		t.Fatalf("unexpected status: %v", status)
	}
}

func TestEngineAllDeviceMethods(t *testing.T) {
	device := newFakeDevice()
	engine := newTestEngine(t, device)
	sink := &sink{}

	timeResult := result(t, send(t, engine, sink, `{"id":1,"method":"time.get"}`))
	if timeResult["unix"] != float64(1794528000) {
		t.Fatalf("unexpected time: %v", timeResult)
	}
	setResult := result(t, send(t, engine, sink,
		`{"id":2,"method":"time.set","params":{"unix":1800000000}}`))
	if setResult["set"].(map[string]any)["unix"] != float64(1800000000) {
		t.Fatalf("unexpected time.set: %v", setResult)
	}
	listResult := result(t, send(t, engine, sink,
		`{"id":3,"method":"list","params":{"all":true}}`))
	if listResult["credential_total"] != float64(0) &&
		listResult["credential_total"] != float64(1) {
		t.Fatalf("unexpected list: %v", listResult)
	}
	add := result(t, send(t, engine, sink,
		`{"id":4,"method":"credentials.add","params":{"service":"GitHub","username":"u","password":"p"}}`))
	if add["entry_id"] != float64(7) {
		t.Fatalf("unexpected add: %v", add)
	}
	update := result(t, send(t, engine, sink,
		`{"id":5,"method":"credentials.update","params":{"id":7,"service":"GitHub","password":"p2"}}`))
	if update["entry_id"] != float64(7) {
		t.Fatalf("unexpected update: %v", update)
	}
	totp := result(t, send(t, engine, sink,
		`{"id":6,"method":"totp.add","params":{"uri":"otpauth://totp/Example:u?secret=JBSWY3DPEHPK3PXP"}}`))
	if totp["entry_id"] != float64(8) {
		t.Fatalf("unexpected totp add: %v", totp)
	}
	deleted := result(t, send(t, engine, sink,
		`{"id":7,"method":"delete","params":{"type":"credential","id":7}}`))
	if deleted["deleted"] != true {
		t.Fatalf("unexpected delete: %v", deleted)
	}
	locked := result(t, send(t, engine, sink, `{"id":8,"method":"lock"}`))
	if locked["locked"] != true {
		t.Fatalf("unexpected lock: %v", locked)
	}
	doctor := result(t, send(t, engine, sink, `{"id":9,"method":"doctor"}`))
	if doctor["ok"] != true {
		t.Fatalf("unexpected doctor: %v", doctor)
	}

	device.mu.Lock()
	defer device.mu.Unlock()
	if device.addCalls != 1 || device.updateCalls != 1 || device.totpCalls != 1 ||
		device.deleteCalls != 1 || device.lockCalls != 1 {
		t.Fatalf("device calls: %+v", device)
	}
}

func TestEngineErrorsStayMachineReadable(t *testing.T) {
	device := newFakeDevice()
	engine := newTestEngine(t, device)
	sink := &sink{}

	if code := errorCode(t, send(t, engine, sink, `not json`)); code != "invalid_json" {
		t.Fatalf("invalid json code %q", code)
	}
	if code := errorCode(t, send(t, engine, sink,
		`{"id":1,"method":"nope"}`)); code != "unknown_method" {
		t.Fatalf("unknown method code %q", code)
	}
	if code := errorCode(t, send(t, engine, sink,
		`{"id":2,"method":"delete","params":{"type":"bogus","id":1}}`)); code != "invalid_params" {
		t.Fatalf("invalid params code %q", code)
	}
	if code := errorCode(t, send(t, engine, sink,
		`{"id":3,"method":"credentials.update","params":{"id":0,"service":"x","password":"p"}}`)); code != "invalid_id" {
		t.Fatalf("invalid id code %q", code)
	}
	if code := errorCode(t, send(t, engine, sink,
		`{"id":4,"method":"list","params":{"limit":99}}`)); code != "invalid_params" {
		t.Fatalf("limit code %q", code)
	}

	device.mu.Lock()
	device.statusErr = &vaultkey.DeviceError{Code: "device_locked"}
	device.mu.Unlock()
	locked := send(t, engine, sink, `{"id":5,"method":"status"}`)
	info := locked["error"].(map[string]any)
	if info["kind"] != "locked" || info["exit_code"] != float64(4) {
		t.Fatalf("unexpected locked info: %v", info)
	}
}

func TestEngineDoesNotRetryAmbiguousMutation(t *testing.T) {
	device := newFakeDevice()
	device.addErr = &vaultkey.AmbiguousError{
		Command: "ADD_CREDENTIAL", Cause: context.DeadlineExceeded,
	}
	engine := newTestEngine(t, device)
	sink := &sink{}

	message := send(t, engine, sink,
		`{"id":1,"method":"credentials.add","params":{"service":"GitHub","password":"p"}}`)
	info := message["error"].(map[string]any)
	if info["kind"] != "ambiguous" || info["ambiguous"] != true || info["exit_code"] != float64(7) {
		t.Fatalf("unexpected ambiguous error: %v", info)
	}
	device.mu.Lock()
	defer device.mu.Unlock()
	if device.addCalls != 1 {
		t.Fatalf("mutating command called %d times", device.addCalls)
	}
}

func TestEngineKeepsSecretsOutOfDebugOutput(t *testing.T) {
	device := newFakeDevice()
	var logs []string
	var logsMu sync.Mutex
	engine, err := New(context.Background(), Options{
		Connector:      func(ctx context.Context) (Device, error) { return device, nil },
		DefaultTimeout: time.Second,
		Debug:          true,
		Logger: func(message string) {
			logsMu.Lock()
			logs = append(logs, message)
			logsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer engine.Close()
	sink := &sink{}
	send(t, engine, sink,
		`{"id":1,"method":"credentials.add","params":{"service":"GitHub","password":"super-secret-password"}}`)
	send(t, engine, sink,
		`{"id":2,"method":"totp.add","params":{"uri":"otpauth://totp/X?secret=JBSWY3DPEHPK3PXP"}}`)

	logsMu.Lock()
	joined := strings.Join(logs, "\n")
	logsMu.Unlock()
	for _, secret := range []string{"super-secret-password", "JBSWY3DPEHPK3PXP"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("debug log contains %q: %s", secret, joined)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, line := range sink.lines {
		for _, secret := range []string{"super-secret-password", "JBSWY3DPEHPK3PXP"} {
			if bytes.Contains(line, []byte(secret)) {
				t.Fatalf("response contains %q: %s", secret, line)
			}
		}
	}
}

func TestEngineBusyAndSerializedEmit(t *testing.T) {
	device := newFakeDevice()
	device.statusEntered = make(chan struct{})
	device.statusRelease = make(chan struct{})
	engine := newTestEngine(t, device)

	var active, maximum int32
	sinkFn := func(line []byte) error {
		current := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maximum)
			if current <= old || atomic.CompareAndSwapInt32(&maximum, old, current) {
				break
			}
		}
		time.Sleep(300 * time.Microsecond)
		atomic.AddInt32(&active, -1)
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- engine.Handle(context.Background(),
			[]byte(`{"id":1,"method":"status"}`), sinkFn)
	}()
	<-device.statusEntered

	if err := engine.Handle(context.Background(),
		[]byte(`{"id":2,"method":"capabilities"}`), sinkFn); err != nil {
		t.Fatalf("busy Handle: %v", err)
	}
	close(device.statusRelease)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Handle: %v", err)
	}

	for index := 0; index < 5; index++ {
		if err := engine.Handle(context.Background(),
			[]byte(fmt.Sprintf(`{"id":%d,"method":"capabilities"}`, 10+index)), sinkFn); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if atomic.LoadInt32(&maximum) != 1 {
		t.Fatalf("emitter ran concurrently: max=%d", maximum)
	}
}

func TestEngineWatchEventsAndStop(t *testing.T) {
	device := newFakeDevice()
	engine := newTestEngine(t, device)
	sink := &sink{}

	result(t, send(t, engine, sink,
		`{"id":1,"method":"watch.start","params":{"interval_ms":250}}`))
	deadline := time.Now().Add(2 * time.Second)
	for {
		statuses := 0
		for _, message := range sink.decoded(t) {
			if message["type"] == "event" && message["event"] == "status" {
				statuses++
			}
		}
		if statuses >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not receive two status events: %v", sink.decoded(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	result(t, send(t, engine, sink, `{"id":2,"method":"watch.stop"}`))

	sink.reset()
	result(t, send(t, engine, sink,
		`{"id":3,"method":"watch.start","params":{"interval_ms":250}}`))
	device.mu.Lock()
	device.statusErr = vaultkey.ErrClosed
	device.mu.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for {
		disconnected := false
		for _, message := range sink.decoded(t) {
			if message["event"] == "disconnected" {
				disconnected = true
			}
		}
		if disconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no disconnected event: %v", sink.decoded(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEngineReconnectAndShutdown(t *testing.T) {
	device := newFakeDevice()
	replacement := newFakeDevice()
	replacement.port = "/dev/new"
	connects := 0
	engine, err := New(context.Background(), Options{
		Connector: func(ctx context.Context) (Device, error) {
			connects++
			return device, nil
		},
		Reconnect: func(ctx context.Context, port string) (Device, error) {
			return replacement, nil
		},
		DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer engine.Close()
	sink := &sink{}

	reconnected := result(t, send(t, engine, sink,
		`{"id":1,"method":"reconnect","params":{"port":"/dev/new"}}`))
	if reconnected["port"] != "/dev/new" {
		t.Fatalf("unexpected reconnect: %v", reconnected)
	}
	device.mu.Lock()
	if !device.closed {
		t.Error("old device was not closed")
	}
	device.mu.Unlock()

	stopped := result(t, send(t, engine, sink, `{"id":2,"method":"shutdown"}`))
	if stopped["shutting_down"] != true || !engine.ShutdownRequested() {
		t.Fatalf("unexpected shutdown: %v", stopped)
	}
}

func TestRunHandlesMalformedAndOversizedLines(t *testing.T) {
	device := newFakeDevice()
	input := strings.Join([]string{
		`not json`,
		`{"id":1,"method":"capabilities"}`,
		strings.Repeat("x", MaxRequestLine+1),
		`{"id":2,"method":"shutdown"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := Run(context.Background(), strings.NewReader(input), &out, &errOut, Options{
		Connector:      func(ctx context.Context) (Device, error) { return device, nil },
		DefaultTimeout: time.Second,
	})
	if code != 0 {
		t.Fatalf("Run exit %d, stderr=%s", code, errOut.String())
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) < 5 {
		t.Fatalf("unexpected output: %s", out.String())
	}
	var ready map[string]any
	if err := json.Unmarshal(lines[0], &ready); err != nil {
		t.Fatalf("ready decode: %v", err)
	}
	if ready["event"] != "ready" {
		t.Fatalf("first line is not ready: %s", lines[0])
	}
	var sawInvalid, sawTooLong, sawCapabilities, sawShutdown bool
	for _, line := range lines {
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("output line: %v", err)
		}
		if message["event"] == "ready" {
			continue
		}
		info, _ := message["error"].(map[string]any)
		if info != nil {
			switch info["code"] {
			case "invalid_json":
				sawInvalid = true
			case "line_too_long":
				sawTooLong = true
			}
		}
		if message["ok"] == true {
			value, _ := message["result"].(map[string]any)
			if _, ok := value["session_protocol"]; ok {
				sawCapabilities = true
			}
			if value["shutting_down"] == true {
				sawShutdown = true
			}
		}
	}
	if !sawInvalid || !sawTooLong || !sawCapabilities || !sawShutdown {
		t.Fatalf("missing responses: invalid=%t tooLong=%t capabilities=%t shutdown=%t\n%s",
			sawInvalid, sawTooLong, sawCapabilities, sawShutdown, out.String())
	}
}

func TestRunEOFClosesDevice(t *testing.T) {
	device := newFakeDevice()
	var out bytes.Buffer
	code := Run(context.Background(), strings.NewReader(""), &out, &bytes.Buffer{}, Options{
		Connector:      func(ctx context.Context) (Device, error) { return device, nil },
		DefaultTimeout: time.Second,
	})
	if code != 0 {
		t.Fatalf("Run exit %d", code)
	}
	device.mu.Lock()
	defer device.mu.Unlock()
	if !device.closed {
		t.Fatal("device was not closed on EOF")
	}
}

func TestRunStartupFailureEmitsFatal(t *testing.T) {
	var out bytes.Buffer
	code := Run(context.Background(), strings.NewReader(""), &out, &bytes.Buffer{}, Options{
		Connector: func(ctx context.Context) (Device, error) {
			return nil, errors.New("no device")
		},
	})
	if code != vaultkey.ExitFailure {
		t.Fatalf("Run exit %d, want %d", code, vaultkey.ExitFailure)
	}
	var message map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &message); err != nil {
		t.Fatalf("fatal decode: %v", err)
	}
	if message["event"] != "fatal" {
		t.Fatalf("unexpected fatal event: %v", message)
	}
}
