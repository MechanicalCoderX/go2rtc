//go:build !(darwin || ios || freebsd || openbsd || netbsd || dragonfly || windows)

package onvif

import "syscall"

func setsockoptInt(fd uintptr, level, opt, value int) error {
	return syscall.SetsockoptInt(int(fd), level, opt, value)
}

func setsockoptIPMreq(fd uintptr, level, opt int, mreq *syscall.IPMreq) error {
	return syscall.SetsockoptIPMreq(int(fd), level, opt, mreq)
}
