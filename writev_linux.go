// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import "golang.org/x/sys/unix"

func writev(fd int, iovs [][]byte) (int, error) {
	return unix.Writev(fd, iovs)
}
