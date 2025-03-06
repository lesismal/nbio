// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"net"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

const (
	// sysAF_UNSPEC = unix.AF_UNSPEC
	// sysAF_INET   = unix.AF_INET
	// sysAF_INET6  = unix.AF_INET6

	// sysSOCK_RAW = unix.SOCK_RAW

	sizeofSockaddrInet4 = unix.SizeofSockaddrInet4
	sizeofSockaddrInet6 = unix.SizeofSockaddrInet6
)

const (
	sysRECVMMSG = 0x12b
	sysSENDMMSG = 0x133
)

type iovec struct {
	Base *byte
	Len  uint64
}

type msghdr struct {
	Name       *byte
	Namelen    uint32
	Pad_cgo_0  [4]byte
	Iov        *iovec
	Iovlen     uint64
	Control    *byte
	Controllen uint64
	Flags      int32
	Pad_cgo_1  [4]byte
}

type mmsghdr struct {
	Hdr       msghdr
	Len       uint32
	Pad_cgo_0 [4]byte
}

type cmsghdr struct {
	Len   uint64
	Level int32
	Type  int32
}

func (v *iovec) set(b []byte) {
	l := len(b)
	if l == 0 {
		return
	}
	v.Base = (*byte)(unsafe.Pointer(&b[0]))
	v.Len = uint64(l)
}

func (h *msghdr) pack(vs []iovec, bs [][]byte, oob []byte, sa []byte) {
	for i := range vs {
		vs[i].set(bs[i])
	}
	h.setIov(vs)
	if len(oob) > 0 {
		h.setControl(oob)
	}
	if sa != nil {
		h.Name = (*byte)(unsafe.Pointer(&sa[0]))
		h.Namelen = uint32(len(sa))
	} else {
		h.Name = nil
		h.Namelen = 0
	}
}

func (h *msghdr) setIov(vs []iovec) {
	l := len(vs)
	if l == 0 {
		return
	}
	h.Iov = &vs[0]
	h.Iovlen = uint64(l)
}

func (h *msghdr) setControl(b []byte) {
	h.Control = (*byte)(unsafe.Pointer(&b[0]))
	h.Controllen = uint64(len(b))
}

func (h *msghdr) name() []byte {
	if h.Name != nil && h.Namelen > 0 {
		return (*[sizeofSockaddrInet6]byte)(unsafe.Pointer(h.Name))[:h.Namelen]
	}
	return nil
}

func (h *msghdr) controllen() int {
	return int(h.Controllen)
}

func (h *msghdr) flags() int {
	return int(h.Flags)
}

const (
	sizeofIovec  = 0x10
	sizeofMsghdr = 0x38
)

func udppack(hs []msghdr, parseFn func([]byte, string) (net.Addr, error), marshalFn func(net.Addr, []byte) int) mmsghdrs {
	vsRest := p.vs
	saRest := p.sockaddrs
	for i := range hs {
		nvs := len(ms[i].Buffers)
		vs := vsRest[:nvs]
		vsRest = vsRest[nvs:]

		var sa []byte
		if parseFn != nil {
			sa = saRest[:sizeofSockaddrInet6]
			saRest = saRest[sizeofSockaddrInet6:]
		} else if marshalFn != nil {
			n := marshalFn(ms[i].Addr, saRest)
			if n > 0 {
				sa = saRest[:n]
				saRest = saRest[n:]
			}
		}
		hs[i].Hdr.pack(vs, ms[i].Buffers, ms[i].OOB, sa)
	}
	return hs
}

// read batch from UDP socket.
//
//go:norace
func (c *Conn) readUDPBatch(hs []mmsghdr, flags int) (*Conn, int, error) {
	var err error
	conn := &ipv4.PacketConn{}
	// conn.ReadBatch()
	n, _, err := syscall.Syscall6(sysRECVMMSG, uintptr(c.fd), uintptr(unsafe.Pointer(&hs[0])), uintptr(len(hs)), uintptr(flags), 0, 0)
	// return int(n),syscall.Errno errnoErr(errno.Error)

	nread, rAddr, err := syscall.Recvfrom(c.fd, b, 0)
	if c.closeErr == nil {
		c.closeErr = err
	}
	if err != nil {
		return c, 0, err
	}

	var g = c.p.g
	var dstConn = c
	if c.typ == ConnTypeUDPServer {
		// get or create and cache the consistent connection for the socket
		// that has the same local addr and remote addr.
		uc, ok := c.connUDP.getConn(c.p, c.fd, rAddr)
		if g.UDPReadTimeout > 0 {
			uc.SetReadDeadline(time.Now().Add(g.UDPReadTimeout))
		}
		if !ok {
			g.onOpen(uc)
		}
		dstConn = uc
	}

	return dstConn, nread, err
}
