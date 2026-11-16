//go:build windows

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

const (
	enableEchoInput = 0x0004
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// Secret reads a line from the Windows console with echo disabled.
func Secret(label string) (string, error) {
	console, err := os.OpenFile("CONIN$", os.O_RDONLY, 0)
	if err != nil {
		return "", ErrNoTerminal
	}
	defer console.Close()

	handle := syscall.Handle(console.Fd())
	var mode uint32
	if result, _, _ := procGetConsoleMode.Call(uintptr(handle),
		uintptr(unsafe.Pointer(&mode))); result == 0 {
		return "", ErrNoTerminal
	}
	if result, _, _ := procSetConsoleMode.Call(uintptr(handle),
		uintptr(mode&^enableEchoInput)); result == 0 {
		return "", ErrNoTerminal
	}

	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_, _, _ = procSetConsoleMode.Call(uintptr(handle), uintptr(mode))
		})
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	finished := make(chan struct{})
	go func() {
		select {
		case <-signals:
			restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		signal.Stop(signals)
		restore()
	}()

	fmt.Fprint(os.Stderr, label)
	value, err := readLimitedLine(bufio.NewReader(console), maxSecretLength)
	fmt.Fprintln(os.Stderr)
	restore()
	return value, err
}

// Confirm asks a yes/no question on the Windows console.
func Confirm(question string) (bool, error) {
	console, err := os.OpenFile("CONIN$", os.O_RDONLY, 0)
	if err != nil {
		return false, ErrNoTerminal
	}
	defer console.Close()
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	answer, err := readLimitedLine(bufio.NewReader(console), 32)
	if err != nil {
		return false, err
	}
	return isYes(answer), nil
}
