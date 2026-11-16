//go:build !linux && !darwin && !windows

package transport

// Candidates is unavailable on this platform.
func Candidates() ([]string, error) {
	return nil, ErrUnsupported
}
