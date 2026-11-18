package vaultkey

// ProtocolVersion is the only CDC protocol version understood by this app.
const ProtocolVersion = 1

// Hello is the identity returned by HELLO and GET_VERSION.
type Hello struct {
	Product  string `json:"product"`
	Firmware string `json:"firmware"`
	Hardware string `json:"hardware"`
	Protocol int    `json:"protocol"`
}

// Status is the subset of GET_STATUS exposed by firmware protocol v1.
// Credentials and TOTP counts are only present while CDC provisioning is
// authorized.
type Status struct {
	Locked           bool    `json:"locked"`
	RTC              string  `json:"rtc"`
	Storage          string  `json:"storage"`
	Startup          string  `json:"startup"`
	StartupError     string  `json:"startup_error,omitempty"`
	Display          string  `json:"display"`
	ButtonEventDrops uint64  `json:"button_event_drops"`
	USBDriver        bool    `json:"usb_driver"`
	USBMounted       bool    `json:"usb_mounted"`
	CDC              bool    `json:"cdc"`
	HID              bool    `json:"hid"`
	Provisioning     bool    `json:"provisioning"`
	Credentials      *uint64 `json:"credentials,omitempty"`
	TOTP             *uint64 `json:"totp,omitempty"`
}

// Clock is the validated DS1307 UTC time.
type Clock struct {
	Unix int64 `json:"unix"`
	UTC  bool  `json:"utc"`
}

// CredentialSummary is a non-secret credential listing entry.
type CredentialSummary struct {
	ID      uint32 `json:"id"`
	Service string `json:"service"`
}

// TOTPSummary is a non-secret TOTP listing entry.
type TOTPSummary struct {
	ID      uint32 `json:"id"`
	Issuer  string `json:"issuer"`
	Account string `json:"account"`
}

// ServiceList is one page of LIST_SERVICES output.
type ServiceList struct {
	Offset          uint32              `json:"offset"`
	Limit           uint32              `json:"limit"`
	CredentialTotal uint32              `json:"credential_total"`
	TOTPTotal       uint32              `json:"totp_total"`
	Credentials     []CredentialSummary `json:"credentials"`
	TOTP            []TOTPSummary       `json:"totp"`
}

// CredentialInput is the complete field set for add/update.
type CredentialInput struct {
	Service  string `json:"service"`
	Username string `json:"username,omitempty"`
	Password string `json:"password"`
	URL      string `json:"url,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// Device limits for firmware protocol v1. These mirror
// include/core/HardwareConfig.h in the firmware repository.
const (
	MaxService    = 64
	MaxUsername   = 128
	MaxPassword   = 256
	MaxURL        = 192
	MaxNotes      = 512
	MaxQuery      = 64
	MaxTOTPURI    = 1024
	MaxLineLength = 2048 // firmware accepts 2048 bytes before the LF
	MaxPageLimit  = 25
	MaxPageOffset = 256

	MaxCredentials = 128
	MaxTOTP        = 128
)

// Limits is the machine-readable description of the firmware limits.
type Limits struct {
	Service     int `json:"service"`
	Username    int `json:"username"`
	Password    int `json:"password"`
	URL         int `json:"url"`
	Notes       int `json:"notes"`
	Query       int `json:"query"`
	TOTPURI     int `json:"totp_uri"`
	LineLength  int `json:"request_line"`
	PageLimit   int `json:"page_limit"`
	PageOffset  int `json:"page_offset"`
	Credentials int `json:"credentials"`
	TOTP        int `json:"totp"`
}

// CurrentLimits returns the limits understood by this build.
func CurrentLimits() Limits {
	return Limits{
		Service:     MaxService,
		Username:    MaxUsername,
		Password:    MaxPassword,
		URL:         MaxURL,
		Notes:       MaxNotes,
		Query:       MaxQuery,
		TOTPURI:     MaxTOTPURI,
		LineLength:  MaxLineLength,
		PageLimit:   MaxPageLimit,
		PageOffset:  MaxPageOffset,
		Credentials: MaxCredentials,
		TOTP:        MaxTOTP,
	}
}
