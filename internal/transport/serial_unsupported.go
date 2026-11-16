//go:build !linux && !darwin && !windows

package transport

// Open is unavailable on this platform.
func Open(name string) (Port, error) {
	return nil, ErrUnsupported
}
