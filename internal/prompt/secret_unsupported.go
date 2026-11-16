//go:build !linux && !darwin && !windows

package prompt

// Secret is unavailable on this platform.
func Secret(label string) (string, error) {
	return "", ErrNoTerminal
}

// Confirm is unavailable on this platform.
func Confirm(question string) (bool, error) {
	return false, ErrNoTerminal
}
