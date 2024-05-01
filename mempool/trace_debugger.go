package mempool

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

type TraceDebugger struct {
	mux       sync.Mutex
	pAlloced  map[uintptr]string
	allocator Allocator
}

func NewTraceDebuger(allocator Allocator) *TraceDebugger {
	return &TraceDebugger{
		allocator: allocator,
		pAlloced:  map[uintptr]string{},
	}
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
	if cap(buf) == 0 {
		// td.printStack("invalid buf with cap 0")
		return
	}
	td.deleteBufferPointer(buf)
	td.allocator.Free(buf)
}

func (td *TraceDebugger) setBufferPointer(buf []byte) {
	if cap(buf) == 0 {
		// td.printStack("invalid buf with cap 0")
		return
	}

	td.mux.Lock()
	defer td.mux.Unlock()
	p := td.pointer(buf)
	if s, ok := td.pAlloced[p]; ok {
		td.printStack("re-alloc the same buf before free:", s)
		return
	}
	td.pAlloced[p] = td.getStack()
}

func (td *TraceDebugger) deleteBufferPointer(buf []byte) {
	if cap(buf) == 0 {
		// td.printStack("invalid buf with cap 0")
		return
	}

	td.mux.Lock()
	defer td.mux.Unlock()
	p := td.pointer(buf)
	if s, ok := td.pAlloced[p]; !ok {
		td.printStack("free un-allocated buf:", s)
		return
	}
	delete(td.pAlloced, p)
}

func (td *TraceDebugger) pointer(buf []byte) uintptr {
	p := *((*uintptr)(unsafe.Pointer(&(buf[:1][0]))))
	return p
}

func (td *TraceDebugger) getStack() string {
	var (
		i      int
		errstr string
	)
	for {
		pc, file, line, ok := runtime.Caller(i)
		if !ok || i > 20 {
			break
		}
		errstr += fmt.Sprintf("\tstack: %d %v [file: %s] [func: %s] [line: %d]\n", i-1, ok, file, runtime.FuncForPC(pc).Name(), line)
		i++
	}
	return errstr
}

func (td *TraceDebugger) printStack(info, preStack string) {
	fmt.Println("-----------------------------")
	currStack := td.getStack()
	fmt.Printf(`-----------------------------
	[mempool trace] %v
	previous stack: 
	%v
	
	-----------------------------

	current stack :
	%v
	-----------------------------`, info, preStack, currStack)
}
