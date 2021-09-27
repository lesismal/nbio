// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"errors"
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
	if c.closed {
		c.mux.Unlock()
		return -1, errClosed
	}

	if remain <= 0 {
		stat, err := f.Stat()
		if err != nil {
			return 0, err
		}
		remain = stat.Size()
	}

	if len(c.writeBuffer) > 0 {
		if c.chWaitWrite == nil {
			c.chWaitWrite = make(chan struct{}, 1)
		}
		c.mux.Unlock()
		<-c.chWaitWrite
		if c.closed {
			c.chWaitWrite = nil
			return -1, errClosed
		}
		c.mux.Lock()
	}

	c.g.beforeWrite(c)

	var (
		err   error
		n     int
		src   = int(f.Fd())
		dst   = c.fd
		total = remain
	)

	for remain > 0 {
		n = maxSendfileSize
		if int64(n) > remain {
			n = int(remain)
		}
		n, err = syscall.Sendfile(dst, src, nil, n)
		if n > 0 {
			remain -= int64(n)
		} else if n == 0 && err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			c.modWrite()
			if c.chWaitWrite == nil {
				c.chWaitWrite = make(chan struct{}, 1)
			}
			c.mux.Unlock()
			<-c.chWaitWrite
			c.chWaitWrite = nil
			if c.closed {
				return total - remain, err
			}
			c.mux.Lock()
			continue
		}
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			c.mux.Unlock()
			return total - remain, err
		}
	}

	c.chWaitWrite = nil
	c.mux.Unlock()
	return total - remain, err
}
