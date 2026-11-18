package vaultkey

import (
	"context"
	"encoding/json"
	"fmt"
)

// EntryType is a delete target type.
type EntryType string

const (
	CredentialEntry EntryType = "credential"
	TOTPEntry       EntryType = "totp"
)

func decodeResponse[T any](raw json.RawMessage, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return zero, &ProtocolError{Message: "could not decode response"}
	}
	return value, nil
}

// Hello identifies the device and checks protocol compatibility.
func (c *Client) Hello(ctx context.Context) (Hello, error) {
	raw, err := c.Do(ctx, "HELLO", nil, false)
	return decodeResponse[Hello](raw, err)
}

// Status returns lock, RTC, storage, and USB state.
func (c *Client) Status(ctx context.Context) (Status, error) {
	raw, err := c.Do(ctx, "GET_STATUS", nil, false)
	return decodeResponse[Status](raw, err)
}

// Time returns the validated DS1307 UTC time.
func (c *Client) Time(ctx context.Context) (Clock, error) {
	raw, err := c.Do(ctx, "GET_TIME", nil, false)
	return decodeResponse[Clock](raw, err)
}

// SetTime writes a UTC Unix timestamp.
func (c *Client) SetTime(ctx context.Context, unix int64) (Clock, error) {
	if err := ValidateUnix(unix); err != nil {
		clock := Clock{}
		return clock, err
	}
	raw, err := c.Do(ctx, "SET_TIME", map[string]any{"unix": unix}, true)
	return decodeResponse[Clock](raw, err)
}

// List returns one page of non-secret service labels.
func (c *Client) List(ctx context.Context, query string, offset, limit uint32) (ServiceList, error) {
	if err := validateText("query", query, MaxQuery, false); err != nil {
		list := ServiceList{}
		return list, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit > MaxPageLimit {
		list := ServiceList{}
		return list, &RequestError{Message: fmt.Sprintf("limit must be 1-%d", MaxPageLimit)}
	}
	if offset > MaxPageOffset {
		list := ServiceList{}
		return list, &RequestError{Message: fmt.Sprintf("offset must be 0-%d", MaxPageOffset)}
	}
	fields := map[string]any{"offset": offset, "limit": limit}
	if query != "" {
		fields["query"] = query
	}
	raw, err := c.Do(ctx, "LIST_SERVICES", fields, false)
	return decodeResponse[ServiceList](raw, err)
}

// AddCredential creates a credential and returns its entry ID.
func (c *Client) AddCredential(ctx context.Context, input CredentialInput) (uint32, error) {
	if err := ValidateCredential(input); err != nil {
		return 0, err
	}
	fields := credentialFields(input)
	raw, err := c.Do(ctx, "ADD_CREDENTIAL", fields, true)
	result, err := decodeResponse[struct {
		EntryID uint32 `json:"entry_id"`
	}](raw, err)
	if err != nil {
		return 0, err
	}
	if result.EntryID == 0 {
		return 0, &ProtocolError{Message: "successful add response has no entry_id"}
	}
	return result.EntryID, nil
}

// UpdateCredential replaces every field of an existing credential.
func (c *Client) UpdateCredential(ctx context.Context, id uint32,
	input CredentialInput) (uint32, error) {
	if id == 0 {
		return 0, &RequestError{Message: "entry ID must be positive"}
	}
	if err := ValidateCredential(input); err != nil {
		return 0, err
	}
	fields := credentialFields(input)
	fields["entry_id"] = id
	raw, err := c.Do(ctx, "UPDATE_CREDENTIAL", fields, true)
	result, err := decodeResponse[struct {
		EntryID uint32 `json:"entry_id"`
	}](raw, err)
	if err != nil {
		return 0, err
	}
	if result.EntryID == 0 {
		return 0, &ProtocolError{Message: "successful update response has no entry_id"}
	}
	return result.EntryID, nil
}

func credentialFields(input CredentialInput) map[string]any {
	return map[string]any{
		"service":  input.Service,
		"username": input.Username,
		"password": input.Password,
		"url":      input.URL,
		"notes":    input.Notes,
	}
}

// AddTOTP imports an otpauth://totp/ URI and returns its entry ID.
func (c *Client) AddTOTP(ctx context.Context, uri string) (uint32, error) {
	if err := ValidateTOTPURI(uri); err != nil {
		return 0, err
	}
	raw, err := c.Do(ctx, "ADD_TOTP", map[string]any{"uri": uri}, true)
	result, err := decodeResponse[struct {
		EntryID uint32 `json:"entry_id"`
	}](raw, err)
	if err != nil {
		return 0, err
	}
	if result.EntryID == 0 {
		return 0, &ProtocolError{Message: "successful TOTP add response has no entry_id"}
	}
	return result.EntryID, nil
}

// Delete removes a credential or TOTP entry.
func (c *Client) Delete(ctx context.Context, entryType EntryType, id uint32) error {
	if id == 0 {
		return &RequestError{Message: "entry ID must be positive"}
	}
	if entryType != CredentialEntry && entryType != TOTPEntry {
		return &RequestError{Message: `type must be "credential" or "totp"`}
	}
	_, err := c.Do(ctx, "DELETE_ENTRY", map[string]any{
		"type":     string(entryType),
		"entry_id": id,
	}, true)
	return err
}

// Lock locks the vault, cancels HID output, and revokes provisioning.
func (c *Client) Lock(ctx context.Context) error {
	_, err := c.Do(ctx, "LOCK_DEVICE", nil, true)
	return err
}
