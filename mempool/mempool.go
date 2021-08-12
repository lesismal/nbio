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

	Debug        bool
	mux          sync.Mutex
	bufferStacks map[*byte]string
}

func New(minSize int) *MemPool {
	if minSize <= 0 {
		minSize = 64
	}
	mp := &MemPool{
		minSize:      minSize,
		bufferStacks: map[*byte]string{},
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
		mp.unsaveStack(&(*pbuf)[0])
	}

	return (*pbuf)[:size]
}

// Realloc .
// func (c *MemPool) Realloc(buf []byte, size int) []byte {
// 	if size <= cap(buf) {
// 		return buf[:size]
// 	}
// 	newBuf := c.Malloc(size)
// 	copy(newBuf, buf)
// 	c.Free(buf)
// 	return newBuf[:size]
// }

// Free .
func (mp *MemPool) Free(buf []byte) error {
	if cap(buf) < mp.minSize {
		return nil
	}
	if mp.Debug {
		mp.saveStack(&buf[0])
	}
	mp.pool.Put(&buf)
	return nil
}

func (mp *MemPool) saveStack(p *byte) {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	s, ok := mp.bufferStacks[p]
	if ok {
		sep := "--------------------------------------------------"
		err := fmt.Errorf("%vbuffer exists: %p\n%vprevious allocation: \n%v\n%vcurrent allocation: \n%v", sep, p, sep, s, sep, getStack())
		panic(err)
	}
	mp.bufferStacks[p] = getStack()
}

func (mp *MemPool) unsaveStack(p *byte) {
	mp.mux.Lock()
	defer mp.mux.Unlock()
	delete(mp.bufferStacks, p)
}

// Malloc exports default package method
func Malloc(size int) []byte {
	return DefaultMemPool.Malloc(size)
}

// Realloc exports default package method
// func Realloc(buf []byte, size int) []byte {
// 	return DefaultMemPool.Realloc(buf, size)
// }

// Free exports default package method
func Free(buf []byte) error {
	return DefaultMemPool.Free(buf)
}

func State() (int64, int64, string) {
	return 0, 0, ""
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
