//go:build darwin

package transport

import "syscall"

const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)
