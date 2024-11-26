// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"syscall"
)

//go:norace
func writev(c *Conn, iovs [][]byte) (int, error) {
	size := 0
	for _, v := range iovs {
		size += len(v)
	}
	pbuf := c.p.g.BodyAllocator.Malloc(size)
	*pbuf = (*pbuf)[0:0]
	for _, v := range iovs {
		pbuf = c.p.g.BodyAllocator.Append(pbuf, v...)
	}
	n, err := syscall.Write(c.fd, *pbuf)
	c.p.g.BodyAllocator.Free(pbuf)
	return n, err
}
