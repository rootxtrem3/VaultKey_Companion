//go:build linux || darwin

package prompt

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"
)

// Secret reads a line from the controlling terminal with echo disabled.
func Secret(label string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", ErrNoTerminal
	}
	defer tty.Close()

	fd := int(tty.Fd())
	var original syscall.Termios
	if err := termios(fd, ioctlReadTermios, &original); err != nil {
		return "", ErrNoTerminal
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO | syscall.ECHONL
	if err := termios(fd, ioctlWriteTermios, &hidden); err != nil {
		return "", ErrNoTerminal
	}

	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_ = termios(fd, ioctlWriteTermios, &original)
		})
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	finished := make(chan struct{})
	go func() {
		select {
		case <-signals:
			restore()
			fmt.Fprintln(tty)
			os.Exit(130)
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		signal.Stop(signals)
		restore()
	}()

	fmt.Fprint(tty, label)
	value, err := readLimitedLine(bufio.NewReader(tty), maxSecretLength)
	fmt.Fprintln(tty)
	restore()
	return value, err
}

// Confirm asks a yes/no question on the controlling terminal.
func Confirm(question string) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, ErrNoTerminal
	}
	defer tty.Close()
	fmt.Fprintf(tty, "%s [y/N] ", question)
	answer, err := readLimitedLine(bufio.NewReader(tty), 32)
	if err != nil {
		return false, err
	}
	return isYes(answer), nil
}

func termios(fd int, request uintptr, value *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), request,
		uintptr(unsafe.Pointer(value)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
