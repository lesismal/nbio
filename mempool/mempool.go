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
func (mp *MemPool) Malloc(size int) *[]byte {
	var ret []byte
	if size > mp.freeSize {
		ret = make([]byte, size)
		mp.incrMalloc(&ret)
		return &ret
	}
	pbuf := mp.pool.Get().(*[]byte)
	n := cap(*pbuf)
	if n < size {
		*pbuf = append((*pbuf)[:n], make([]byte, size-n)...)
	}
	(*pbuf) = (*pbuf)[:size]
	mp.incrMalloc(pbuf)
	return pbuf
}

// Realloc .
func (mp *MemPool) Realloc(pbuf *[]byte, size int) *[]byte {
	if size <= cap(*pbuf) {
		*pbuf = (*pbuf)[:size]
		return pbuf
	}

	if cap(*pbuf) < mp.freeSize {
		newBufPtr := mp.pool.Get().(*[]byte)
		n := cap(*newBufPtr)
		if n < size {
			*newBufPtr = append((*newBufPtr)[:n], make([]byte, size-n)...)
		}
		*newBufPtr = (*newBufPtr)[:size]
		copy(*newBufPtr, *pbuf)
		mp.Free(pbuf)
		return newBufPtr
	}
	*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, size-cap(*pbuf))...)[:size]
	return pbuf
}

// Append .
func (mp *MemPool) Append(pbuf *[]byte, more ...byte) *[]byte {
	*pbuf = append(*pbuf, more...)
	return pbuf
}

// AppendString .
func (mp *MemPool) AppendString(pbuf *[]byte, more string) *[]byte {
	*pbuf = append(*pbuf, more...)
	return pbuf
}

// Free .
func (mp *MemPool) Free(pbuf *[]byte) {
	if pbuf != nil && cap(*pbuf) > 0 {
		mp.incrFree(pbuf)
		if cap(*pbuf) > mp.freeSize {
			return
		}
		mp.pool.Put(pbuf)
	}
}
