// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/lesismal/nbio/loging"
)

type MixedPool struct {
	*FixedNoOrderPool
	cuncurrent    int32
	maxCuncurrent int32
}

func (mp *MixedPool) call(f func()) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			loging.Error("taskpool call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
		atomic.AddInt32(&mp.cuncurrent, -1)
	}()
	f()
}

// Go .
func (mp *MixedPool) Go(f func()) {
	if atomic.AddInt32(&mp.cuncurrent, 1) <= mp.maxCuncurrent {
		go mp.call(f)
		return
	}
	atomic.AddInt32(&mp.cuncurrent, -1)
	mp.FixedNoOrderPool.Go(f)
}

// Go .
func (mp *MixedPool) Stop() {
	close(mp.chTask)
}

// NewMixedPool .
func NewMixedPool(totalSize int, fixedSize int, bufferSize int) *MixedPool {
	mp := &MixedPool{
		FixedNoOrderPool: NewFixedNoOrderPool(fixedSize, bufferSize),
		maxCuncurrent:    int32(totalSize),
	}

	return mp
}
