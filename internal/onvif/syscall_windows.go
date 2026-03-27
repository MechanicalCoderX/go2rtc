//go:build windows

package onvif

import "syscall"

func setsockoptInt(fd uintptr, level, opt, value int) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), level, opt, value)
}

func setsockoptIPMreq(fd uintptr, level, opt int, mreq *syscall.IPMreq) error {
	return syscall.SetsockoptIPMreq(syscall.Handle(fd), level, opt, mreq)
}
