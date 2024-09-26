// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
)

// MemPool .
type MemPool struct {
	*debugger
	bufSize  int
	freeSize int
	pool     *sync.Pool
}

// New .
//
//go:norace
func New(bufSize, freeSize int) Allocator {
	if bufSize <= 0 {
		bufSize = 64
	}
	if freeSize <= 0 {
		freeSize = 64 * 1024
	}
	if freeSize < bufSize {
		freeSize = bufSize
	}

	mp := &MemPool{
		debugger: &debugger{},
		bufSize:  bufSize,
		freeSize: freeSize,
		pool:     &sync.Pool{},
		// Debug:       true,
	}
	mp.pool.New = func() interface{} {
		buf := make([]byte, bufSize)
		return &buf
	}
	return mp
}

// Malloc .
//
//go:norace
func (mp *MemPool) Malloc(size int) []byte {
	var ret []byte
	if size > mp.freeSize {
		ret = make([]byte, size)
		mp.incrMalloc(ret)
		return ret
	}
	pbuf := mp.pool.Get().(*[]byte)
	n := cap(*pbuf)
	if n < size {
		*pbuf = append((*pbuf)[:n], make([]byte, size-n)...)
	}
	ret = (*pbuf)[:size]
	mp.incrMalloc(ret)
	return ret
}

// Realloc .
//
//go:norace
func (mp *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}

	if cap(buf) < mp.freeSize {
		pbuf := mp.pool.Get().(*[]byte)
		n := cap(buf)
		if n < size {
			*pbuf = append((*pbuf)[:n], make([]byte, size-n)...)
		}
		*pbuf = (*pbuf)[:size]
		copy(*pbuf, buf)
		mp.Free(buf)
		return *pbuf
	}
	return append(buf[:cap(buf)], make([]byte, size-cap(buf))...)[:size]
}

// Append .
//
//go:norace
func (mp *MemPool) Append(buf []byte, more ...byte) []byte {
	return append(buf, more...)
}

// AppendString .
//
//go:norace
func (mp *MemPool) AppendString(buf []byte, more string) []byte {
	return append(buf, more...)
}

// Free .
//
//go:norace
func (mp *MemPool) Free(buf []byte) {
	mp.incrFree(buf)
	if cap(buf) > mp.freeSize {
		return
	}
	mp.pool.Put(&buf)
}
