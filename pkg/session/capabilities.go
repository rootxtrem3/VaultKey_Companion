package session

import (
	"runtime"

	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// SessionProtocol is the only session protocol version this build speaks.
const SessionProtocol = 1

// Capabilities is the no-device negotiation response.
type Capabilities struct {
	SessionProtocol int              `json:"session_protocol"`
	App             vaultkey.AppInfo `json:"app"`
	Protocol        int              `json:"protocol"`
	Limits          vaultkey.Limits  `json:"limits"`
	Methods         []string         `json:"methods"`
	Features        []string         `json:"features"`
	ExitCodes       map[string]int   `json:"exit_codes"`
}

// Methods is the ordered list of supported session methods.
var Methods = []string{
	"capabilities",
	"hello",
	"status",
	"time.get",
	"time.set",
	"list",
	"credentials.add",
	"credentials.update",
	"totp.add",
	"delete",
	"lock",
	"watch.start",
	"watch.stop",
	"reconnect",
	"shutdown",
	"doctor",
}

// Features describes optional behavior a UI may rely on.
var Features = []string{
	"session",
	"watch",
	"json_errors",
	"capabilities",
	"doctor",
}

// CapabilitiesFor builds the negotiation object from options without touching
// a device.
func CapabilitiesFor(options Options) Capabilities {
	return Capabilities{
		SessionProtocol: SessionProtocol,
		App: vaultkey.AppInfo{
			Version:   options.Version,
			Commit:    options.Commit,
			BuildDate: options.BuildDate,
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		},
		Protocol: vaultkey.ProtocolVersion,
		Limits:   vaultkey.CurrentLimits(),
		Methods:  append([]string(nil), Methods...),
		Features: append([]string(nil), Features...),
		ExitCodes: map[string]int{
			"ok":                   vaultkey.ExitOK,
			"failure":              vaultkey.ExitFailure,
			"usage":                vaultkey.ExitUsage,
			"connection":           vaultkey.ExitConnection,
			"locked":               vaultkey.ExitLocked,
			"not_authorized":       vaultkey.ExitNotAuthorized,
			"protocol":             vaultkey.ExitProtocol,
			"ambiguous_or_timeout": vaultkey.ExitAmbiguousOrTimeout,
			"device_operation":     vaultkey.ExitDeviceOperation,
			"interrupted":          vaultkey.ExitInterrupted,
		},
	}
}
