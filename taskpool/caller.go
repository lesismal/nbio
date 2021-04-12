// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"runtime"
	"unsafe"

	"github.com/lesismal/nbio/loging"
)

func call(f func()) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			loging.Error("taskpool call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()
	f()
}
