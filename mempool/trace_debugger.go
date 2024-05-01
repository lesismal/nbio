package mempool

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

var (
	stackMux    = sync.Mutex{}
	stackBuf    = make([]byte, 1024*8)
	stackMap    = map[string]uintptr{}
	nilStackPtr uintptr
)

type TraceDebugger struct {
	mux       sync.Mutex
	pAlloced  map[uintptr]uintptr
	allocator Allocator
}

func NewTraceDebuger(allocator Allocator) *TraceDebugger {
	return &TraceDebugger{
		allocator: allocator,
		pAlloced:  map[uintptr]uintptr{},
	}
}

// Malloc .
func (td *TraceDebugger) Malloc(size int) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	buf := td.allocator.Malloc(size)
	ptr := bytesPointer(buf)
	if stackPtr, ok := td.pAlloced[ptr]; ok {
		td.printStack("malloc got a buf which has been malloced by otherwhere", stackPtr)
	}
	td.setBufferPointer(ptr)
	return buf
}

// Realloc .
func (td *TraceDebugger) Realloc(buf []byte, size int) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	pold := bytesPointer(buf)
	if _, ok := td.pAlloced[pold]; !ok {
		td.printStack("realloc to a buf which has not been malloced", nilStackPtr)
	}
	newBuf := td.allocator.Realloc(buf, size)
	pnew := bytesPointer(newBuf)
	if pnew != pold {
		if preStack, ok := td.pAlloced[pnew]; ok {
			td.printStack("realloc got another new buf which has been malloced by otherwhere", preStack)
		}
		td.deleteBufferPointer(pold)
		td.setBufferPointer(pnew)
	}
	return newBuf
}

// Append .
func (td *TraceDebugger) Append(buf []byte, more ...byte) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	pold := bytesPointer(buf)
	if _, ok := td.pAlloced[pold]; !ok {
		td.printStack("Append to a buf which has not been malloced", nilStackPtr)
	}
	newBuf := td.allocator.Append(buf, more...)
	pnew := bytesPointer(newBuf)
	if pnew != pold {
		if preStack, ok := td.pAlloced[pnew]; ok {
			td.printStack("Append got another new buf which has been malloced by otherwhere", preStack)
		}
		td.deleteBufferPointer(pold)
		td.setBufferPointer(pnew)
	}
	return newBuf
}

// AppendString .
func (td *TraceDebugger) AppendString(buf []byte, more string) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	pold := bytesPointer(buf)
	if _, ok := td.pAlloced[pold]; !ok {
		td.printStack("AppendString to a buf which has not been malloced", nilStackPtr)
	}
	newBuf := td.allocator.AppendString(buf, more)
	pnew := bytesPointer(newBuf)
	if pnew != pold {
		if preStack, ok := td.pAlloced[pnew]; ok {
			td.printStack("AppendString got another new buf which has been malloced by otherwhere", preStack)
		}
		td.deleteBufferPointer(pold)
		td.setBufferPointer(pnew)
	}
	return newBuf
}

// Free .
func (td *TraceDebugger) Free(buf []byte) {
	td.mux.Lock()
	defer td.mux.Unlock()

	if cap(buf) == 0 {
		td.printStack("Free invalid buf with cap 0", nilStackPtr)
		return
	}
	ptr := bytesPointer(buf)
	if _, ok := td.pAlloced[ptr]; !ok {
		td.printStack("Free a buf which is not malloced by allocator", nilStackPtr)
	}
	td.deleteBufferPointer(ptr)
	td.allocator.Free(buf)
}

func (td *TraceDebugger) setBufferPointer(ptr uintptr) {
	td.pAlloced[ptr] = td.getStackPtr()
}

func (td *TraceDebugger) deleteBufferPointer(ptr uintptr) {
	delete(td.pAlloced, ptr)
}

func (td *TraceDebugger) getStackPtr() uintptr {
	stackMux.Lock()
	defer stackMux.Unlock()
	buf := stackBuf[:runtime.Stack(stackBuf, false)]
	key := *(*string)(unsafe.Pointer(&buf))
	if ptr, ok := stackMap[key]; ok {
		return ptr
	}
	key = string(buf)
	ptr := stringPointer(key)
	stackMap[key] = ptr
	return ptr
}

func (td *TraceDebugger) ptr2StackString(ptr uintptr) string {
	if ptr == nilStackPtr {
		return "nil"
	}
	return *((*string)(unsafe.Pointer(&ptr)))
}

func (td *TraceDebugger) printStack(info string, preStackPtr uintptr) {
	var (
		currStack = td.getStackPtr()
		preStack  = td.ptr2StackString(preStackPtr)
	)
	fmt.Printf(`
-------------------------------------------
[mempool trace] %v>>>

previous stack: 
%v

-------------------------------------------

current stack :
%v
-------------------------------------------\n\n`, info, preStack, currStack)
}

func bytesPointer(buf []byte) uintptr {
	return (uintptr)(unsafe.Pointer(&(buf[:1][0])))
}

func stringPointer(s string) uintptr {
	return (uintptr)(unsafe.Pointer(&s))
}
