// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
)

const holderSize = 1024 * 1024 * 4

var holderBuffer = make([]byte, holderSize)

var DefaultMemPool = New(64)

// MemPool
type MemPool struct {
	minSize int
	pool    sync.Pool
}

func New(minSize int) *MemPool {
	if minSize <= 0 {
		minSize = 64
	}
	c := &MemPool{
		minSize: minSize,
	}
	c.pool.New = func() interface{} {
		buf := make([]byte, minSize)
		return &buf
	}
	return c
}

func (c *MemPool) Malloc(size int) []byte {
	pbuf := c.pool.Get().(*[]byte)
	if cap(*pbuf) < size {
		if cap(*pbuf)+holderSize >= size {
			*pbuf = (*pbuf)[:cap(*pbuf)]
			*pbuf = append(*pbuf, holderBuffer[:size-len(*pbuf)]...)
		} else {
			c.pool.Put(pbuf)
			newBuf := make([]byte, size)
			pbuf = &newBuf
		}
	}

	return (*pbuf)[:size]
}

// Realloc .
func (c *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := c.Malloc(size)
	copy(newBuf, buf)
	c.Free(buf)
	return newBuf[:size]
}

// Free .
func (c *MemPool) Free(buf []byte) error {
	if cap(buf) < c.minSize {
		return nil
	}
	c.pool.Put(&buf)
	return nil
}

// Malloc exports default package method
func Malloc(size int) []byte {
	return DefaultMemPool.Malloc(size)
}

// Realloc exports default package method
func Realloc(buf []byte, size int) []byte {
	return DefaultMemPool.Realloc(buf, size)
}

// Free exports default package method
func Free(buf []byte) error {
	return DefaultMemPool.Free(buf)
}

func State() (int64, int64, string) {
	return 0, 0, ""
}
