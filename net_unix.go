// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

func init() {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		if n := int(limit.Max); n > 0 && n < MaxOpenFiles {
			MaxOpenFiles = n
		}
	}
}

func dupStdConn(conn net.Conn) (*Conn, error) {
	sc, ok := conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, errors.New("RawConn Unsupported")
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return nil, errors.New("RawConn Unsupported")
	}

	var newFd int
	errCtrl := rc.Control(func(fd uintptr) {
		newFd, err = syscall.Dup(int(fd))
	})

	if errCtrl != nil {
		return nil, errCtrl
	}

	if err != nil {
		return nil, err
	}

	lAddr := conn.LocalAddr()
	rAddr := conn.RemoteAddr()

	conn.Close()

	// err = syscall.SetNonblock(newFd, true)
	// if err != nil {
	// 	syscall.Close(newFd)
	// 	return nil, err
	// }

	c := &Conn{
		fd:    newFd,
		lAddr: lAddr,
		rAddr: rAddr,
	}

	switch conn.(type) {
	case *net.TCPConn:
		c.typ = ConnTypeTCP
	case *net.UnixConn:
		c.typ = ConnTypeUnix
	case *net.UDPConn:
		lAddrUDP := lAddr.(*net.UDPAddr)
		newLAddr := net.UDPAddr{
			IP:   make([]byte, len(lAddrUDP.IP)),
			Port: lAddrUDP.Port,
			Zone: lAddrUDP.Zone,
		}

		copy(newLAddr.IP, lAddrUDP.IP)

		c.lAddr = &newLAddr

		// no remote addr, this is a listener
		if rAddr == nil {
			c.typ = ConnTypeUDPServer
			c.connUDP = &udpConn{
				parent: c,
				conns:  map[udpAddrKey]*Conn{},
			}
		} else {
			// has remote addr, this is a dialer
			c.typ = ConnTypeUDPClientFromDial
			c.connUDP = &udpConn{
				parent: c,
			}
		}
	default:
	}

	return c, nil
}

func parseDomainAndType(network, addr string) (int, int, syscall.Sockaddr, net.Addr, ConnType, error) {
	isIPv4 := len(strings.Split(addr, ":")) == 2

	socketResult := func(sockType int, connType ConnType) (int, int, syscall.Sockaddr, net.Addr, ConnType, error) {
		dstAddr, err := net.ResolveTCPAddr(network, addr)
		if err != nil {
			return 0, 0, nil, nil, 0, err
		}

		if isIPv4 {
			return syscall.AF_INET, sockType, &syscall.SockaddrInet4{
				Addr: [4]byte{dstAddr.IP[0], dstAddr.IP[1], dstAddr.IP[2], dstAddr.IP[3]},
				Port: dstAddr.Port,
			}, dstAddr, connType, nil
		}
		iface, err := net.InterfaceByName(dstAddr.Zone)
		if err != nil {
			return 0, 0, nil, nil, 0, err
		}
		addr6 := &syscall.SockaddrInet6{
			Port:   dstAddr.Port,
			ZoneId: uint32(iface.Index),
		}
		copy(addr6.Addr[:], dstAddr.IP)
		return syscall.AF_INET6, sockType, addr6, dstAddr, connType, nil
	}

	switch network {
	case NETWORK_TCP, NETWORK_TCP4, NETWORK_TCP6:
		return socketResult(syscall.SOCK_STREAM, ConnTypeTCP)
	case NETWORK_UDP, NETWORK_UDP4, NETWORK_UDP6:
		return socketResult(syscall.SOCK_DGRAM, ConnTypeUDPClientFromDial)
	case NETWORK_UNIX, NETWORK_UNIXGRAM, NETWORK_UNIXPACKET:
		sotype := syscall.SOCK_STREAM
		switch network {
		case NETWORK_UNIX:
			sotype = syscall.SOCK_STREAM
		case NETWORK_UNIXGRAM:
			sotype = syscall.SOCK_DGRAM
		case NETWORK_UNIXPACKET:
			sotype = syscall.SOCK_SEQPACKET
		default:
		}
		dstAddr := &net.UnixAddr{
			Net:  network,
			Name: addr,
		}
		return syscall.AF_UNIX, sotype, &syscall.SockaddrUnix{Name: addr}, dstAddr, ConnTypeUnix, nil
	default:
	}
	return 0, 0, nil, nil, 0, net.UnknownNetworkError(network)
}
