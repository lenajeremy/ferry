package server

import (
	"net"
	"syscall"
)

// setMulticastInterface picks the network interface (by its address) multicast leaves from.
func setMulticastInterface(conn *net.UDPConn, ip net.IP) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var addr [4]byte
	copy(addr[:], ip.To4())
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInet4Addr(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, addr)
	}); err != nil {
		return err
	}
	return serr
}
