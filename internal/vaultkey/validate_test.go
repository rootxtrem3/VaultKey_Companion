package vaultkey

import (
	"strings"
	"testing"
	"time"
)

func TestValidateCredentialLimits(t *testing.T) {
	base := CredentialInput{Service: "Example", Password: "secret"}
	if err := ValidateCredential(base); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}

	cases := []struct {
		name  string
		input CredentialInput
	}{
		{"missing service", CredentialInput{Password: "secret"}},
		{"missing password", CredentialInput{Service: "Example"}},
		{"service too long", CredentialInput{Service: strings.Repeat("s", MaxService+1), Password: "secret"}},
		{"username too long", CredentialInput{Service: "Example", Username: strings.Repeat("u", MaxUsername+1), Password: "secret"}},
		{"password too long", CredentialInput{Service: "Example", Password: strings.Repeat("p", MaxPassword+1)}},
		{"url too long", CredentialInput{Service: "Example", URL: strings.Repeat("u", MaxURL+1), Password: "secret"}},
		{"notes too long", CredentialInput{Service: "Example", Notes: strings.Repeat("n", MaxNotes+1), Password: "secret"}},
		{"invalid UTF-8", CredentialInput{Service: string([]byte{0xff}), Password: "secret"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var request *RequestError
			err := ValidateCredential(test.input)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !asRequest(err, &request) {
				t.Fatalf("want RequestError, got %T %v", err, err)
			}
		})
	}
}

func asRequest(err error, target **RequestError) bool {
	request, ok := err.(*RequestError)
	if ok {
		*target = request
	}
	return ok
}

func TestValidateTOTPURI(t *testing.T) {
	valid := "otpauth://totp/Issuer:account?secret=JBSWY3DPEHPK3PXP"
	if err := ValidateTOTPURI(valid); err != nil {
		t.Fatalf("valid URI rejected: %v", err)
	}
	for _, value := range []string{
		"",
		"https://example.test",
		"otpauth://hotp/Issuer:account?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/",
		"otpauth://totp/" + strings.Repeat("a", MaxTOTPURI),
	} {
		if err := ValidateTOTPURI(value); err == nil {
			t.Errorf("accepted invalid URI (length %d)", len(value))
		}
	}
}

func TestValidateUnixRange(t *testing.T) {
	if err := ValidateUnix(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC).Unix()); err != nil {
		t.Fatalf("valid time rejected: %v", err)
	}
	if err := ValidateUnix(0); err == nil {
		t.Fatal("accepted zero time")
	}
	if err := ValidateUnix(time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC).Unix()); err == nil {
		t.Fatal("accepted pre-2000 time")
	}
	if err := ValidateUnix(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix()); err == nil {
		t.Fatal("accepted post-2099 time")
	}
}

func TestHIDTypeable(t *testing.T) {
	if !HIDTypeable("user@example.test\tpassword\n") {
		t.Fatal("rejected supported ASCII")
	}
	for _, value := range []string{"café", "пароль", "\x01"} {
		if HIDTypeable(value) {
			t.Errorf("accepted unsupported HID value %q", value)
		}
	}
}
