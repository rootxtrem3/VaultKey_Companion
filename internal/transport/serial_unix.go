//go:build linux || darwin

package transport

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

type unixPort struct {
	file     *os.File
	fd       int
	name     string
	original syscall.Termios
	once     sync.Once
	closeErr error
}

// Open opens a serial port and puts it into raw mode. USB CDC ignores the
// configured line speed, so the existing speed is left untouched.
func Open(name string) (Port, error) {
	fd, err := syscall.Open(name,
		syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}

	var original syscall.Termios
	if err := ioctlTermios(fd, ioctlReadTermios, &original); err != nil {
		_ = syscall.Close(fd)
		return nil, notSerial(name, err)
	}

	raw := original
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK |
		syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL |
		syscall.IXON | syscall.IXOFF | syscall.IXANY
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON |
		syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.CSTOPB
	raw.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := ioctlTermios(fd, ioctlWriteTermios, &raw); err != nil {
		_ = syscall.Close(fd)
		return nil, notSerial(name, err)
	}

	return &unixPort{
		file:     os.NewFile(uintptr(fd), name),
		fd:       fd,
		name:     name,
		original: original,
	}, nil
}

func ioctlTermios(fd int, request uintptr, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), request,
		uintptr(unsafe.Pointer(termios)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func (p *unixPort) Name() string { return p.name }

func (p *unixPort) Read(buffer []byte) (int, error) {
	return p.file.Read(buffer)
}

func (p *unixPort) Write(buffer []byte) (int, error) {
	return p.file.Write(buffer)
}

func (p *unixPort) Close() error {
	p.once.Do(func() {
		// Best effort: restoring the previous settings keeps a vanished
		// device from leaving an unconfigured tty behind.
		_ = ioctlTermios(p.fd, ioctlWriteTermios, &p.original)
		p.closeErr = p.file.Close()
	})
	return p.closeErr
}
