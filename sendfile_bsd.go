// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"io"
	"os"
)

const maxSendfileSize = 4 << 20

// SendFile .
func (c *Conn) Sendfile(f *os.File, remain int64) (int64, error) {
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

	return io.CopyN(c, f, remain)
}
