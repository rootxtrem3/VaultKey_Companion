package vaultkey

import (
	"context"
	"fmt"
	"runtime"
)

// AppInfo identifies the running companion app.
type AppInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Platform  string `json:"platform"`
}

// Check is one read-only doctor result.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok | warn | fail
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// DoctorReport is the machine-readable output of Session.Doctor.
type DoctorReport struct {
	OK       bool    `json:"ok"`
	App      AppInfo `json:"app"`
	Platform string  `json:"platform"`
	Port     string  `json:"port,omitempty"`
	Device   *Hello  `json:"device,omitempty"`
	Checks   []Check `json:"checks"`
}

func platformName() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// Doctor runs read-only checks. It never mutates the device. A failed check is
// reported in the report instead of aborting, so a UI can render the result.
func (s *Session) Doctor(ctx context.Context) (DoctorReport, error) {
	report := DoctorReport{
		OK:       true,
		App:      s.app,
		Platform: platformName(),
		Port:     s.PortName(),
		Device:   &s.hello,
		Checks:   []Check{},
	}
	report.App.Platform = platformName()
	add := func(name, status, message, detail string) {
		report.Checks = append(report.Checks, Check{
			Name: name, Status: status, Message: message, Detail: detail,
		})
		if status == "fail" {
			report.OK = false
		}
	}

	add("connection", "ok", "device connection is open", s.PortName())
	add("identity", "ok", fmt.Sprintf("%s %s, firmware %s, protocol %d",
		s.hello.Product, s.hello.Hardware, s.hello.Firmware, s.hello.Protocol), "")

	status, err := s.statusForDoctor(ctx)
	if err != nil {
		add("status", "fail", "GET_STATUS failed", err.Error())
		add("platform", "ok", "closing the port revokes CDC authorization",
			"keep one session open while the UI is running")
		return report, nil
	}
	add("status", "ok", "GET_STATUS answered", "")

	if status.Locked {
		add("unlocked", "warn", "vault is locked",
			"unlock it with the PIN on the device to manage entries")
	} else {
		add("unlocked", "ok", "vault is unlocked", "")
	}
	if status.Provisioning {
		add("authorization", "ok", "CDC provisioning is authorized", "")
	} else {
		add("authorization", "warn", "CDC provisioning is not authorized",
			`on the device open USB Functions and choose "Authorize CDC 5 min"`)
	}
	if status.RTC == "ready" {
		add("rtc", "ok", "RTC status is ready", "")
	} else {
		add("rtc", "warn", "RTC status: "+status.RTC,
			"set the clock if the device reports an invalid time")
	}
	if status.Storage == "ready" {
		add("storage", "ok", "storage is ready", "")
	} else {
		add("storage", "warn", "storage status: "+status.Storage, "")
	}
	if status.USBMounted && status.CDC {
		add("usb", "ok", "USB mounted and CDC active", "")
	} else {
		add("usb", "warn", fmt.Sprintf("USB mounted=%t cdc=%t hid=%t",
			status.USBMounted, status.CDC, status.HID), "")
	}

	clock, timeErr := s.Time(ctx)
	switch {
	case timeErr == nil:
		add("clock", "ok", fmt.Sprintf("RTC UTC unix %d", clock.Unix), "")
	case IsCode(timeErr, "rtc_time_invalid"):
		add("clock", "warn", "RTC time is not set or not valid", "")
	default:
		add("clock", "warn", "GET_TIME failed", timeErr.Error())
	}

	add("platform", "ok", "closing the port revokes CDC authorization",
		"keep one session open while the UI is running")
	return report, nil
}

func (s *Session) statusForDoctor(ctx context.Context) (Status, error) {
	return s.client.Status(ctx)
}
