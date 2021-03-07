// Copyright 2021 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"errors"
	"sync"
)

var defaultMemPool = New(1024 * 1024 * 1024)

var pos = []byte{0, 1, 28, 2, 29, 14, 24, 3,
	30, 22, 20, 15, 25, 17, 4, 8, 31, 27, 13, 23, 21, 19,
	16, 7, 26, 12, 18, 6, 11, 5, 10, 9}

// MemPool definition
type MemPool struct {
	maxSize int
	buffers []sync.Pool
}

// debrujin algorithm
func (pool *MemPool) maxBits(size int) byte {
	v := uint32(size)
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v = (v >> 1) + 1
	ret := pos[(v*0x077CB531)>>27]
	if size > 1<<ret {
		ret++
	}
	return ret
}

// init buffers
func (pool *MemPool) init(maxSize int) {
	pool.maxSize = maxSize

	pool.buffers = make([]sync.Pool, pool.maxBits(maxSize)+1)
	for k := range pool.buffers {
		i := k
		pool.buffers[k].New = func() interface{} {
			return make([]byte, 1<<uint32(i))
		}
	}
}

// Malloc borrows []byte from pool
func (pool *MemPool) Malloc(size int) []byte {
	if size <= 0 || size > pool.maxSize {
		return nil
	}
	allocSize := size
	if size < 64 {
		allocSize = 64
	}
	return pool.buffers[pool.maxBits(allocSize)].Get().([]byte)[:size]
}

// Realloc returns the buf passed in if it's size <= cap
// else payback the buf to pool, then borrows and returns a new []byte from pool
func (pool *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	pool.Free(buf)
	return pool.Malloc(size)
}

// Free payback []byte to pool
func (pool *MemPool) Free(buf []byte) error {
	bits := pool.maxBits(cap(buf))
	if cap(buf) == 0 || cap(buf) > pool.maxSize || cap(buf) != 1<<bits {
		return errors.New("MemPool Put() incorrect buffer size")
	}
	pool.buffers[bits].Put(buf)
	return nil
}

// New factory
func New(maxSize int) *MemPool {
	pool := &MemPool{}
	pool.init(maxSize)
	return pool
}
