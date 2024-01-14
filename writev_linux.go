// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"syscall"

	"github.com/lesismal/nbio/mempool"
	"golang.org/x/sys/unix"
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

func writev2(fd int, iovs [][]byte) (int, error) {
	return unix.Writev(fd, iovs)
}
