// Package config stores non-secret desktop preferences. It never stores
// credentials, passwords, TOTP secrets, or device data.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings is the persisted desktop preference set.
type Settings struct {
	LockTimeoutSeconds          int  `json:"lock_timeout_seconds"`
	SynchronizeClock            bool `json:"synchronize_clock"`
	RequirePhysicalConfirmation bool `json:"require_physical_confirmation"`
	ClearSessionOnDisconnect    bool `json:"clear_session_on_disconnect"`
	StartMinimized              bool `json:"start_minimized"`
	CloseToTray                 bool `json:"close_to_tray"`
}

// Defaults returns the settings used when no file exists.
func Defaults() Settings {
	return Settings{
		LockTimeoutSeconds:          300,
		SynchronizeClock:            false,
		RequirePhysicalConfirmation: true,
		ClearSessionOnDisconnect:    true,
		StartMinimized:              false,
		CloseToTray:                 false,
	}
}

// Path returns the settings file location.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vaultkey", "companion.json"), nil
}

// Load reads settings. A missing file returns defaults without an error.
func Load() (Settings, error) {
	settings := Defaults()
	path, err := Path()
	if err != nil {
		return settings, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return Defaults(), fmt.Errorf("settings file is not valid JSON: %w", err)
	}
	settings.normalize()
	return settings, nil
}

// Save writes settings atomically with owner-only permissions.
func Save(settings Settings) error {
	settings.normalize()
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(filepath.Dir(path), ".companion-*.json")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func (s *Settings) normalize() {
	if s.LockTimeoutSeconds < 30 {
		s.LockTimeoutSeconds = 30
	}
	if s.LockTimeoutSeconds > 3600 {
		s.LockTimeoutSeconds = 3600
	}
}
