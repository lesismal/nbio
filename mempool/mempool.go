// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"fmt"
	"runtime"
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

// DefaultMemPool .
var DefaultMemPool = New(64, 64)

// MemPool .
type MemPool struct {
	Debug bool
	mux   sync.Mutex

	smallSize int
	bigSize   int
	smallPool *sync.Pool
	bigPool   *sync.Pool

	allocCnt    uint64
	freeCnt     uint64
	allocStacks map[uintptr]string
}

// New .
func New(smallSize, bigSize int) Allocator {
	if smallSize <= 0 {
		smallSize = 64
	}
	if bigSize <= 0 {
		bigSize = 64 * 1024
	}
	if bigSize < smallSize {
		bigSize = smallSize
	}

	mp := &MemPool{
		smallSize:   smallSize,
		bigSize:     bigSize,
		allocStacks: map[uintptr]string{},
		smallPool:   &sync.Pool{},
		bigPool:     &sync.Pool{},
		// Debug:       true,
	}
	mp.smallPool.New = func() interface{} {
		buf := make([]byte, smallSize)
		return &buf
	}
	mp.bigPool.New = func() interface{} {
		buf := make([]byte, bigSize)
		return &buf
	}
	if bigSize == smallSize {
		mp.bigPool = mp.smallPool
	}

	return mp
}

// Malloc .
func (mp *MemPool) Malloc(size int) []byte {
	pool := mp.smallPool
	if size >= mp.bigSize {
		pool = mp.bigPool
	}

	pbuf := pool.Get().(*[]byte)
	need := size - cap(*pbuf)
	if need > 0 {
		*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
	}

	if mp.Debug {
		mp.mux.Lock()
		defer mp.mux.Unlock()
		ptr := getBufferPtr(*pbuf)
		mp.addAllocStack(ptr)
	}

	return (*pbuf)[:size]
}

// Realloc .
func (mp *MemPool) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}

	if !mp.Debug {
		if cap(buf) < mp.bigSize && size >= mp.bigSize {
			pbuf := mp.bigPool.Get().(*[]byte)
			need := size - cap(*pbuf)
			if need > 0 {
				*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
			}
			*pbuf = (*pbuf)[:size]
			copy(*pbuf, buf)
			mp.Free(buf)
			return *pbuf
		}
		need := size - cap(buf)
		if need > 0 {
			buf = append(buf[:cap(buf)], make([]byte, need)...)
		}
		return buf[:size]
	}

	return mp.reallocDebug(buf, size)
}

func (mp *MemPool) reallocDebug(buf []byte, size int) []byte {
	if cap(buf) == 0 {
		panic("realloc zero size buf")
	}
	if cap(buf) < mp.bigSize && size >= mp.bigSize {
		pbuf := mp.bigPool.Get().(*[]byte)
		need := size - cap(*pbuf)
		if need > 0 {
			*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
		}
		*pbuf = (*pbuf)[:size]
		copy(*pbuf, buf)
		mp.Free(buf)
		ptr := getBufferPtr(*pbuf)
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.addAllocStack(ptr)
		return *pbuf
	}
	oldPtr := getBufferPtr(buf)
	need := size - cap(buf)
	if need > 0 {
		buf = append(buf[:cap(buf)], make([]byte, need)...)
	}
	newPtr := getBufferPtr(buf)
	if newPtr != oldPtr {
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.deleteAllocStack(oldPtr)
		mp.addAllocStack(newPtr)
	}

	return (buf)[:size]
}

// Append .
func (mp *MemPool) Append(buf []byte, more ...byte) []byte {
	if !mp.Debug {
		bl := len(buf)
		total := bl + len(more)
		if bl < mp.bigSize && total >= mp.bigSize {
			pbuf := mp.bigPool.Get().(*[]byte)
			need := total - cap(*pbuf)
			if need > 0 {
				*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
			}
			*pbuf = (*pbuf)[:total]
			copy(*pbuf, buf)
			copy((*pbuf)[bl:], more)
			mp.Free(buf)
			return *pbuf
		}
		return append(buf, more...)
	}
	return mp.appendDebug(buf, more...)
}

func (mp *MemPool) appendDebug(buf []byte, more ...byte) []byte {
	if cap(buf) == 0 {
		panic("append zero cap buf")
	}
	bl := len(buf)
	total := bl + len(more)
	if bl < mp.bigSize && total >= mp.bigSize {
		pbuf := mp.bigPool.Get().(*[]byte)
		need := total - cap(*pbuf)
		if need > 0 {
			*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
		}
		*pbuf = (*pbuf)[:total]
		copy(*pbuf, buf)
		copy((*pbuf)[bl:], more)
		mp.Free(buf)
		ptr := getBufferPtr(*pbuf)
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.addAllocStack(ptr)
		return *pbuf
	}

	oldPtr := getBufferPtr(buf)
	buf = append(buf, more...)
	newPtr := getBufferPtr(buf)
	if newPtr != oldPtr {
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.deleteAllocStack(oldPtr)
		mp.addAllocStack(newPtr)
	}
	return buf
}

// AppendString .
func (mp *MemPool) AppendString(buf []byte, more string) []byte {
	if !mp.Debug {
		bl := len(buf)
		total := bl + len(more)
		if bl < mp.bigSize && total >= mp.bigSize {
			pbuf := mp.bigPool.Get().(*[]byte)
			need := total - cap(*pbuf)
			if need > 0 {
				*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
			}
			*pbuf = (*pbuf)[:total]
			copy(*pbuf, buf)
			copy((*pbuf)[bl:], more)
			mp.Free(buf)
			return *pbuf
		}
		return append(buf, more...)
	}
	return mp.appendStringDebug(buf, more)
}

func (mp *MemPool) appendStringDebug(buf []byte, more string) []byte {
	if cap(buf) == 0 {
		panic("append zero cap buf")
	}
	bl := len(buf)
	total := bl + len(more)
	if bl < mp.bigSize && total >= mp.bigSize {
		pbuf := mp.bigPool.Get().(*[]byte)
		need := total - cap(*pbuf)
		if need > 0 {
			*pbuf = append((*pbuf)[:cap(*pbuf)], make([]byte, need)...)
		}
		*pbuf = (*pbuf)[:total]
		copy(*pbuf, buf)
		copy((*pbuf)[bl:], more)
		mp.Free(buf)
		ptr := getBufferPtr(*pbuf)
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.addAllocStack(ptr)
		return *pbuf
	}

	oldPtr := getBufferPtr(buf)
	buf = append(buf, more...)
	newPtr := getBufferPtr(buf)
	if newPtr != oldPtr {
		mp.mux.Lock()
		defer mp.mux.Unlock()
		mp.deleteAllocStack(oldPtr)
		mp.addAllocStack(newPtr)
	}
	return buf
}

// Free .
func (mp *MemPool) Free(buf []byte) {
	size := cap(buf)
	pool := mp.smallPool
	if size >= mp.bigSize {
		pool = mp.bigPool
	}

	if mp.Debug {
		mp.mux.Lock()
		defer mp.mux.Unlock()
		ptr := getBufferPtr(buf)
		mp.deleteAllocStack(ptr)
	}

	pool.Put(&buf)
}

func (mp *MemPool) addAllocStack(ptr uintptr) {
	mp.allocCnt++
	mp.allocStacks[ptr] = getStack()
}

func (mp *MemPool) deleteAllocStack(ptr uintptr) {
	if _, ok := mp.allocStacks[ptr]; !ok {
		panic("delete buffer which is not from pool")
	}
	mp.freeCnt++
	delete(mp.allocStacks, ptr)
}

func (mp *MemPool) LogDebugInfo() {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	fmt.Println("---------------------------------------------------------")
	fmt.Println("MemPool Debug Info:")
	fmt.Println("---------------------------------------------------------")
	for ptr, stack := range mp.allocStacks {
		fmt.Println("ptr:", ptr)
		fmt.Println("stack:\n", stack)
		fmt.Println("---------------------------------------------------------")
	}
	// fmt.Println("---------------------------------------------------------")
	// fmt.Println("Free")
	// for s, n := range mp.freeStacks {
	// 	fmt.Println("num:", n)
	// 	fmt.Println("stack:\n", s)
	// 	totalFree += n
	// 	fmt.Println("---------------------------------------------------------")
	// }
	fmt.Println("Alloc Without Free:", mp.allocCnt-mp.freeCnt)
	fmt.Println("TotalAlloc        :", mp.allocCnt)
	fmt.Println("TotalFree         :", mp.freeCnt)
	fmt.Println("---------------------------------------------------------")
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

func getBufferPtr(buf []byte) uintptr {
	if cap(buf) == 0 {
		panic("zero cap buffer")
	}
	return uintptr(unsafe.Pointer(&((buf)[:1][0])))
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
