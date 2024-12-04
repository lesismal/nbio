// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package nbio

import (
	"io"
	"os"
)

// Sendfile .
//
//go:norace
func (c *Conn) Sendfile(f *os.File, remain int64) (written int64, err error) {
	if f == nil {
		return 0, nil
	}

	if remain <= 0 {
		stat, e := f.Stat()
		if e != nil {
			return 0, e
		}
		remain = stat.Size()
	}

	for remain > 0 {
		bufLen := 1024 * 32
		if bufLen > int(remain) {
			bufLen = int(remain)
		}
		pbuf := c.p.g.BodyAllocator.Malloc(bufLen)
		nr, er := f.Read(*pbuf)
		if nr > 0 {
			nw, ew := c.Write((*pbuf)[0:nr])
			c.p.g.BodyAllocator.Free(pbuf)
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

	if c.p.g.onWrittenSize != nil && written > 0 {
		c.p.g.onWrittenSize(c, nil, int(written))
	}

	return written, err
}
