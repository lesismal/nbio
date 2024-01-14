// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"syscall"

	"github.com/lesismal/nbio/mempool"
)

func writev(fd int, iovs [][]byte) (int, error) {
	size := 0
	for _, v := range iovs {
		size += len(v)
	}
	buf := mempool.Malloc(size)[:0]
	for _, v := range iovs {
		buf = append(buf, v...)
	}
	n, err := syscall.Write(fd, buf)
	mempool.Free(buf)
	return n, err
}
