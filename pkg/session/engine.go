package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/apperr"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

type response struct {
	Type   string              `json:"type"`
	ID     uint64              `json:"id"`
	OK     bool                `json:"ok"`
	Result any                 `json:"result,omitempty"`
	Error  *vaultkey.ErrorInfo `json:"error,omitempty"`
}

type event struct {
	Type   string              `json:"type"`
	Event  string              `json:"event"`
	Result any                 `json:"result,omitempty"`
	Error  *vaultkey.ErrorInfo `json:"error,omitempty"`
}

// Engine owns one device and serializes all access to it.
type Engine struct {
	options Options

	opMu   sync.Mutex
	device Device

	inFlight atomic.Bool

	emitMu sync.Mutex
	emit   func([]byte) error

	watchMu   sync.Mutex
	watching  bool
	interval  time.Duration
	watchStop chan struct{}
	watchDone chan struct{}

	shutdown atomic.Bool

	closeOnce sync.Once
	closeErr  error
}

// New creates the engine and opens the initial device with the connector.
func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Connector == nil {
		return nil, &LocalError{
			ErrorKind: "usage", CodeName: "invalid_params",
			Message: "session: a connector is required", Exit: vaultkey.ExitUsage,
		}
	}
	device, err := options.Connector(ctx)
	if err != nil {
		return nil, err
	}
	return &Engine{options: options, device: device}, nil
}

// ShutdownRequested reports whether a shutdown response was sent.
func (e *Engine) ShutdownRequested() bool { return e.shutdown.Load() }

// Close stops watching and closes the device once.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		e.stopWatch()
		e.opMu.Lock()
		defer e.opMu.Unlock()
		if e.device != nil {
			e.closeErr = e.device.Close()
			e.device = nil
		}
	})
	return e.closeErr
}

func (e *Engine) logf(format string, arguments ...any) {
	if !e.options.Debug {
		return
	}
	if e.options.Logger != nil {
		e.options.Logger(fmt.Sprintf(format, arguments...))
	}
}

func (e *Engine) setEmitter(emit func([]byte) error) {
	e.emitMu.Lock()
	e.emit = emit
	e.emitMu.Unlock()
}

func (e *Engine) emitLine(line []byte) error {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	if e.emit == nil {
		return errNoEmitter
	}
	return e.emit(line)
}

// Handle processes one request line and emits exactly one response. A second
// concurrent request is rejected with a busy error instead of being queued.
func (e *Engine) Handle(ctx context.Context, line []byte, emit func([]byte) error) error {
	e.setEmitter(emit)

	var envelope requestEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return e.respond(0, nil, &LocalError{
			CodeName: "invalid_json", Message: "request is not a JSON object",
		})
	}
	id, idErr := parseRequestID(envelope.ID)
	if idErr != nil {
		return e.respond(0, nil, &LocalError{
			CodeName: "invalid_id", Message: "request id must be an unsigned integer",
		})
	}

	if !e.inFlight.CompareAndSwap(false, true) {
		return e.respond(id, nil, &LocalError{
			ErrorKind: "busy", CodeName: "busy",
			Message: "another request is already in flight",
			Exit:    vaultkey.ExitFailure,
		})
	}
	defer e.inFlight.Store(false)

	timeout := e.options.timeout()
	if envelope.TimeoutMS != nil {
		timeout = clampTimeout(time.Duration(*envelope.TimeoutMS) * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	e.logf("session: %s request=%dB", envelope.Method, len(line))
	result, err := e.dispatch(ctx, envelope.Method, envelope.Params)
	if err != nil {
		info := apperr.Classify(err)
		e.logf("session: %s error=%s (%.1fms)", envelope.Method, info.Code,
			float64(time.Since(started).Microseconds())/1000)
	} else {
		e.logf("session: %s ok (%.1fms)", envelope.Method,
			float64(time.Since(started).Microseconds())/1000)
	}
	if responseErr := e.respond(id, result, err); responseErr != nil {
		return responseErr
	}
	if err == nil && envelope.Method == "shutdown" {
		e.shutdown.Store(true)
	}
	return nil
}

type requestEnvelope struct {
	ID        json.RawMessage `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	TimeoutMS *int64          `json:"timeout_ms"`
}

func parseRequestID(raw json.RawMessage) (uint64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" || text == "0" {
		return 0, nil
	}
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, `"`) {
		return 0, errors.New("invalid id")
	}
	return strconv.ParseUint(text, 10, 64)
}

func clampTimeout(timeout time.Duration) time.Duration {
	if timeout < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	if timeout > 120*time.Second {
		return 120 * time.Second
	}
	return timeout
}

func (e *Engine) respond(id uint64, result any, err error) error {
	message := response{Type: "response", ID: id, OK: err == nil, Result: result}
	if err != nil {
		info := apperr.Classify(err)
		message.OK = false
		message.Result = nil
		message.Error = &info
	}
	line, marshalErr := marshalLine(message)
	if marshalErr != nil {
		fallback := response{
			Type: "response", ID: id, OK: false,
			Error: &vaultkey.ErrorInfo{
				Kind: "internal", Code: "internal_error",
				Message: "could not encode response", ExitCode: vaultkey.ExitFailure,
			},
		}
		line, _ = marshalLine(fallback)
	}
	return e.emitLine(line)
}

func (e *Engine) emitEvent(name string, result any, err error) error {
	message := event{Type: "event", Event: name, Result: result}
	if err != nil {
		info := apperr.Classify(err)
		message.Error = &info
	}
	line, marshalErr := marshalLine(message)
	if marshalErr != nil {
		return marshalErr
	}
	return e.emitLine(line)
}

func marshalLine(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func deviceCall[T any](e *Engine, ctx context.Context, action func(Device) (T, error)) (T, error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	var zero T
	if e.device == nil {
		return zero, noDeviceError()
	}
	return action(e.device)
}

func decodeParams(raw json.RawMessage, target any) error {
	text := bytes.TrimSpace(raw)
	if len(text) == 0 || bytes.Equal(text, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(text, target); err != nil {
		return invalidParams("params could not be decoded")
	}
	return nil
}

func (e *Engine) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "capabilities":
		return CapabilitiesFor(e.options), nil

	case "hello":
		return deviceCall(e, ctx, func(device Device) (helloResult, error) {
			return helloResult{Port: device.PortName(), Device: device.Hello()}, nil
		})

	case "status":
		return deviceCall(e, ctx, func(device Device) (vaultkey.Status, error) {
			return device.Status(ctx)
		})

	case "time.get":
		return deviceCall(e, ctx, func(device Device) (vaultkey.Clock, error) {
			return device.Time(ctx)
		})

	case "time.set":
		return e.setTime(ctx, params)

	case "list":
		return e.list(ctx, params)

	case "credentials.add":
		return e.addCredential(ctx, params)

	case "credentials.update":
		return e.updateCredential(ctx, params)

	case "totp.add":
		return e.addTOTP(ctx, params)

	case "delete":
		return e.deleteEntry(ctx, params)

	case "lock":
		return deviceCall(e, ctx, func(device Device) (map[string]bool, error) {
			return map[string]bool{"locked": true}, device.Lock(ctx)
		})

	case "watch.start":
		return e.watchStart(params)

	case "watch.stop":
		e.stopWatch()
		return map[string]bool{"watching": false}, nil

	case "reconnect":
		return e.reconnect(ctx, params)

	case "shutdown":
		return map[string]bool{"shutting_down": true}, nil

	case "doctor":
		report, err := deviceCall(e, ctx, func(device Device) (vaultkey.DoctorReport, error) {
			return device.Doctor(ctx)
		})
		report.App = vaultkey.AppInfo{
			Version: e.options.Version, Commit: e.options.Commit,
			BuildDate: e.options.BuildDate, Platform: CapabilitiesFor(e.options).App.Platform,
		}
		return report, err
	}

	return nil, &LocalError{
		CodeName: "unknown_method", Message: "unknown method: " + method,
		Exit: vaultkey.ExitUsage,
	}
}

type helloResult struct {
	Port   string         `json:"port"`
	Device vaultkey.Hello `json:"device"`
}

type timeSetParams struct {
	Unix    *int64  `json:"unix"`
	RFC3339 *string `json:"rfc3339"`
}

type timeSetResult struct {
	Before   *vaultkey.Clock `json:"before"`
	Set      vaultkey.Clock  `json:"set"`
	Verified *vaultkey.Clock `json:"verified"`
	Warning  string          `json:"warning,omitempty"`
}

func (e *Engine) setTime(ctx context.Context, raw json.RawMessage) (any, error) {
	var params timeSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Unix != nil && params.RFC3339 != nil {
		return nil, invalidParams("use only one of unix and rfc3339")
	}
	if params.Unix == nil && params.RFC3339 == nil {
		return nil, invalidParams("unix or rfc3339 is required")
	}
	var unix int64
	if params.Unix != nil {
		unix = *params.Unix
	} else {
		parsed, err := time.Parse(time.RFC3339, *params.RFC3339)
		if err != nil {
			return nil, &LocalError{CodeName: "invalid_time", Message: "invalid RFC3339 time"}
		}
		unix = parsed.Unix()
	}
	if err := vaultkey.ValidateUnix(unix); err != nil {
		return nil, err
	}

	result := timeSetResult{}
	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, 2*time.Second)
	}
	beforeCtx, beforeCancel := short()
	if before, err := deviceCall(e, beforeCtx, func(device Device) (vaultkey.Clock, error) {
		return device.Time(beforeCtx)
	}); err == nil {
		result.Before = &before
	}
	beforeCancel()

	clock, err := deviceCall(e, ctx, func(device Device) (vaultkey.Clock, error) {
		return device.SetTime(ctx, unix)
	})
	if err != nil {
		return nil, err
	}
	// SET_TIME writes UTC; the firmware response omits the GET_TIME utc flag.
	clock.UTC = true
	result.Set = clock

	verifyCtx, verifyCancel := short()
	verified, verifyErr := deviceCall(e, verifyCtx, func(device Device) (vaultkey.Clock, error) {
		return device.Time(verifyCtx)
	})
	verifyCancel()
	if verifyErr == nil {
		result.Verified = &verified
	} else {
		result.Warning = "time was written but verification failed: " + verifyErr.Error()
	}
	return result, nil
}

type listParams struct {
	Query  string `json:"query"`
	Offset uint32 `json:"offset"`
	Limit  uint32 `json:"limit"`
	All    bool   `json:"all"`
}

func (e *Engine) list(ctx context.Context, raw json.RawMessage) (any, error) {
	var params listParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit > vaultkey.MaxPageLimit {
		return nil, invalidParams("limit must be 1-25")
	}
	if params.Offset > vaultkey.MaxPageOffset {
		return nil, invalidParams("offset must be 0-256")
	}
	if params.All && params.Offset != 0 {
		return nil, invalidParams("all cannot be combined with offset")
	}
	if len(params.Query) > vaultkey.MaxQuery {
		return nil, &LocalError{
			CodeName: "value_too_long",
			Message:  fmt.Sprintf("query exceeds %d bytes", vaultkey.MaxQuery),
		}
	}
	if params.All {
		return deviceCall(e, ctx, func(device Device) (vaultkey.ServiceList, error) {
			return device.ListAll(ctx, params.Query, e.options.timeout())
		})
	}
	return deviceCall(e, ctx, func(device Device) (vaultkey.ServiceList, error) {
		return device.List(ctx, params.Query, params.Offset, params.Limit)
	})
}

type credentialParams struct {
	Service  string `json:"service"`
	Username string `json:"username"`
	Password string `json:"password"`
	URL      string `json:"url"`
	Notes    string `json:"notes"`
}

func (p credentialParams) input() vaultkey.CredentialInput {
	return vaultkey.CredentialInput{
		Service: p.Service, Username: p.Username, Password: p.Password,
		URL: p.URL, Notes: p.Notes,
	}
}

func (e *Engine) addCredential(ctx context.Context, raw json.RawMessage) (any, error) {
	var params credentialParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	input := params.input()
	if err := vaultkey.ValidateCredential(input); err != nil {
		return nil, err
	}
	return deviceCall(e, ctx, func(device Device) (map[string]uint32, error) {
		id, err := device.AddCredential(ctx, input)
		return map[string]uint32{"entry_id": id}, err
	})
}

type updateCredentialParams struct {
	credentialParams
	ID uint32 `json:"id"`
}

func (e *Engine) updateCredential(ctx context.Context, raw json.RawMessage) (any, error) {
	var params updateCredentialParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.ID == 0 {
		return nil, &LocalError{CodeName: "invalid_id", Message: "id must be a positive entry id"}
	}
	input := params.input()
	if err := vaultkey.ValidateCredential(input); err != nil {
		return nil, err
	}
	return deviceCall(e, ctx, func(device Device) (map[string]uint32, error) {
		id, err := device.UpdateCredential(ctx, params.ID, input)
		return map[string]uint32{"entry_id": id}, err
	})
}

func (e *Engine) addTOTP(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := vaultkey.ValidateTOTPURI(params.URI); err != nil {
		return nil, err
	}
	return deviceCall(e, ctx, func(device Device) (map[string]uint32, error) {
		id, err := device.AddTOTP(ctx, params.URI)
		return map[string]uint32{"entry_id": id}, err
	})
}

func (e *Engine) deleteEntry(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Type string `json:"type"`
		ID   uint32 `json:"id"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	entryType := vaultkey.EntryType(params.Type)
	if entryType != vaultkey.CredentialEntry && entryType != vaultkey.TOTPEntry {
		return nil, invalidParams(`type must be "credential" or "totp"`)
	}
	if params.ID == 0 {
		return nil, &LocalError{CodeName: "invalid_id", Message: "id must be a positive entry id"}
	}
	return deviceCall(e, ctx, func(device Device) (map[string]any, error) {
		err := device.Delete(ctx, entryType, params.ID)
		return map[string]any{
			"deleted": err == nil, "type": params.Type, "entry_id": params.ID,
		}, err
	})
}

func (e *Engine) watchStart(raw json.RawMessage) (any, error) {
	var params struct {
		IntervalMS *int64 `json:"interval_ms"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	interval := 2 * time.Second
	if params.IntervalMS != nil {
		interval = time.Duration(*params.IntervalMS) * time.Millisecond
	}
	if interval < 250*time.Millisecond || interval > 60*time.Second {
		return nil, invalidParams("interval_ms must be 250-60000")
	}
	e.startWatch(interval)
	return map[string]any{"watching": true, "interval_ms": interval.Milliseconds()}, nil
}

func (e *Engine) startWatch(interval time.Duration) {
	e.stopWatch()
	e.emitStatus()
	stop := make(chan struct{})
	done := make(chan struct{})
	e.watchMu.Lock()
	e.watching = true
	e.interval = interval
	e.watchStop = stop
	e.watchDone = done
	e.watchMu.Unlock()
	go e.watchLoop(interval, stop, done)
}

func (e *Engine) stopWatch() {
	e.watchMu.Lock()
	stop := e.watchStop
	e.watchStop = nil
	e.watchDone = nil
	e.watching = false
	e.watchMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (e *Engine) markWatchStopped() {
	e.watchMu.Lock()
	e.watching = false
	e.watchStop = nil
	e.watchDone = nil
	e.watchMu.Unlock()
}

func (e *Engine) watchLoop(interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !e.emitStatus() {
				return
			}
		}
	}
}

func (e *Engine) emitStatus() bool {
	watchCtx, cancel := context.WithTimeout(context.Background(), e.options.timeout())
	defer cancel()
	status, err := deviceCall(e, watchCtx, func(device Device) (vaultkey.Status, error) {
		return device.Status(watchCtx)
	})
	if err != nil {
		info := apperr.Classify(err)
		_ = e.emitEvent("watch_error", nil, err)
		if info.Kind == "connection" || info.Kind == "timeout" {
			_ = e.emitEvent("disconnected", nil, err)
			e.markWatchStopped()
			return false
		}
		return true
	}
	result := struct {
		Time   string          `json:"time"`
		Status vaultkey.Status `json:"status"`
	}{Time: time.Now().UTC().Format(time.RFC3339), Status: status}
	return e.emitEvent("status", result, nil) == nil
}

func (e *Engine) reconnect(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Port string `json:"port"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	e.stopWatch()

	e.opMu.Lock()
	defer e.opMu.Unlock()
	if e.device != nil {
		_ = e.device.Close()
		e.device = nil
	}

	var device Device
	var err error
	if e.options.Reconnect != nil {
		device, err = e.options.Reconnect(ctx, params.Port)
	} else {
		if strings.TrimSpace(params.Port) != "" {
			return nil, invalidParams("this session cannot reconnect to a different port")
		}
		device, err = e.options.Connector(ctx)
	}
	if err != nil {
		return nil, err
	}
	e.device = device
	return helloResult{Port: device.PortName(), Device: device.Hello()}, nil
}
