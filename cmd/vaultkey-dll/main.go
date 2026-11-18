//go:build windows && cgo

package main

/*
#include <stdlib.h>
*/
import "C"

// Prototype Windows DLL for the VaultKey companion.
//
// This file is experimental: it wraps the existing internal protocol client
// behind one JSON-in/JSON-out entry point so a UI can call it via P/Invoke.
// It is not part of the 0.2.0 release plan and is not wired into the Makefile.
//
// ABI:
//
//	const char *VKVersion(void);          // caller frees with VKFree
//	const char *VKCall(const char *json); // caller frees with VKFree
//	void VKFree(const char *p);
//	int VKSessionProtocol(void);
//
// Requests:  {"id":1,"method":"status","params":{},"timeout_ms":5000}
// Responses: {"id":1,"ok":true,"result":{...}}
//            {"id":1,"ok":false,"error":{"kind":"...","code":"...","message":"...","exit_code":N}}
//
// Secrets travel in the request JSON, never in argv. Nothing is logged.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/rootxtrem3/VaultKey_Companion/internal/dllapi"
	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// Keep the bridge code terse while the mapping lives in a testable package.
type errorInfo = dllapi.Info

var (
	usageError      = dllapi.Usage
	connectionError = dllapi.Connection
	classify        = dllapi.Classify
)

var (
	version   = "0.1.0"
	commit    = "unknown"
	buildDate = "unknown"
)

const (
	sessionProtocol   = 1
	defaultTimeout    = 5 * time.Second
	maxRequestTimeout = 2 * time.Minute
	minRequestTimeout = 100 * time.Millisecond
)

var (
	stateMu     sync.Mutex
	active      *vaultkey.Client
	activeHello vaultkey.Hello
	activePort  string
)

type callRequest struct {
	ID        uint64          `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	TimeoutMS int             `json:"timeout_ms"`
}

type callResponse struct {
	ID     uint64     `json:"id,omitempty"`
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *errorInfo `json:"error,omitempty"`
}

func fail(err *errorInfo) callResponse {
	return callResponse{OK: false, Error: err}
}

func decodeParams(raw json.RawMessage, target any) *errorInfo {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return usageError("invalid_params", "params are not valid for this method")
	}
	return nil
}

func requestTimeout(milliseconds int) time.Duration {
	if milliseconds == 0 {
		return defaultTimeout
	}
	timeout := time.Duration(milliseconds) * time.Millisecond
	if timeout < minRequestTimeout {
		return minRequestTimeout
	}
	if timeout > maxRequestTimeout {
		return maxRequestTimeout
	}
	return timeout
}

func openAndCheck(ctx context.Context, port string) (*vaultkey.Client, vaultkey.Hello, error) {
	raw, err := transport.Open(port)
	if err != nil {
		return nil, vaultkey.Hello{}, err
	}
	client := vaultkey.New(raw)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	hello, err := client.Hello(probeCtx)
	if err != nil {
		client.Close()
		return nil, vaultkey.Hello{}, err
	}
	if hello.Product != "VaultKey" {
		client.Close()
		return nil, vaultkey.Hello{}, fmt.Errorf(
			"%s responded as %q, not VaultKey", port, hello.Product)
	}
	if hello.Protocol != vaultkey.ProtocolVersion {
		client.Close()
		return nil, vaultkey.Hello{}, &vaultkey.ProtocolError{Message: fmt.Sprintf(
			"device protocol is %d; this app supports %d", hello.Protocol, vaultkey.ProtocolVersion)}
	}
	return client, hello, nil
}

func openDevice(ctx context.Context, requested string) (*vaultkey.Client, vaultkey.Hello, string, error) {
	if requested != "" {
		port := strings.TrimSpace(requested)
		client, hello, err := openAndCheck(ctx, port)
		if err != nil {
			return nil, vaultkey.Hello{}, "", err
		}
		return client, hello, port, nil
	}

	ports, err := transport.Candidates()
	if err != nil {
		return nil, vaultkey.Hello{}, "", fmt.Errorf("enumerate serial ports: %w", err)
	}
	if len(ports) == 0 {
		return nil, vaultkey.Hello{}, "", fmt.Errorf("no candidate VaultKey serial ports were found")
	}

	type match struct {
		client *vaultkey.Client
		hello  vaultkey.Hello
		port   string
	}
	matches := make([]match, 0, 1)
	tried := make([]string, 0, len(ports))
	for _, port := range ports {
		client, hello, err := openAndCheck(ctx, port)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s: %v", port, err))
			continue
		}
		matches = append(matches, match{client: client, hello: hello, port: port})
	}

	switch len(matches) {
	case 0:
		return nil, vaultkey.Hello{}, "", fmt.Errorf(
			"no VaultKey answered on the candidate ports: %s", strings.Join(tried, "; "))
	case 1:
		return matches[0].client, matches[0].hello, matches[0].port, nil
	default:
		ports := make([]string, 0, len(matches))
		for _, m := range matches {
			ports = append(ports, m.port)
			if m.client != nil {
				m.client.Close()
			}
		}
		return nil, vaultkey.Hello{}, "", fmt.Errorf(
			"multiple VaultKey devices found: %s", strings.Join(ports, ", "))
	}
}

func closeActive() error {
	if active == nil {
		return nil
	}
	err := active.Close()
	active = nil
	activeHello = vaultkey.Hello{}
	activePort = ""
	return err
}

func requireDevice() (*vaultkey.Client, *errorInfo) {
	if active == nil {
		return nil, connectionError("not_open", "no device is open",
			"call open first, with an explicit port or an empty string to auto-detect.")
	}
	return active, nil
}

func listAll(ctx context.Context, client *vaultkey.Client, query string) (vaultkey.ServiceList, error) {
	aggregate := vaultkey.ServiceList{
		Credentials: []vaultkey.CredentialSummary{},
		TOTP:        []vaultkey.TOTPSummary{},
	}
	seenCredentials := make(map[uint32]struct{})
	seenTOTP := make(map[uint32]struct{})
	offset := uint32(0)
	const limit = vaultkey.MaxPageLimit

	for page := 0; page < 64; page++ {
		result, err := client.List(ctx, query, offset, limit)
		if err != nil {
			return aggregate, err
		}
		aggregate.CredentialTotal = result.CredentialTotal
		aggregate.TOTPTotal = result.TOTPTotal
		for _, entry := range result.Credentials {
			if _, exists := seenCredentials[entry.ID]; !exists {
				seenCredentials[entry.ID] = struct{}{}
				aggregate.Credentials = append(aggregate.Credentials, entry)
			}
		}
		for _, entry := range result.TOTP {
			if _, exists := seenTOTP[entry.ID]; !exists {
				seenTOTP[entry.ID] = struct{}{}
				aggregate.TOTP = append(aggregate.TOTP, entry)
			}
		}
		if offset+limit >= result.CredentialTotal && offset+limit >= result.TOTPTotal {
			break
		}
		offset += limit
		if offset > vaultkey.MaxPageOffset {
			return aggregate, &vaultkey.RequestError{Message: "pagination exceeded the device limit"}
		}
	}
	return aggregate, nil
}

var methodList = []string{
	"capabilities", "open", "close", "reconnect", "hello",
	"status", "time.get", "time.set", "list",
	"credentials.add", "credentials.update", "totp.add", "delete", "lock",
}

func capabilities() map[string]any {
	return map[string]any{
		"session_protocol": sessionProtocol,
		"app": map[string]string{
			"version":    version,
			"commit":     commit,
			"build_date": buildDate,
			"platform":   runtime.GOOS + "/" + runtime.GOARCH,
		},
		"protocol": vaultkey.ProtocolVersion,
		"limits": map[string]int{
			"service":      vaultkey.MaxService,
			"username":     vaultkey.MaxUsername,
			"password":     vaultkey.MaxPassword,
			"url":          vaultkey.MaxURL,
			"notes":        vaultkey.MaxNotes,
			"query":        vaultkey.MaxQuery,
			"totp_uri":     vaultkey.MaxTOTPURI,
			"request_line": vaultkey.MaxLineLength,
			"page_limit":   vaultkey.MaxPageLimit,
			"page_offset":  vaultkey.MaxPageOffset,
		},
		"methods":  methodList,
		"features": []string{"dll", "json_errors", "capabilities"},
		"exit_codes": map[string]int{
			"ok": 0, "failure": 1, "usage": 2, "connection": 3,
			"locked": 4, "not_authorized": 5, "protocol": 6,
			"ambiguous_or_timeout": 7, "device_operation": 8, "interrupted": 130,
		},
	}
}

func dispatch(ctx context.Context, method string, params json.RawMessage) (any, *errorInfo) {
	switch method {
	case "capabilities":
		return capabilities(), nil

	case "open":
		var input struct {
			Port string `json:"port"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		// Windows COM ports are exclusive, so release the current handle
		// before opening the requested one.
		_ = closeActive()
		client, hello, port, err := openDevice(ctx, input.Port)
		if err != nil {
			return nil, classify(err)
		}
		active, activeHello, activePort = client, hello, port
		return map[string]any{"port": port, "device": hello}, nil

	case "reconnect":
		var input struct {
			Port string `json:"port"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		previous := activePort
		_ = closeActive()
		target := input.Port
		if target == "" {
			target = previous
		}
		client, hello, port, err := openDevice(ctx, target)
		if err != nil {
			return nil, classify(err)
		}
		active, activeHello, activePort = client, hello, port
		return map[string]any{"port": port, "device": hello}, nil

	case "close":
		if err := closeActive(); err != nil {
			return nil, classify(err)
		}
		return map[string]any{"closed": true}, nil

	case "hello":
		if active == nil {
			return nil, connectionError("not_open", "no device is open", "call open first.")
		}
		return map[string]any{"port": activePort, "device": activeHello}, nil

	case "status":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		status, err := client.Status(ctx)
		if err != nil {
			return nil, classify(err)
		}
		return status, nil

	case "time.get":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		clock, err := client.Time(ctx)
		if err != nil {
			return nil, classify(err)
		}
		return clock, nil

	case "time.set":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input struct {
			Unix    *int64 `json:"unix"`
			RFC3339 string `json:"rfc3339"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		var unix int64
		switch {
		case input.RFC3339 != "":
			parsed, err := time.Parse(time.RFC3339, input.RFC3339)
			if err != nil {
				return nil, usageError("invalid_time", "rfc3339 must look like 2026-11-17T12:00:00Z")
			}
			unix = parsed.Unix()
		case input.Unix != nil:
			unix = *input.Unix
		default:
			unix = time.Now().Unix()
		}
		clock, err := client.SetTime(ctx, unix)
		if err != nil {
			return nil, classify(err)
		}
		return clock, nil

	case "list":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input struct {
			Query  string `json:"query"`
			Offset uint32 `json:"offset"`
			Limit  uint32 `json:"limit"`
			All    bool   `json:"all"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		if input.All {
			result, err := listAll(ctx, client, input.Query)
			if err != nil {
				return nil, classify(err)
			}
			return result, nil
		}
		result, err := client.List(ctx, input.Query, input.Offset, input.Limit)
		if err != nil {
			return nil, classify(err)
		}
		return result, nil

	case "credentials.add":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input vaultkey.CredentialInput
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		id, err := client.AddCredential(ctx, input)
		if err != nil {
			return nil, classify(err)
		}
		return map[string]any{"entry_id": id}, nil

	case "credentials.update":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input struct {
			ID uint32 `json:"id"`
			vaultkey.CredentialInput
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		id, err := client.UpdateCredential(ctx, input.ID, input.CredentialInput)
		if err != nil {
			return nil, classify(err)
		}
		return map[string]any{"entry_id": id}, nil

	case "totp.add":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input struct {
			URI string `json:"uri"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		id, err := client.AddTOTP(ctx, input.URI)
		if err != nil {
			return nil, classify(err)
		}
		return map[string]any{"entry_id": id}, nil

	case "delete":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		var input struct {
			Type string `json:"type"`
			ID   uint32 `json:"id"`
		}
		if info := decodeParams(params, &input); info != nil {
			return nil, info
		}
		entryType := vaultkey.EntryType(input.Type)
		if err := client.Delete(ctx, entryType, input.ID); err != nil {
			return nil, classify(err)
		}
		return map[string]any{"deleted": true, "type": input.Type, "entry_id": input.ID}, nil

	case "lock":
		client, info := requireDevice()
		if info != nil {
			return nil, info
		}
		if err := client.Lock(ctx); err != nil {
			return nil, classify(err)
		}
		return map[string]any{"locked": true}, nil

	default:
		return nil, usageError("unknown_method", "unknown method: "+method)
	}
}

func encodeResponse(response callResponse) *C.char {
	data, err := json.Marshal(response)
	if err != nil {
		return C.CString(`{"ok":false,"error":{"kind":"internal","code":"internal_error","message":"could not encode response","exit_code":1}}`)
	}
	return C.CString(string(data))
}

//export VKCall
func VKCall(cRequest *C.char) (out *C.char) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = encodeResponse(fail(&errorInfo{
				Kind: "internal", Code: "internal_error",
				Message: "internal error", ExitCode: 1,
			}))
		}
	}()

	stateMu.Lock()
	defer stateMu.Unlock()

	if cRequest == nil {
		return encodeResponse(fail(usageError("invalid_json", "request is null")))
	}

	var request callRequest
	if err := json.Unmarshal([]byte(C.GoString(cRequest)), &request); err != nil {
		return encodeResponse(fail(usageError("invalid_json", "request is not valid JSON")))
	}
	if request.Method == "" {
		response := fail(usageError("missing_field", "method is required"))
		response.ID = request.ID
		return encodeResponse(response)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout(request.TimeoutMS))
	defer cancel()

	result, info := dispatch(ctx, request.Method, request.Params)
	if info != nil {
		response := fail(info)
		response.ID = request.ID
		return encodeResponse(response)
	}
	return encodeResponse(callResponse{ID: request.ID, OK: true, Result: result})
}

//export VKFree
func VKFree(pointer *C.char) {
	if pointer != nil {
		C.free(unsafe.Pointer(pointer))
	}
}

//export VKVersion
func VKVersion() *C.char {
	return C.CString(version + " (" + commit + ", " + buildDate + ")")
}

//export VKSessionProtocol
func VKSessionProtocol() C.int {
	return sessionProtocol
}

func main() {}
