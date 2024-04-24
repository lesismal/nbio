package mempool

import (
	"fmt"
	"runtime/debug"
	"sync"
	"unsafe"
)

type TraceDebugger struct {
	mux       sync.Mutex
	pAlloced  map[uintptr]struct{}
	allocator Allocator
}

// Malloc .
func (td *TraceDebugger) Malloc(size int) []byte {
	buf := td.allocator.Malloc(size)
	td.setBufferPointer(buf)
	return buf
}

// Realloc .
func (td *TraceDebugger) Realloc(buf []byte, size int) []byte {
	newBuf := td.allocator.Realloc(buf, size)
	pold := td.pointer(buf)
	pnew := td.pointer(newBuf)
	if pnew != pold {
		td.deleteBufferPointer(buf)
		td.setBufferPointer(newBuf)
	}
	return newBuf
}

// Append .
func (td *TraceDebugger) Append(buf []byte, more ...byte) []byte {
	newBuf := td.allocator.Append(buf, more...)
	pold := td.pointer(buf)
	pnew := td.pointer(newBuf)
	if pnew != pold {
		td.deleteBufferPointer(buf)
		td.setBufferPointer(newBuf)
	}
	return newBuf
}

// AppendString .
func (td *TraceDebugger) AppendString(buf []byte, more string) []byte {
	newBuf := td.allocator.AppendString(buf, more)
	pold := td.pointer(buf)
	pnew := td.pointer(newBuf)
	if pnew != pold {
		td.deleteBufferPointer(buf)
		td.setBufferPointer(newBuf)
	}
	return newBuf
}

// Free .
func (td *TraceDebugger) Free(buf []byte) {
	td.deleteBufferPointer(buf)
	td.allocator.Free(buf)
}

func (td *TraceDebugger) setBufferPointer(buf []byte) {
	if len(buf) == 0 {
		td.printStack("invalid buf with length 0:")
		return
	}

	td.mux.Lock()
	defer td.mux.Unlock()
	p := *((*uintptr)(unsafe.Pointer(&(buf[0]))))
	if _, ok := td.pAlloced[p]; ok {
		td.printStack("re-alloc the same buf before free:")
		return
	}
	td.pAlloced[p] = struct{}{}
}

func (td *TraceDebugger) deleteBufferPointer(buf []byte) {
	if len(buf) == 0 {
		td.printStack("invalid buf with length 0:")
		return
	}

	td.mux.Lock()
	defer td.mux.Unlock()
	p := td.pointer(buf)
	if _, ok := td.pAlloced[p]; !ok {
		td.printStack("free un-allocated buf:")
		return
	}
	delete(td.pAlloced, p)
}

func (td *TraceDebugger) pointer(buf []byte) uintptr {
	p := *((*uintptr)(unsafe.Pointer(&(buf[0]))))
	return p
}

func (td *TraceDebugger) printStack(info string) {
	fmt.Println("-----------------------------")
	fmt.Println("[mempool trace] " + info + "\n")
	debug.PrintStack()
	fmt.Println("-----------------------------")
}
