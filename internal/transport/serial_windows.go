//go:build windows

package transport

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	genericRead  = 0x80000000
	genericWrite = 0x40000000
	openExisting = 3

	dtrControlEnable = 1 << 4
	rtsControlEnable = 1 << 12

	maxDWord = 0xFFFFFFFF

	errorInvalidHandle syscall.Errno = 6
)

// dcb mirrors the Windows DCB structure. Its layout is 28 bytes on supported
// architectures.
type dcb struct {
	DCBlength uint32
	BaudRate  uint32
	Flags     uint32
	Reserved  uint16
	XonLim    uint16
	XoffLim   uint16
	ByteSize  byte
	Parity    byte
	StopBits  byte
	XonChar   byte
	XoffChar  byte
	ErrorChar byte
	EofChar   byte
	EvtChar   byte
	Reserved1 uint16
}

type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetCommState    = kernel32.NewProc("GetCommState")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
	procSetupComm       = kernel32.NewProc("SetupComm")
	procCancelIoEx      = kernel32.NewProc("CancelIoEx")
)

type windowsPort struct {
	handle   syscall.Handle
	name     string
	once     sync.Once
	closeErr error
}

// Open opens a Windows COM port in binary raw mode.
func Open(name string) (Port, error) {
	path := name
	upper := strings.ToUpper(name)
	if !strings.HasPrefix(path, `\\.\`) && strings.HasPrefix(upper, "COM") {
		path = `\\.\` + name
	}
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	handle, err := syscall.CreateFile(pointer, genericRead|genericWrite, 0, nil,
		openExisting, 0, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}

	port := &windowsPort{handle: handle, name: name}
	if err := port.configure(); err != nil {
		_ = syscall.CloseHandle(handle)
		return nil, notSerial(name, err)
	}
	return port, nil
}

func (p *windowsPort) configure() error {
	var state dcb
	state.DCBlength = uint32(unsafe.Sizeof(state))
	if result, _, callErr := procGetCommState.Call(uintptr(p.handle),
		uintptr(unsafe.Pointer(&state))); result == 0 {
		return fmt.Errorf("GetCommState: %w", callErr)
	}

	state.BaudRate = 115200
	state.Flags &^= (1 << 1) | (1 << 2) | (1 << 3) | (0x3 << 4) |
		(1 << 6) | (1 << 7) | (1 << 8) | (1 << 9) | (1 << 10) | (1 << 11) |
		(0x3 << 12) | (1 << 14)
	state.Flags |= 1 | dtrControlEnable | rtsControlEnable
	state.ByteSize = 8
	state.Parity = 0   // NOPARITY
	state.StopBits = 0 // ONESTOPBIT

	if result, _, callErr := procSetCommState.Call(uintptr(p.handle),
		uintptr(unsafe.Pointer(&state))); result == 0 {
		return fmt.Errorf("SetCommState: %w", callErr)
	}

	// Return buffered bytes promptly and otherwise wake up periodically so a
	// blocked reader can observe Close.
	timeouts := commTimeouts{
		ReadIntervalTimeout:         50,
		ReadTotalTimeoutMultiplier:  0,
		ReadTotalTimeoutConstant:    100,
		WriteTotalTimeoutMultiplier: 0,
		WriteTotalTimeoutConstant:   5000,
	}
	if result, _, callErr := procSetCommTimeouts.Call(uintptr(p.handle),
		uintptr(unsafe.Pointer(&timeouts))); result == 0 {
		return fmt.Errorf("SetCommTimeouts: %w", callErr)
	}
	_, _, _ = procSetupComm.Call(uintptr(p.handle), 4096, 4096)
	return nil
}

func (p *windowsPort) Name() string { return p.name }

func (p *windowsPort) Read(buffer []byte) (int, error) {
	var read uint32
	err := syscall.ReadFile(p.handle, buffer, &read, nil)
	if err != nil {
		if err == syscall.ERROR_OPERATION_ABORTED ||
			err == errorInvalidHandle {
			return 0, io.EOF
		}
		return 0, err
	}
	return int(read), nil
}

func (p *windowsPort) Write(buffer []byte) (int, error) {
	var written uint32
	if err := syscall.WriteFile(p.handle, buffer, &written, nil); err != nil {
		if err == syscall.ERROR_OPERATION_ABORTED ||
			err == errorInvalidHandle {
			return 0, io.EOF
		}
		return int(written), err
	}
	return int(written), nil
}

func (p *windowsPort) Close() error {
	p.once.Do(func() {
		_, _, _ = procCancelIoEx.Call(uintptr(p.handle), 0)
		p.closeErr = syscall.CloseHandle(p.handle)
	})
	return p.closeErr
}
