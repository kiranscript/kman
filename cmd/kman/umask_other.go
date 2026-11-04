//go:build !unix

package main

import "net"

// listenUnixPrivate binds a Unix socket. Windows has no umask; the socket
// file takes the directory's ACL.
func listenUnixPrivate(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
