package vaultkey

// Stable process exit codes. These are part of the machine interface and must
// not change.
const (
	ExitOK                 = 0
	ExitFailure            = 1
	ExitUsage              = 2
	ExitConnection         = 3
	ExitLocked             = 4
	ExitNotAuthorized      = 5
	ExitProtocol           = 6
	ExitAmbiguousOrTimeout = 7
	ExitDeviceOperation    = 8
	ExitInterrupted        = 130
)

// ErrorInfo is the stable, secret-free error object exposed to the UI.
type ErrorInfo struct {
	Kind       string `json:"kind"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Ambiguous  bool   `json:"ambiguous,omitempty"`
	DeviceCode string `json:"device_code,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Classified is implemented by errors that carry their own machine-readable
// classification.
type Classified interface {
	error
	Kind() string
	Code() string
	Hint() string
	ExitCode() int
}
