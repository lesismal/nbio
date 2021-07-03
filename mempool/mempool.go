// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	mallocCnt     int64
	mallocCntSize int64
	freeCnt       int64
	freeCntSize   int64
)

var DefaultMemPool = NewChosMemPool(64)

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

func printStack(s, c int) {
	// i := 2
	// str := ""
	// for ; i < 5; i++ {
	// 	pc, file, line, ok := runtime.Caller(i)
	// 	if !ok {
	// 		break
	// 	}
	// 	str += fmt.Sprintf("\tstack: %d %v [file: %s] [func: %s] [line: %d]\n", i-1, ok, file, runtime.FuncForPC(pc).Name(), line)
	// }
	// println("size:", s, "cap:", c)
	// println(str)
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
	buf := pool.buffers[pool.maxBits(allocSize)].Get().([]byte)[:size]
	atomic.AddInt64(&mallocCnt, 1)
	atomic.AddInt64(&mallocCntSize, int64(cap(buf)))
	// fmt.Println("+++ Malloc:", cap(buf))
	printStack(size, cap(buf))

	return buf
}

// Realloc returns the buf passed in if it's size <= cap
// else payback the buf to pool, then borrows and returns a new []byte from pool
func (pool *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := pool.Malloc(size)
	copy(newBuf, buf)
	pool.Free(buf)
	return newBuf
}

// Free payback []byte to pool
func (pool *MemPool) Free(buf []byte) error {
	bits := pool.maxBits(cap(buf))
	if cap(buf) == 0 || cap(buf) > pool.maxSize || cap(buf) != 1<<bits {
		return errors.New("MemPool Put() incorrect buffer size")
	}
	printStack(len(buf), cap(buf))
	atomic.AddInt64(&freeCnt, 1)
	atomic.AddInt64(&freeCntSize, int64(cap(buf)))
	pool.buffers[bits].Put(buf)
	// fmt.Println("--- Free:", cap(buf))
	// debug.PrintStack()
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

// NativeAllocator definition
type NativeAllocator struct{}

// Malloc .
func (a *NativeAllocator) Malloc(size int) []byte {
	return make([]byte, size)
}

// Realloc .
func (a *NativeAllocator) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := make([]byte, size)
	copy(newBuf, buf)
	return newBuf
}

// Free .
func (a *NativeAllocator) Free(buf []byte) error {
	return nil
}

// New factory
func New(maxSize int) *MemPool {
	pool := &MemPool{}
	pool.init(maxSize)
	return pool
}

func State() (int64, int64, int64, int64, string) {
	n1, n2, n3, n4 := atomic.LoadInt64(&mallocCnt), atomic.LoadInt64(&mallocCntSize), atomic.LoadInt64(&freeCnt), atomic.LoadInt64(&freeCntSize)
	s := fmt.Sprintf("malloc num : %v\nmalloc size: %v\nfree num   : %v\nfree size  : %v\nleft times : %v\nleft size  : %v\n", n1, n2, n3, n4, n1-n3, n2-n4)
	return n1, n2, n3, n4, s
}

// ChosMemPool
type ChosMemPool struct {
	minSize int
	pool    sync.Pool
}

func NewChosMemPool(minSize int) *ChosMemPool {
	if minSize <= 0 {
		minSize = 64
	}
	c := &ChosMemPool{
		minSize: minSize,
	}
	c.pool.New = func() interface{} {
		return make([]byte, minSize)
	}
	return c
}

func (c *ChosMemPool) Malloc(size int) []byte {
	buf := c.pool.Get().([]byte)
	if cap(buf) < size {
		c.Free(buf)
		buf = make([]byte, size)
	}
	atomic.AddInt64(&mallocCnt, 1)
	atomic.AddInt64(&mallocCntSize, int64(cap(buf)))
	return buf[:size]
}

// Realloc .
func (c *ChosMemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := c.Malloc(size)
	copy(newBuf, buf)
	c.pool.Put(buf)
	return newBuf[:size]
}

// Free .
func (c *ChosMemPool) Free(buf []byte) error {
	if cap(buf) < c.minSize {
		return nil
	}
	atomic.AddInt64(&freeCnt, 1)
	atomic.AddInt64(&freeCntSize, int64(cap(buf)))
	c.pool.Put(buf)
	return nil
}
