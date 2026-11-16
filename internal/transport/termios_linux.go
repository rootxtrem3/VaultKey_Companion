//go:build linux

package transport

import "syscall"

const (
	ioctlReadTermios  = syscall.TCGETS
	ioctlWriteTermios = syscall.TCSETS
)
