//go:build !linux

// Package usbdev reads the platform description of a serial port's USB
// parent. Only the Linux sysfs implementation is provided for now.
package usbdev

// Info is the non-secret USB description of a serial port.
type Info struct {
	Found        bool
	Port         string
	VID          string
	PID          string
	Serial       string
	Manufacturer string
	Product      string
	Interface    string
	Class        string
	SubClass     string
	Protocol     string
	BusPath      string
}

// Describe returns an empty description on platforms without a sysfs reader.
func Describe(port string) Info { return Info{Port: port} }
