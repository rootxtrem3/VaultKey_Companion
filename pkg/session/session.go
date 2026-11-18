// Package session implements the long-lived JSON Lines session protocol used
// by the desktop UI. It owns one device connection and never retries mutating
// commands.
package session

import (
	"context"
	"errors"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// Device is the narrow device surface the session engine needs.
type Device interface {
	PortName() string
	Hello() vaultkey.Hello
	Close() error
	Status(ctx context.Context) (vaultkey.Status, error)
	Time(ctx context.Context) (vaultkey.Clock, error)
	SetTime(ctx context.Context, unix int64) (vaultkey.Clock, error)
	List(ctx context.Context, query string, offset, limit uint32) (vaultkey.ServiceList, error)
	ListAll(ctx context.Context, query string, pageTimeout time.Duration) (vaultkey.ServiceList, error)
	AddCredential(ctx context.Context, input vaultkey.CredentialInput) (uint32, error)
	UpdateCredential(ctx context.Context, id uint32, input vaultkey.CredentialInput) (uint32, error)
	AddTOTP(ctx context.Context, uri string) (uint32, error)
	Delete(ctx context.Context, entryType vaultkey.EntryType, id uint32) error
	Lock(ctx context.Context) error
	Doctor(ctx context.Context) (vaultkey.DoctorReport, error)
}

// Connector opens the initial device.
type Connector func(ctx context.Context) (Device, error)

// Options configures a session engine.
type Options struct {
	Connector Connector
	// Reconnect is optional. It receives an explicit port (possibly empty)
	// when the UI asks to reopen the device.
	Reconnect func(ctx context.Context, port string) (Device, error)

	Version   string
	Commit    string
	BuildDate string

	DefaultTimeout time.Duration
	Debug          bool
	Logger         func(string)
}

func (o Options) timeout() time.Duration {
	if o.DefaultTimeout <= 0 {
		return 5 * time.Second
	}
	return o.DefaultTimeout
}

// LocalError is a session-level error with a stable machine classification.
type LocalError struct {
	ErrorKind string
	CodeName  string
	Message   string
	HintText  string
	Exit      int
}

func (e *LocalError) Error() string { return e.Message }
func (e *LocalError) Kind() string {
	if e.ErrorKind != "" {
		return e.ErrorKind
	}
	return "usage"
}
func (e *LocalError) Code() string { return e.CodeName }
func (e *LocalError) Hint() string { return e.HintText }
func (e *LocalError) ExitCode() int {
	if e.Exit == 0 {
		return vaultkey.ExitUsage
	}
	return e.Exit
}

func invalidParams(message string) error {
	return &LocalError{CodeName: "invalid_params", Message: message}
}

func noDeviceError() error {
	return &LocalError{
		ErrorKind: "connection",
		CodeName:  "connection_closed",
		Message:   "the device connection is closed",
		HintText:  "reconnect the device and try again.",
		Exit:      vaultkey.ExitConnection,
	}
}

var errNoEmitter = errors.New("session: no output emitter")
