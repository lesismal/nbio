// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"io"
	"os"

	"github.com/lesismal/nbio/mempool"
)

// Sendfile .
func (c *Conn) Sendfile(f *os.File, remain int64) (written int64, err error) {
	if f == nil {
		return 0, nil
	}

	if remain <= 0 {
		stat, err := f.Stat()
		if err != nil {
			return 0, err
		}
		remain = stat.Size()
	}

	for remain > 0 {
		bufLen := 1024 * 32
		if bufLen > int(remain) {
			bufLen = int(remain)
		}
		buf := mempool.Malloc(bufLen)
		nr, er := f.Read(buf)
		if nr > 0 {
			nw, ew := c.Write(buf[0:nr])
			if nw < 0 {
				nw = 0
			}
			remain -= int64(nw)
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}
