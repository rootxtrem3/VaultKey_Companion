//go:build darwin

package transport

import (
	"path/filepath"
	"sort"
)

// Candidates lists macOS USB call-out serial ports.
func Candidates() ([]string, error) {
	found := make(map[string]struct{})
	for _, pattern := range []string{
		"/dev/cu.usbmodem*",
		"/dev/cu.usbserial*",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
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
