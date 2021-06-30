// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package netmempool

import (
	"github.com/lesismal/nbio/mempool"
)

type Buffer struct {
	data        []byte
	offset      int
	notFromPool bool
}

func WrapBuffer(buf []byte) *Buffer {
	return &Buffer{
		data:        buf,
		notFromPool: true,
	}
}

func (b *Buffer) Remaining() []byte {
	return b.data[b.offset:]
}
func (b *Buffer) Consumed(i int) {
	b.offset += i
}

// Malloc borrows []byte from pool
func Malloc(size int) *Buffer {
	buf := mempool.Malloc(size)
	return &Buffer{
		data: buf,
	}
}

// Realloc returns the buf passed in if it's size <= cap
// else payback the buf to pool, then borrows and returns a new []byte from pool
func Realloc(buf *Buffer, size int) *Buffer {
	if size <= cap(buf.data) {
		buf.data = buf.data[:size]
		return buf
	}
	newBuf := Malloc(size)
	copy(newBuf.data, buf.data)
	Free(buf)
	return newBuf
}

// Free payback []byte to pool
func Free(buffer *Buffer) error {
	buf := buffer.data
	if buffer.notFromPool {
		return nil
	}
	return mempool.Free(buf)
}
