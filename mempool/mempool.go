// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
	"unsafe"
)

type Allocator interface {
	Malloc(size int) []byte
	Realloc(buf []byte, size int) []byte
	Append(buf []byte, more ...byte) []byte
	AppendString(buf []byte, more string) []byte
	Free(buf []byte)
}

type AlignedAllocator interface {
	Malloc(size int) []byte
	Free(buf []byte)
}

// DefaultMemPool .
var DefaultMemPool = New(1024, 1024*1024*1024)
var DefaultAlignedMemPool = NewAligned()

// MemPool .
type MemPool struct {
	// Debug bool
	// mux   sync.Mutex

	bufSize  int
	freeSize int
	pool     *sync.Pool

	// allocCnt    uint64
	// freeCnt     uint64
	// allocStacks map[uintptr]string
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
func (mp *MemPool) Malloc(size int) []byte {
	if size > mp.freeSize {
		return make([]byte, size)
	}
	pbuf := mp.pool.Get().(*[]byte)
	n := cap(*pbuf)
	if n < size {
		*pbuf = append((*pbuf)[:n], make([]byte, size-n)...)
	}
	return (*pbuf)[:size]
}

// Realloc .
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
func (mp *MemPool) Append(buf []byte, more ...byte) []byte {
	return append(buf, more...)
}

// AppendString .
func (mp *MemPool) AppendString(buf []byte, more string) []byte {
	return append(buf, more...)
}

// Free .
func (mp *MemPool) Free(buf []byte) {
	if cap(buf) > mp.freeSize {
		return
	}
	mp.pool.Put(&buf)
}

const (
	minAlignedBufferSizeBits = 8
	maxAlignedBufferSizeBits = 16
	alignedBlockSize         = 1 << minAlignedBufferSizeBits
)

var alignedPool [8]sync.Pool
var alignedSizeMap [256]int
var alignedSizePoolMap [256]*sync.Pool

func init() {
	var poolSizes [8]int
	for i := range alignedPool {
		size := 1 << uint32(i+minAlignedBufferSizeBits)
		poolSizes[i] = size
		alignedPool[i].New = func() interface{} {
			return make([]byte, size)
		}
	}

	getPoolBySize := func(size int) *sync.Pool {
		for i, n := range poolSizes {
			if size <= n {
				return &alignedPool[i]
			}
		}
		return nil
	}

	for i := range alignedSizePoolMap {
		size := i * alignedBlockSize
		alignedSizeMap[i] = size
		alignedSizePoolMap[i] = getPoolBySize(size)
	}
}

// AlignedMemPool .
type AlignedMemPool struct{}

// NewAligned initiates a []byte allocator for frames less than 65536 bytes,
func NewAligned() *AlignedMemPool {
	return &AlignedMemPool{}
}

// Malloc .
func (amp *AlignedMemPool) Malloc(size int) []byte {
	if size < 0 {
		return nil
	}
	pool := amp.pool(size)
	if pool != nil {
		return pool.Get().([]byte)[:size]
	}
	return make([]byte, size)
}

// Realloc .
func (amp *AlignedMemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := amp.Malloc(size)
	copy(newBuf, buf)
	return newBuf
}

// Append .
func (amp *AlignedMemPool) Append(buf []byte, more ...byte) []byte {
	if cap(buf)-len(buf) >= len(more) {
		return append(buf, more...)
	}
	newBuf := amp.Malloc(len(buf) + len(more))
	copy(newBuf, buf)
	copy(newBuf[len(buf):], more)
	return newBuf
}

// AppendString .
func (amp *AlignedMemPool) AppendString(buf []byte, s string) []byte {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	more := *(*[]byte)(unsafe.Pointer(&h))
	return amp.Append(buf, more...)
}

// Free .
func (amp *AlignedMemPool) Free(buf []byte) {
	size := cap(buf)
	if size&alignedBlockSize != 0 {
		return
	}
	pool := amp.pool(size)
	if pool != nil {
		pool.Put(buf)
	}
}

func (amp *AlignedMemPool) pool(size int) *sync.Pool {
	idx := size >> minAlignedBufferSizeBits
	if idx < 256 {
		if alignedSizeMap[idx] < size {
			idx++
		}
		if idx < 256 {
			return alignedSizePoolMap[idx]
		}
	}
	return nil
}

// stdAllocator .
type stdAllocator struct{}

// Malloc .
func (a *stdAllocator) Malloc(size int) []byte {
	return make([]byte, size)
}

// Realloc .
func (a *stdAllocator) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := make([]byte, size)
	copy(newBuf, buf)
	return newBuf
}

// Free .
func (a *stdAllocator) Free(buf []byte) {
}

func (a *stdAllocator) Append(buf []byte, more ...byte) []byte {
	return append(buf, more...)
}

func (a *stdAllocator) AppendString(buf []byte, more string) []byte {
	return append(buf, more...)
}

func NewSTD() Allocator {
	return &stdAllocator{}
}

// Realloc exports default package method.
func Realloc(buf []byte, size int) []byte {
	return DefaultMemPool.Realloc(buf, size)
}

// Append exports default package method.
func Append(buf []byte, more ...byte) []byte {
	return DefaultMemPool.Append(buf, more...)
}

// AppendString exports default package method.
func AppendString(buf []byte, more string) []byte {
	return DefaultMemPool.AppendString(buf, more)
}

// Free exports default package method.
func Free(buf []byte) {
	DefaultMemPool.Free(buf)
}

func Malloc(size int) []byte {
	return DefaultAlignedMemPool.Malloc(size)
}

func Init(bufSize, freeSize int) {
	DefaultMemPool = New(bufSize, freeSize)
}
