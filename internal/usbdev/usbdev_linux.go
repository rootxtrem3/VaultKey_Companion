//go:build linux

// Package usbdev reads the Linux sysfs descriptions of a serial port's USB
// parent. It never opens the port, so discovery does not disturb the device or
// revoke CDC authorization.
package usbdev

import (
	"os"
	"path/filepath"
	"strings"
)

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

// Describe reads sysfs metadata for a tty device such as /dev/ttyACM0.
func Describe(port string) Info {
	info := Info{Port: port}
	if !strings.HasPrefix(port, "/dev/") {
		return info
	}

	interfaceDir, err := filepath.EvalSymlinks(
		filepath.Join("/sys/class/tty", filepath.Base(port), "device"))
	if err != nil {
		return info
	}

	// The tty device is normally a child of a USB interface directory
	// (for example 1-1:1.0). Walk up a few levels and stop at the first
	// directory that carries a USB device descriptor.
	deviceDir := ""
	current := interfaceDir
	for range 4 {
		if fileExists(filepath.Join(current, "idVendor")) {
			deviceDir = current
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	if deviceDir == "" {
		return info
	}
	info.Found = true
	info.VID = readFile(deviceDir, "idVendor")
	info.PID = readFile(deviceDir, "idProduct")
	info.Serial = readFile(deviceDir, "serial")
	info.Manufacturer = readFile(deviceDir, "manufacturer")
	info.Product = readFile(deviceDir, "product")
	info.BusPath = readFile(deviceDir, "devpath")

	// Interface class files live on the interface directory when present.
	info.Class = readFile(interfaceDir, "bInterfaceClass")
	info.SubClass = readFile(interfaceDir, "bInterfaceSubClass")
	info.Protocol = readFile(interfaceDir, "bInterfaceProtocol")
	info.Interface = interfaceLabel(info.Class, info.SubClass, info.Protocol)
	return info
}

func readFile(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func interfaceLabel(class, subclass, protocol string) string {
	if class == "" {
		return ""
	}
	switch strings.ToLower(class) {
	case "02":
		return "Communications (CDC control)"
	case "0a":
		return "CDC data"
	case "03":
		return "Human interface device"
	case "08":
		return "Mass storage"
	}
	parts := []string{"class " + class}
	if subclass != "" {
		parts = append(parts, "subclass "+subclass)
	}
	if protocol != "" {
		parts = append(parts, "protocol "+protocol)
	}
	return strings.Join(parts, ", ")
}
