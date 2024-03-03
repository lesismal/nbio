// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

const maxSendfileSize = 4 << 20

// Sendfile .
func (c *Conn) Sendfile(f *os.File, remain int64) (int64, error) {
	if f == nil {
		return 0, nil
	}

	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}

	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	stat, err := f.Stat()
	if err != nil {
		c.closeWithErrorWithoutLock(err)
		return 0, err
	}
	size := stat.Size()
	if (remain <= 0) || (remain > size-offset) {
		remain = size - offset
	}

	src := int(f.Fd())
	if len(c.writeList) > 0 {
		src, err = syscall.Dup(src)
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			return 0, err
		}
		t := newToWriteFile(src, offset, remain)
		c.appendWrite(t)
		return remain, nil
	}

	// c.p.g.beforeWrite(c)

	var (
		n     int
		dst   = c.fd
		total = remain
	)

	err = syscall.SetNonblock(src, true)
	if err != nil {
		c.closeWithErrorWithoutLock(err)
		return 0, err
	}

	for remain > 0 {
		n = maxSendfileSize
		if int64(n) > remain {
			n = int(remain)
		}
		var tmpOffset = offset
		n, err = syscall.Sendfile(dst, src, &tmpOffset, n)
		if n > 0 {
			remain -= int64(n)
			offset += int64(n)
		} else if n == 0 && err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			src, err = syscall.Dup(src)
			if err == nil {
				t := newToWriteFile(src, offset, remain)
				c.appendWrite(t)
				// err = syscall.SetNonblock(src, true)
				// if err != nil {
				// 	c.closeWithErrorWithoutLock(err)
				// 	return 0, err
				// }
				c.modWrite()
			}
			break
		}
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			return 0, err
		}
	}

	return total, nil
}
