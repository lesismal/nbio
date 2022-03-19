// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"fmt"
	"runtime"
	"sync"
)

const maxAppendSize = 1024 * 1024 * 4

type Allocator interface {
	Malloc(size int) []byte
	Realloc(buf []byte, size int) []byte
	Append(buf []byte, more ...byte) []byte
	AppendString(buf []byte, more string) []byte
	Free(buf []byte)
}

// DefaultMemPool .
var DefaultMemPool = New(64)

// MemPool .
type MemPool struct {
	Debug       bool
	mux         sync.Mutex
	pool        sync.Pool
	minSize     int
	allocStacks map[string]int
	freeStacks  map[string]int
}

// New .
func New(minSize int) Allocator {
	if minSize <= 0 {
		minSize = 64
	}
	mp := &MemPool{
		minSize:     minSize,
		allocStacks: map[string]int{},
		freeStacks:  map[string]int{},
		// Debug:       true,
	}
	mp.pool.New = func() interface{} {
		buf := make([]byte, minSize)
		return &buf
	}
	return mp
}

// Malloc .
func (mp *MemPool) Malloc(size int) []byte {
	pbuf := mp.pool.Get().(*[]byte)
	need := size - cap(*pbuf)
	if need > 0 {
		if need <= maxAppendSize {
			*pbuf = (*pbuf)[:cap(*pbuf)]
			*pbuf = append(*pbuf, make([]byte, need)...)
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
		newBuf := mp.Malloc(size)
		copy(newBuf[:len(buf)], buf)
		return newBuf
	}
	pbuf := &buf
	need := size - cap(buf)
	if need <= maxAppendSize {
		*pbuf = (*pbuf)[:cap(*pbuf)]
		*pbuf = append(*pbuf, make([]byte, need)...)
	} else {
		mp.pool.Put(pbuf)
		newBuf := make([]byte, size)
		pbuf = &newBuf
	}
	copy((*pbuf)[:len(buf)], buf)

	if mp.Debug {
		mp.saveAllocStack(*pbuf)
	}
	return (*pbuf)[:size]
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
	if mp.Debug {
		mp.saveFreeStack(buf)
	}

	if cap(buf) < mp.minSize {
		return
	}

	mp.pool.Put(&buf)
}

func (mp *MemPool) saveFreeStack(buf []byte) {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	s := getStack()
	mp.freeStacks[s] = mp.freeStacks[s] + 1
}

func (mp *MemPool) saveAllocStack(buf []byte) {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	s := getStack()
	mp.allocStacks[s] = mp.allocStacks[s] + 1
}

func (mp *MemPool) LogDebugInfo() {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	totalAlloc := 0
	totalFree := 0
	fmt.Println("*********************************************************")
	fmt.Println("Alloc")
	for s, n := range mp.allocStacks {
		fmt.Println("num:", n)
		fmt.Println("stack:\n", s)
		totalAlloc += n
		fmt.Println("*********************************************************")
	}
	fmt.Println("---------------------------------------------------------")
	fmt.Println("Free")
	for s, n := range mp.freeStacks {
		fmt.Println("num:", n)
		fmt.Println("stack:\n", s)
		totalFree += n
		fmt.Println("---------------------------------------------------------")
	}
	fmt.Println("totalAlloc:", totalAlloc)
	fmt.Println("totalFree:", totalFree)
	fmt.Println("*********************************************************")
}

// NativeAllocator definition.
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

// Malloc exports default package method.
func Malloc(size int) []byte {
	return DefaultMemPool.Malloc(size)
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

// SetDebug .
func SetDebug(enable bool) {
	mp, ok := DefaultMemPool.(*MemPool)
	if ok {
		mp.Debug = enable
	}
}

// LogDebugInfo .
func LogDebugInfo() {
	mp, ok := DefaultMemPool.(*MemPool)
	if ok {
		mp.LogDebugInfo()
	}
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
