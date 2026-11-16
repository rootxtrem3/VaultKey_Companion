//go:build linux

package prompt

import "syscall"

const (
	ioctlReadTermios  = syscall.TCGETS
	ioctlWriteTermios = syscall.TCSETS
)
