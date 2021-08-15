// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"fmt"
	"runtime"
	"sync"
)

const holderSize = 1024 * 1024 * 4

var holderBuffer = make([]byte, holderSize)

var DefaultMemPool = New(64)

// MemPool
type MemPool struct {
	minSize int
	pool    sync.Pool

	Debug       bool
	mux         sync.Mutex
	allocStacks map[*byte]string
	freeStacks  map[*byte]string
}

func New(minSize int) *MemPool {
	if minSize <= 0 {
		minSize = 64
	}
	mp := &MemPool{
		minSize:     minSize,
		allocStacks: map[*byte]string{},
		freeStacks:  map[*byte]string{},
		// Debug:       true,
	}
	mp.pool.New = func() interface{} {
		buf := make([]byte, minSize)
		return &buf
	}
	return mp
}

func (mp *MemPool) Malloc(size int) []byte {
	pbuf := mp.pool.Get().(*[]byte)
	if cap(*pbuf) < size {
		if cap(*pbuf)+holderSize >= size {
			*pbuf = (*pbuf)[:cap(*pbuf)]
			*pbuf = append(*pbuf, holderBuffer[:size-len(*pbuf)]...)
		} else {
			mp.pool.Put(pbuf)
			newBuf := make([]byte, size)
			pbuf = &newBuf
		}
	}

	if mp.Debug {
		mp.saveAllocStack(*pbuf)
	}

	return (*pbuf)[:size]
}

// Realloc .
func (mp *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	if cap(buf) < mp.minSize {
		return mp.Malloc(size)
	}
	pbuf := &buf
	if cap(*pbuf)+holderSize >= size {
		*pbuf = (*pbuf)[:cap(*pbuf)]
		*pbuf = append(*pbuf, holderBuffer[:size-len(*pbuf)]...)
	} else {
		mp.pool.Put(pbuf)
		newBuf := make([]byte, size)
		pbuf = &newBuf
	}

	if mp.Debug {
		mp.saveAllocStack(*pbuf)
	}
	return (*pbuf)[:size]
}

// Free .
func (mp *MemPool) Free(buf []byte) {
	if cap(buf) < mp.minSize {
		return
	}
	if mp.Debug {
		mp.saveFreeStack(buf)
	}
	mp.pool.Put(&buf)
}

func (mp *MemPool) saveFreeStack(buf []byte) {
	p := &(buf[:1][0])
	mp.mux.Lock()
	defer mp.mux.Unlock()
	s, ok := mp.freeStacks[p]
	if ok {
		allocStack := mp.allocStacks[p]
		err := fmt.Errorf("\nbuffer exists: %p\nprevious allocation:\n%v\nprevious free:\n%v\ncurrent free:\n%v", p, allocStack, s, getStack())
		panic(err)
	}
	mp.freeStacks[p] = getStack()
	delete(mp.allocStacks, p)
}

func (mp *MemPool) saveAllocStack(buf []byte) {
	p := &(buf[:1][0])
	mp.mux.Lock()
	defer mp.mux.Unlock()
	delete(mp.freeStacks, p)
	mp.allocStacks[p] = getStack()
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
func (a *NativeAllocator) Free(buf []byte) {
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
func Free(buf []byte) {
	DefaultMemPool.Free(buf)
}

func getStack() string {
	i := 2
	str := ""
	for ; i < 10; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		str += fmt.Sprintf("\tstack: %d %v [file: %s] [func: %s] [line: %d]\n", i-1, ok, file, runtime.FuncForPC(pc).Name(), line)
	}
	return str
}
