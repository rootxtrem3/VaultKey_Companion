//go:build darwin

package prompt

import "syscall"

const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)
