//go:build linux

package transport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Candidates lists USB CDC ACM candidate ports. Linux sysfs metadata is used
// to skip devices that are clearly not Espressif USB devices, so unrelated
// serial hardware is not opened or reset.
func Candidates() ([]string, error) {
	found := make(map[string]struct{})
	for _, pattern := range []string{"/dev/ttyACM*", "/dev/ttyUSB*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || info.Mode()&os.ModeDevice == 0 {
				continue
			}
			if vendor, known := linuxUSBVendor(path); known && vendor != "303a" {
				continue
			}
			found[path] = struct{}{}
		}
	}

	ports := make([]string, 0, len(found))
	for path := range found {
		ports = append(ports, path)
	}
	sort.Strings(ports)
	return ports, nil
}

func linuxUSBVendor(device string) (string, bool) {
	current := filepath.Join("/sys/class/tty", filepath.Base(device), "device")
	for range 4 {
		data, err := os.ReadFile(filepath.Join(current, "idVendor"))
		if err == nil {
			return strings.ToLower(strings.TrimSpace(string(data))), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", false
}
