// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"syscall"
	"unsafe"
)

//go:norace
func writev(c *Conn, bs [][]byte) (int, error) {
	iovs := make([]syscall.Iovec, len(bs))[0:0]
	for _, b := range bs {
		if len(b) > 0 {
			v := syscall.Iovec{}
			v.SetLen(len(b))
			v.Base = &b[0]
			iovs = append(iovs, v)
		}
	}

	if len(iovs) > 0 {
		var _p0 = unsafe.Pointer(&iovs[0])
		var n, _, err = syscall.Syscall(syscall.SYS_WRITEV, uintptr(c.fd), uintptr(_p0), uintptr(len(iovs)))
		if err == 0 {
			return int(n), nil
		}
		return int(n), err
	}
	return 0, nil
}
