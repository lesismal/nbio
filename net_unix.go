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

func init() {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		if n := int(limit.Max); n > 0 && n < MaxOpenFiles {
			MaxOpenFiles = n
		}
	}
}

//go:norace
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

	if _, isUDP := conn.(*net.UDPConn); isUDP {
		lAddrUDP := lAddr.(*net.UDPAddr)
		newLAddr := net.UDPAddr{
			IP:   make([]byte, len(lAddrUDP.IP)),
			Port: lAddrUDP.Port,
			Zone: lAddrUDP.Zone,
		}

		// use `for loop` instead of `copy` or `append` to avoid race warning, still don't know why, maybe fake warning.
		// copy(newLAddr.IP, lAddrUDP.IP)
		for i := range newLAddr.IP {
			newLAddr.IP[i] = lAddrUDP.IP[i]
		}

		c.lAddr = &newLAddr
		// c.lAddr = lAddrUDP
		if rAddr == nil {
			c.typ = ConnTypeUDPServer
			c.connUDP = &udpConn{
				parent: c,
				conns:  map[string]*Conn{},
			}
		} else {
			c.typ = ConnTypeUDPClientFromDial
			c.connUDP = &udpConn{
				parent: c,
			}
		}
	} else {
		c.typ = ConnTypeTCP
	}

	return c, nil
}
