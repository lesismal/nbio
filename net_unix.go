// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"net"
	"syscall"
)

func sockaddrToAddr(sa syscall.Sockaddr) net.Addr {
	var a net.Addr
	switch sa := sa.(type) {
	case *syscall.SockaddrInet4:
		a = &net.TCPAddr{
			IP:   append([]byte{}, sa.Addr[:]...),
			Port: sa.Port,
		}
	case *syscall.SockaddrInet6:
		var zone string
		if sa.ZoneId != 0 {
			if ifi, err := net.InterfaceByIndex(int(sa.ZoneId)); err == nil {
				zone = ifi.Name
			}
		}
		a = &net.TCPAddr{
			IP:   append([]byte{}, sa.Addr[:]...),
			Port: sa.Port,
			Zone: zone,
		}
	case *syscall.SockaddrUnix:
		a = &net.UnixAddr{Net: "unix", Name: sa.Name}
	}
	return a
}

// func getSockaddr(proto, addr string) (sa syscall.Sockaddr, soType int, err error) {
// 	var tcp *net.TCPAddr

// 	tcp, err = net.ResolveTCPAddr(proto, addr)
// 	if err != nil && tcp.IP != nil {
// 		return nil, -1, err
// 	}

// 	tcpVersion, err := determineTCPProto(proto, tcp)
// 	if err != nil {
// 		return nil, -1, err
// 	}

// 	switch tcpVersion {
// 	case "tcp":
// 		return &syscall.SockaddrInet4{Port: tcp.Port}, syscall.AF_INET, nil
// 	case "tcp4":
// 		sa := &syscall.SockaddrInet4{Port: tcp.Port}

// 		if tcp.IP != nil {
// 			copy(sa.Addr[:], tcp.IP[12:16])
// 		}

// 		return sa, syscall.AF_INET, nil
// 	case "tcp6":
// 		sa := &syscall.SockaddrInet6{Port: tcp.Port}

// 		if tcp.IP != nil {
// 			copy(sa.Addr[:], tcp.IP)
// 		}

// 		if tcp.Zone != "" {
// 			iface, err := net.InterfaceByName(tcp.Zone)
// 			if err != nil {
// 				return nil, -1, err
// 			}

// 			sa.ZoneId = uint32(iface.Index)
// 		}

// 		return sa, syscall.AF_INET6, nil
// 	}

// 	return nil, -1, errors.New("unsupported protocol")
// }

// func determineTCPProto(proto string, ip *net.TCPAddr) (string, error) {
// 	if ip.IP.To4() != nil {
// 		return "tcp4", nil
// 	}

// 	if ip.IP.To16() != nil {
// 		return "tcp6", nil
// 	}

// 	switch proto {
// 	case "tcp", "tcp4", "tcp6":
// 		return proto, nil
// 	}

// 	return "", errors.New("unsupported protocol")
// }

// func listen(network, address string, backlogNum int64) (int, error) {
// 	var (
// 		err        error
// 		soType, fd int
// 		sockaddr   syscall.Sockaddr
// 	)

// 	if sockaddr, soType, err = getSockaddr(network, address); err != nil {
// 		return -1, err
// 	}

// 	syscall.ForkLock.RLock()
// 	defer syscall.ForkLock.RUnlock()
// 	if fd, err = syscall.Socket(soType, syscall.SOCK_STREAM, syscall.IPPROTO_TCP); err != nil {
// 		return -1, err
// 	}

// 	if err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
// 		syscall.Close(fd)
// 		return -1, err
// 	}

// 	socketOptReusePort := 0x0F
// 	if err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, socketOptReusePort, 1); err != nil {
// 		socketOptReusePort = 0x200
// 		if err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, socketOptReusePort, 1); err != nil {
// 			syscall.Close(fd)
// 			return -1, err
// 		}
// 	}

// 	if err = syscall.Bind(fd, sockaddr); err != nil {
// 		syscall.Close(fd)
// 		return -1, err
// 	}

// 	n := int(backlogNum)
// 	if backlogNum <= 0 {
// 		n = syscall.SOMAXCONN
// 	}
// 	if err = syscall.Listen(fd, n); err != nil {
// 		syscall.Close(fd)
// 		return -1, err
// 	}

// 	if err = syscall.SetNonblock(fd, true); err != nil {
// 		syscall.Close(fd)
// 		return -1, err
// 	}

// 	return fd, nil
// }

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

	conn.Close()

	err = syscall.SetNonblock(newFd, true)
	if err != nil {
		syscall.Close(newFd)
		return nil, err
	}

	return &Conn{
		fd:    newFd,
		lAddr: conn.LocalAddr(),
		rAddr: conn.RemoteAddr(),
	}, nil
}
