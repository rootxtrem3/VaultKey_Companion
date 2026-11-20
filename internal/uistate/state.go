package uistate

import (
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/usbdev"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// State is the explicit device session state. It is deliberately not a
// boolean: USB presence, an open CDC port, and vault authorization are
// distinct facts.
type State int

const (
	Disconnected State = iota
	USBDetected
	Connecting
	Connected
	Authenticated
	Locked
	Error
)

// String returns the user-facing state name.
func (s State) String() string {
	switch s {
	case USBDetected:
		return "USB detected"
	case Connecting:
		return "Connecting"
	case Connected:
		return "Connected"
	case Authenticated:
		return "Authorized"
	case Locked:
		return "Locked"
	case Error:
		return "Error"
	default:
		return "Disconnected"
	}
}

// DeviceOpen reports whether a CDC port is open.
func (s State) DeviceOpen() bool {
	switch s {
	case Connected, Authenticated, Locked:
		return true
	default:
		return false
	}
}

// Snapshot is an immutable view of the session state for the GTK thread.
type Snapshot struct {
	State  State
	Port   string
	Ports  []string
	USB    usbdev.Info
	Hello  vaultkey.Hello
	Status vaultkey.Status

	HasHello  bool
	HasStatus bool
	StatusAt  time.Time

	Clock    vaultkey.Clock
	HasClock bool
	ClockErr string

	Credentials     []vaultkey.CredentialSummary
	TOTP            []vaultkey.TOTPSummary
	CredentialTotal uint32
	TOTPTotal       uint32
	ListRevision    uint64
	Listing         bool

	Error *vaultkey.ErrorInfo
}

// clone copies slices so a snapshot can cross goroutines safely.
func (s Snapshot) clone() Snapshot {
	s.Ports = append([]string(nil), s.Ports...)
	s.Credentials = append([]vaultkey.CredentialSummary(nil), s.Credentials...)
	s.TOTP = append([]vaultkey.TOTPSummary(nil), s.TOTP...)
	if s.Error != nil {
		errCopy := *s.Error
		s.Error = &errCopy
	}
	return s
}
