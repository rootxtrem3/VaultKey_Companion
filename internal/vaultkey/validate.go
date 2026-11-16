package vaultkey

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func validateText(name, value string, maximum int, required bool) error {
	if !utf8.ValidString(value) {
		return &RequestError{Message: name + " is not valid UTF-8"}
	}
	if required && value == "" {
		return &RequestError{Message: name + " is required"}
	}
	if len(value) > maximum {
		return &RequestError{Message: fmt.Sprintf("%s exceeds %d bytes", name, maximum)}
	}
	return nil
}

// ValidateCredentialFields checks the non-secret fields. It lets the CLI
// reject bad metadata before prompting for a password.
func ValidateCredentialFields(input CredentialInput) error {
	if err := validateText("service", input.Service, MaxService, true); err != nil {
		return err
	}
	if err := validateText("username", input.Username, MaxUsername, false); err != nil {
		return err
	}
	if err := validateText("URL", input.URL, MaxURL, false); err != nil {
		return err
	}
	return validateText("notes", input.Notes, MaxNotes, false)
}

// ValidateCredential checks a complete credential replacement before it is
// sent. Error messages never include field values.
func ValidateCredential(input CredentialInput) error {
	if err := ValidateCredentialFields(input); err != nil {
		return err
	}
	return validateText("password", input.Password, MaxPassword, true)
}

// ValidateTOTPURI performs the cheap structural checks that avoid a wasted
// round trip. The device remains the authority on Base32 and parameters.
func ValidateTOTPURI(uri string) error {
	if !utf8.ValidString(uri) {
		return &RequestError{Message: "TOTP URI is not valid UTF-8"}
	}
	if len(uri) > MaxTOTPURI {
		return &RequestError{Message: fmt.Sprintf("TOTP URI exceeds %d bytes", MaxTOTPURI)}
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		return &RequestError{Message: `TOTP URI must start with "otpauth://totp/"`}
	}
	if len(uri) == len("otpauth://totp/") {
		return &RequestError{Message: "TOTP URI has no label"}
	}
	return nil
}

// ValidateUnix checks the DS1307 calendar range accepted by the firmware.
func ValidateUnix(unix int64) error {
	if unix <= 0 {
		return &RequestError{Message: "time must be a positive Unix timestamp"}
	}
	year := time.Unix(unix, 0).UTC().Year()
	if year < 2000 || year > 2099 {
		return &RequestError{Message: fmt.Sprintf("year %d is outside the DS1307 range 2000-2099", year)}
	}
	return nil
}

// HIDTypeable reports whether every byte can be typed by the firmware's HID
// keyboard path. Stored UTF-8 credentials outside this set cannot be typed.
func HIDTypeable(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 0x20 && character <= 0x7e) ||
			character == '\t' || character == '\n' || character == '\b' {
			continue
		}
		return false
	}
	return true
}
