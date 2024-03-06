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

func writev(fd int, bs [][]byte) (int, error) {
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
		var n int
		var err error
		var _p0 = unsafe.Pointer(&iovs[0])
		var r0, _, e1 = syscall.Syscall(syscall.SYS_WRITEV, uintptr(fd), uintptr(_p0), uintptr(len(iovs)))
		n = int(r0)
		if e1 != 0 {
			err = syscall.Errno(e1)
		}
		return n, err
	}

	return 0, nil
}
