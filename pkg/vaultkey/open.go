package vaultkey

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
)

// Hooks kept as variables so tests can exercise discovery without hardware.
var (
	candidatePorts = transport.Candidates
	openPort       = transport.Open
)

// OpenOptions controls device discovery and opening.
type OpenOptions struct {
	Port         string        // empty = auto-detect
	ProbeTimeout time.Duration // default 2s
	Logger       func(string)

	// Optional app identity used by Doctor and the session ready event.
	Version   string
	Commit    string
	BuildDate string
}

// DeviceInfo is one row of discovery output.
type DeviceInfo struct {
	Port     string `json:"port"`
	State    string `json:"state"` // vaultkey | other-device | no-response | unreachable
	Product  string `json:"product,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Hardware string `json:"hardware,omitempty"`
	Protocol int    `json:"protocol,omitempty"`
	Error    string `json:"error,omitempty"`
}

// NotFoundError means no usable VaultKey was found.
type NotFoundError struct {
	Results []DeviceInfo
	Reason  error
}

func (e *NotFoundError) Error() string {
	if e.Reason != nil {
		return "could not find a VaultKey: " + e.Reason.Error()
	}
	if len(e.Results) == 0 {
		return "no candidate VaultKey serial ports were found"
	}
	var builder strings.Builder
	builder.WriteString("no VaultKey answered on the candidate ports:")
	for _, result := range e.Results {
		builder.WriteString("\n  ")
		builder.WriteString(result.Port)
		builder.WriteString(": ")
		switch {
		case result.Error != "":
			builder.WriteString(result.Error)
		case result.Product != "":
			builder.WriteString("device identifies as " + result.Product)
		default:
			builder.WriteString("no valid response")
		}
	}
	return builder.String()
}

func (e *NotFoundError) Unwrap() error { return e.Reason }

func (e *NotFoundError) Kind() string { return "connection" }
func (e *NotFoundError) Code() string { return "device_not_found" }
func (e *NotFoundError) Hint() string {
	return "connect the VaultKey, make sure its firmware is running, and choose the port if needed."
}
func (e *NotFoundError) ExitCode() int { return ExitConnection }

// MultipleDevicesError means more than one VaultKey answered.
type MultipleDevicesError struct {
	Ports []string
}

func (e *MultipleDevicesError) Error() string {
	return "multiple VaultKey devices found: " + strings.Join(e.Ports, ", ")
}

func (e *MultipleDevicesError) Kind() string { return "connection" }
func (e *MultipleDevicesError) Code() string { return "multiple_devices" }
func (e *MultipleDevicesError) Hint() string {
	return "select one port explicitly."
}
func (e *MultipleDevicesError) ExitCode() int { return ExitConnection }

// Session owns one open device connection. Closing it drops DTR and therefore
// revokes the firmware's CDC authorization.
type Session struct {
	client *Client
	hello  Hello
	app    AppInfo
}

// Open opens exactly one VaultKey. With an empty Port it probes candidates and
// requires exactly one VaultKey to answer.
func Open(ctx context.Context, options OpenOptions) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	port := strings.TrimSpace(options.Port)
	if port == "" {
		infos, err := Discover(ctx, options)
		if err != nil {
			return nil, err
		}
		matches := make([]string, 0, 1)
		for _, info := range infos {
			if info.State == "vaultkey" {
				matches = append(matches, info.Port)
			}
		}
		sort.Strings(matches)
		switch len(matches) {
		case 0:
			return nil, &NotFoundError{Results: infos}
		case 1:
			port = matches[0]
		default:
			return nil, &MultipleDevicesError{Ports: matches}
		}
	}

	client, hello, err := openClient(ctx, port, options)
	if err != nil {
		return nil, err
	}
	return &Session{
		client: client,
		hello:  hello,
		app: AppInfo{
			Version:   options.Version,
			Commit:    options.Commit,
			BuildDate: options.BuildDate,
			Platform:  platformName(),
		},
	}, nil
}

// Discover probes the selected port or every candidate. It never fails just
// because a port answers with a different product or no response.
func Discover(ctx context.Context, options OpenOptions) ([]DeviceInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ports := []string{}
	if port := strings.TrimSpace(options.Port); port != "" {
		ports = append(ports, port)
	} else {
		found, err := candidatePorts()
		if err != nil {
			return nil, &NotFoundError{Reason: err}
		}
		ports = append(ports, found...)
	}

	results := make([]DeviceInfo, 0, len(ports))
	for _, port := range ports {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		results = append(results, probePort(ctx, port, options))
	}
	return results, nil
}

func probePort(ctx context.Context, port string, options OpenOptions) DeviceInfo {
	info := DeviceInfo{Port: port}
	serial, err := openPort(port)
	if err != nil {
		info.State = "unreachable"
		info.Error = err.Error()
		return info
	}
	client := New(serial)
	defer client.Close()
	if options.Logger != nil {
		client.SetLogger(options.Logger)
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(options.ProbeTimeout))
	defer cancel()
	hello, err := client.Hello(probeCtx)
	if err != nil {
		info.State = "no-response"
		info.Error = err.Error()
		return info
	}
	info.Product = hello.Product
	info.Firmware = hello.Firmware
	info.Hardware = hello.Hardware
	info.Protocol = hello.Protocol
	if hello.Product == "VaultKey" {
		info.State = "vaultkey"
	} else {
		info.State = "other-device"
	}
	return info
}

func openClient(ctx context.Context, port string, options OpenOptions) (*Client, Hello, error) {
	serial, err := openPort(port)
	if err != nil {
		return nil, Hello{}, err
	}
	client := New(serial)
	if options.Logger != nil {
		client.SetLogger(options.Logger)
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(options.ProbeTimeout))
	defer cancel()
	hello, err := client.Hello(probeCtx)
	if err != nil {
		client.Close()
		return nil, Hello{}, err
	}
	if hello.Product != "VaultKey" {
		client.Close()
		return nil, hello, &NotFoundError{Results: []DeviceInfo{{
			Port:     port,
			State:    "other-device",
			Product:  hello.Product,
			Firmware: hello.Firmware,
			Hardware: hello.Hardware,
			Protocol: hello.Protocol,
		}}}
	}
	if hello.Protocol != ProtocolVersion {
		client.Close()
		return nil, hello, &ProtocolError{Message: fmt.Sprintf(
			"device protocol is %d; this app supports %d", hello.Protocol, ProtocolVersion)}
	}
	return client, hello, nil
}

func probeTimeout(requested time.Duration) time.Duration {
	timeout := 2 * time.Second
	if requested > 0 {
		timeout = requested
	}
	return timeout
}

// Close closes the device connection.
func (s *Session) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// PortName returns the serial port path.
func (s *Session) PortName() string { return s.client.PortName() }

// Hello returns the cached device identity.
func (s *Session) Hello() Hello { return s.hello }

// Client exposes the lower-level serialized client for advanced use.
func (s *Session) Client() *Client { return s.client }

// Status returns lock, RTC, storage, and USB state.
func (s *Session) Status(ctx context.Context) (Status, error) { return s.client.Status(ctx) }

// Time returns the validated DS1307 UTC time.
func (s *Session) Time(ctx context.Context) (Clock, error) { return s.client.Time(ctx) }

// SetTime writes a UTC Unix timestamp.
func (s *Session) SetTime(ctx context.Context, unix int64) (Clock, error) {
	return s.client.SetTime(ctx, unix)
}

// List returns one page of non-secret labels.
func (s *Session) List(ctx context.Context, query string, offset, limit uint32) (ServiceList, error) {
	return s.client.List(ctx, query, offset, limit)
}

// ListAll follows pagination and deduplicates entries by ID. The aggregated
// result reports Offset 0 and Limit as the total number of returned entries;
// callers that need page information should use List.
func (s *Session) ListAll(ctx context.Context, query string, pageTimeout time.Duration) (ServiceList, error) {
	aggregate := ServiceList{
		Credentials: []CredentialSummary{},
		TOTP:        []TOTPSummary{},
	}
	if pageTimeout <= 0 {
		pageTimeout = 5 * time.Second
	}
	seenCredentials := make(map[uint32]struct{})
	seenTOTP := make(map[uint32]struct{})
	offset := uint32(0)
	const limit = MaxPageLimit

	for page := 0; page < 64; page++ {
		pageCtx, cancel := context.WithTimeout(ctx, pageTimeout)
		result, err := s.client.List(pageCtx, query, offset, limit)
		cancel()
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
		if len(result.Credentials) == 0 && len(result.TOTP) == 0 {
			break
		}
		offset += limit
		if offset > MaxPageOffset {
			return aggregate, &RequestError{Message: "pagination exceeded the device limit"}
		}
	}
	aggregate.Offset = 0
	aggregate.Limit = uint32(len(aggregate.Credentials) + len(aggregate.TOTP))
	return aggregate, nil
}

// AddCredential creates a credential.
func (s *Session) AddCredential(ctx context.Context, input CredentialInput) (uint32, error) {
	return s.client.AddCredential(ctx, input)
}

// UpdateCredential replaces every field of a credential.
func (s *Session) UpdateCredential(ctx context.Context, id uint32,
	input CredentialInput) (uint32, error) {
	return s.client.UpdateCredential(ctx, id, input)
}

// AddTOTP imports an otpauth URI.
func (s *Session) AddTOTP(ctx context.Context, uri string) (uint32, error) {
	return s.client.AddTOTP(ctx, uri)
}

// Delete removes a credential or TOTP entry.
func (s *Session) Delete(ctx context.Context, entryType EntryType, id uint32) error {
	return s.client.Delete(ctx, entryType, id)
}

// Lock locks the vault and revokes CDC authorization.
func (s *Session) Lock(ctx context.Context) error { return s.client.Lock(ctx) }
