// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"syscall"
)

func writev(c *Conn, iovs [][]byte) (int, error) {
	size := 0
	for _, v := range iovs {
		size += len(v)
	}
	buf := c.p.g.BodyAllocator.Malloc(size)[:0]
	for _, v := range iovs {
		buf = append(buf, v...)
	}
	n, err := syscall.Write(c.fd, buf)
	c.p.g.BodyAllocator.Free(buf)
	return n, err
}
