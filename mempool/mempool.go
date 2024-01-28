// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"unsafe"
)

type Allocator interface {
	Malloc(size int) []byte
	Realloc(buf []byte, size int) []byte
	Append(buf []byte, more ...byte) []byte
	AppendString(buf []byte, more string) []byte
	Free(buf []byte)
}

type DebugAllocator interface {
	Allocator
	String() string
	SetDebug(bool)
}

// DefaultMemPool .
var DefaultMemPool = New(1024, 1024*1024*1024)
var DefaultAlignedMemPool = NewAligned()

type debugger struct {
	mux         sync.Mutex
	on          bool
	MallocCount int64
	FreeCount   int64
	NeedFree    int64
	SizeMap     map[int]*struct {
		MallocCount int64
		FreeCount   int64
		NeedFree    int64
	}
}

func (d *debugger) SetDebug(dbg bool) {
	d.on = dbg
}

func (d *debugger) incrMalloc(b []byte) {
	if d.on {
		d.incrMallocSlow(b)
	}
}
func (d *debugger) incrMallocSlow(b []byte) {
	atomic.AddInt64(&d.MallocCount, 1)
	atomic.AddInt64(&d.NeedFree, 1)
	size := cap(b)
	d.mux.Lock()
	defer d.mux.Unlock()
	if d.SizeMap == nil {
		d.SizeMap = map[int]*struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{}
	}
	if v, ok := d.SizeMap[size]; ok {
		v.MallocCount++
		v.NeedFree++
	} else {
		d.SizeMap[size] = &struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{
			MallocCount: 1,
			NeedFree:    1,
		}
	}
}

func (d *debugger) incrFree(b []byte) {
	if d.on {
		d.incrFreeSlow(b)
	}
}

func (d *debugger) incrFreeSlow(b []byte) {
	atomic.AddInt64(&d.FreeCount, 1)
	atomic.AddInt64(&d.NeedFree, -1)
	size := cap(b)
	d.mux.Lock()
	defer d.mux.Unlock()
	if v, ok := d.SizeMap[size]; ok {
		v.FreeCount++
		v.NeedFree--
	} else {
		d.SizeMap[size] = &struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{
			MallocCount: 1,
			NeedFree:    -1,
		}
	}
}

func (d *debugger) String() string {
	var ret string
	if d.on {
		b, _ := json.Marshal(d)
		ret = string(b)
	}
	return ret
}

// MemPool .
type MemPool struct {
	*debugger
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
	mp.incrFree(buf)
	if cap(buf) > mp.freeSize {
		return
	}
	mp.pool.Put(&buf)
}

const (
	minAlignedBufferSizeBits = 5
	maxAlignedBufferSizeBits = 16
	alignedPoolNum           = maxAlignedBufferSizeBits - minAlignedBufferSizeBits + 1
	minAlignedBufferSize     = 1 << minAlignedBufferSizeBits
	maxAlignedBufferSize     = 1 << maxAlignedBufferSizeBits
)

var alignedPools [alignedPoolNum]sync.Pool // 32-64k
var alignedPoolIndex [65537]byte

// var alignedSizeMap [256]int

func init() {
	var poolSizes [alignedPoolNum]int
	for i := range alignedPools {
		size := 1 << (i + minAlignedBufferSizeBits)
		poolSizes[i] = size
		alignedPools[i].New = func() interface{} {
			b := make([]byte, size)
			return &b
		}
	}

	getPoolBySize := func(size int) byte {
		for i, n := range poolSizes {
			if size <= n {
				return byte(i)
			}
		}
		return 0xFF
	}

	for i := range alignedPoolIndex {
		alignedPoolIndex[i] = getPoolBySize(i)
	}
}

// AlignedMemPool .
type AlignedMemPool struct {
	*debugger
}

// NewAligned initiates a []byte allocator for frames less than 65536 bytes,
func NewAligned() Allocator {
	return &AlignedMemPool{
		debugger: &debugger{},
	}
}

// Malloc .
func (amp *AlignedMemPool) Malloc(size int) []byte {
	if size < 0 {
		return nil
	}
	var ret []byte
	if size <= maxAlignedBufferSize {
		idx := alignedPoolIndex[size]
		ret = (*(alignedPools[idx].Get().(*[]byte)))[:size]
	} else {
		ret = make([]byte, size)
	}
	amp.incrMalloc(ret)
	return ret
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
	if size&minAlignedBufferSize != 0 || size > maxAlignedBufferSize {
		return
	}
	amp.incrFree(buf)
	idx := alignedPoolIndex[size]
	alignedPools[idx].Put(&buf)
}

// stdAllocator .
type stdAllocator struct {
	*debugger
}

// Malloc .
func (a *stdAllocator) Malloc(size int) []byte {
	ret := make([]byte, size)
	a.incrMalloc(ret)
	return ret
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
	a.incrFree(buf)
}

func (a *stdAllocator) Append(buf []byte, more ...byte) []byte {
	return append(buf, more...)
}

func (a *stdAllocator) AppendString(buf []byte, more string) []byte {
	return append(buf, more...)
}

func NewSTD() Allocator {
	return &stdAllocator{
		debugger: &debugger{},
	}
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
	return DefaultMemPool.Malloc(size)
}

func Init(bufSize, freeSize int) {
	DefaultMemPool = New(bufSize, freeSize)
}
