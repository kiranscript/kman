//go:build unix

package main

import (
	"net"
	"syscall"
)

// listenUnixPrivate binds a Unix socket under umask 0177, so it is created
// 0600 instead of being open to others until the chmod after it.
func listenUnixPrivate(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}
