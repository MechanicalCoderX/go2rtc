//go:build darwin || ios || freebsd || openbsd || netbsd || dragonfly

package onvif

import "syscall"

func setsockoptIPMreq(fd uintptr, level, opt int, mreq *syscall.IPMreq) error {
	return syscall.SetsockoptIPMreq(int(fd), level, opt, mreq)
}

func setsockoptInt(fd uintptr, level, opt, value int) error {
	// Set both SO_REUSEADDR and SO_REUSEPORT simultaneously on BSD-like systems.
	// https://stackoverflow.com/questions/14388706/how-do-so-reuseaddr-and-so-reuseport-differ
	if opt == syscall.SO_REUSEADDR {
		if err := syscall.SetsockoptInt(int(fd), level, opt, value); err != nil {
			return err
		}
		opt = syscall.SO_REUSEPORT
	}
	return syscall.SetsockoptInt(int(fd), level, opt, value)
}
